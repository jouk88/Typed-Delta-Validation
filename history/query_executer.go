/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package history

import (
	"github.com/hyperledger/fabric-protos-go/common"
	"github.com/hyperledger/fabric-protos-go/ledger/queryresult"
	"github.com/hyperledger/fabric-protos-go/ledger/rwset/kvrwset"
	commonledger "github.com/hyperledger/fabric/common/ledger"
	"github.com/hyperledger/fabric/common/ledger/blkstorage"
	"github.com/hyperledger/fabric/common/ledger/util/leveldbhelper"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/rwsetutil"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/typeddelta"
	protoutil "github.com/hyperledger/fabric/protoutil"
	"github.com/pkg/errors"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
)

// QueryExecutor is a query executor against the LevelDB history DB
type QueryExecutor struct {
	levelDB    *leveldbhelper.DBHandle
	blockStore *blkstorage.BlockStore
}

// GetHistoryForKey implements method in interface `ledger.HistoryQueryExecutor`
func (q *QueryExecutor) GetHistoryForKey(namespace string, key string) (commonledger.ResultsIterator, error) {
	rangeScan := constructRangeScan(namespace, key)
	dbItr, err := q.levelDB.GetIterator(rangeScan.startKey, rangeScan.endKey)
	if err != nil {
		return nil, err
	}
	defer dbItr.Release()

	var mods []*queryresult.KeyModification
	var running int64
	for dbItr.Next() {
		historyKey := dbItr.Key()
		blockNum, tranNum, err := rangeScan.decodeBlockNumTranNum(historyKey)
		if err != nil {
			return nil, err
		}
		tranEnvelope, err := q.blockStore.RetrieveTxByBlockNumTranNum(blockNum, tranNum)
		if err != nil {
			return nil, err
		}
		mod, next, err := foldKeyModificationFromTran(tranEnvelope, namespace, key, running)
		if err != nil {
			return nil, err
		}
		if mod == nil {
			logger.Errorf("no namespace/key found for namespace %s key %s at blockNum %d tranNum %d", namespace, key, blockNum, tranNum)
			continue
		}
		running = next
		mods = append(mods, mod)
	}
	if err := dbItr.Error(); err != nil {
		return nil, errors.WithMessage(err, "error iterating history db")
	}

	for i, j := 0, len(mods)-1; i < j; i, j = i+1, j-1 {
		mods[i], mods[j] = mods[j], mods[i]
	}
	return &historyScanner{mods: mods}, nil
}

type historyScanner struct {
	mods []*queryresult.KeyModification
	pos  int
}

func (scanner *historyScanner) Next() (commonledger.QueryResult, error) {
	if scanner.pos >= len(scanner.mods) {
		return nil, nil
	}
	m := scanner.mods[scanner.pos]
	scanner.pos++
	return m, nil
}

func (scanner *historyScanner) Close() {}

func foldKeyModificationFromTran(tranEnvelope *common.Envelope, namespace, key string, running int64) (*queryresult.KeyModification, int64, error) {
	writes, txID, ts, err := getKeyWritesFromTran(tranEnvelope, namespace, key)
	if err != nil {
		return nil, running, err
	}
	if len(writes) == 0 {
		return nil, running, nil
	}

	for _, w := range writes {
		if w.Delta != nil {
			continue
		}
		if rwsetutil.IsKVWriteDelete(w) {
			return &queryresult.KeyModification{
				TxId: txID, Value: nil, Timestamp: ts, IsDelete: true,
			}, 0, nil
		}
		next := running
		if v, verr := typeddelta.UnmarshalInt64(w.Value); verr == nil {
			next = v
		}
		return &queryresult.KeyModification{
			TxId: txID, Value: w.Value, Timestamp: ts, IsDelete: false,
		}, next, nil
	}

	deltas := make([]typeddelta.Delta, 0, len(writes))
	for _, w := range writes {
		d, derr := deltaFromDescriptor(w.Delta)
		if derr != nil {
			return &queryresult.KeyModification{
				TxId: txID, Value: writes[0].Value, Timestamp: ts, IsDelete: rwsetutil.IsKVWriteDelete(writes[0]),
			}, running, nil
		}
		deltas = append(deltas, d)
	}
	next, aerr := typeddelta.Apply(running, deltas)
	if aerr != nil {
		next = running
	}
	return &queryresult.KeyModification{
		TxId: txID, Value: typeddelta.MarshalInt64(next), Timestamp: ts, IsDelete: false,
	}, next, nil
}

func deltaFromDescriptor(d *kvrwset.DeltaDescriptor) (typeddelta.Delta, error) {
	arg, err := typeddelta.UnmarshalInt64(d.Arg)
	if err != nil {
		return typeddelta.Delta{}, err
	}
	if arg < 0 {
		return typeddelta.Delta{}, errors.New("history: delta arg must be non-negative")
	}
	switch d.Op {
	case kvrwset.DeltaDescriptor_ADD:
		return typeddelta.Delta{Op: typeddelta.OpAdd, Arg: arg}, nil
	case kvrwset.DeltaDescriptor_SUB:
		return typeddelta.Delta{Op: typeddelta.OpSub, Arg: arg}, nil
	default:
		return typeddelta.Delta{}, errors.Errorf("history: unsupported delta op %v", d.Op)
	}
}

func getKeyWritesFromTran(tranEnvelope *common.Envelope, namespace string, key string) ([]*kvrwset.KVWrite, string, *timestamppb.Timestamp, error) {
	payload, err := protoutil.UnmarshalPayload(tranEnvelope.Payload)
	if err != nil {
		return nil, "", nil, err
	}
	tx, err := protoutil.UnmarshalTransaction(payload.Data)
	if err != nil {
		return nil, "", nil, err
	}
	_, respPayload, err := protoutil.GetPayloads(tx.Actions[0])
	if err != nil {
		return nil, "", nil, err
	}
	chdr, err := protoutil.UnmarshalChannelHeader(payload.Header.ChannelHeader)
	if err != nil {
		return nil, "", nil, err
	}
	txID := chdr.TxId
	timestamp := chdr.Timestamp

	txRWSet := &rwsetutil.TxRwSet{}
	if err = txRWSet.FromProtoBytes(respPayload.Results); err != nil {
		return nil, "", nil, err
	}
	for _, nsRWSet := range txRWSet.NsRwSets {
		if nsRWSet.NameSpace == namespace {
			var writes []*kvrwset.KVWrite
			for _, kvWrite := range nsRWSet.KvRwSet.Writes {
				if kvWrite.Key == key {
					writes = append(writes, kvWrite)
				}
			}
			if len(writes) == 0 {
				logger.Debugf("key [%s] not found in namespace [%s]'s writeset", key, namespace)
			}
			return writes, txID, timestamp, nil
		}
	}
	logger.Debugf("namespace [%s] not found in transaction's ReadWriteSets", namespace)
	return nil, "", nil, nil
}
