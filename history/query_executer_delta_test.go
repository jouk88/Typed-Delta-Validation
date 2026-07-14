/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package history

import (
	"testing"

	"github.com/golang/protobuf/proto"
	"github.com/hyperledger/fabric-protos-go/ledger/queryresult"
	"github.com/hyperledger/fabric-protos-go/ledger/rwset"
	"github.com/hyperledger/fabric-protos-go/ledger/rwset/kvrwset"
	"github.com/hyperledger/fabric/common/ledger/testutil"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/typeddelta"
	"github.com/stretchr/testify/require"
)

func TestHistoryForTypedDeltaKey(t *testing.T) {
	env := newTestHistoryEnv(t)
	defer env.cleanup()
	provider := env.testBlockStorageEnv.provider
	store, err := provider.Open("ledger1")
	require.NoError(t, err)
	defer store.Shutdown()

	bg, gb := testutil.NewBlockGenerator(t, "ledger1", false)
	historydb := env.testHistoryDBProvider.GetDBHandle("ledger1")
	require.NoError(t, store.AddBlock(gb))
	require.NoError(t, historydb.Commit(gb))

	commit := func(txid string, w *kvrwset.KVWrite) {
		kvRWSet := &kvrwset.KVRWSet{Writes: []*kvrwset.KVWrite{w}}
		kvBytes, err := proto.Marshal(kvRWSet)
		require.NoError(t, err)
		txRWSet := &rwset.TxReadWriteSet{
			NsRwset: []*rwset.NsReadWriteSet{{Namespace: "ns1", Rwset: kvBytes}},
		}
		txBytes, err := proto.Marshal(txRWSet)
		require.NoError(t, err)
		blk := bg.NextBlockWithTxid([][]byte{txBytes}, []string{txid})
		require.NoError(t, store.AddBlock(blk))
		require.NoError(t, historydb.Commit(blk))
	}
	sub := func(n int64) *kvrwset.DeltaDescriptor {
		return &kvrwset.DeltaDescriptor{Op: kvrwset.DeltaDescriptor_SUB, Arg: typeddelta.MarshalInt64(n)}
	}
	add := func(n int64) *kvrwset.DeltaDescriptor {
		return &kvrwset.DeltaDescriptor{Op: kvrwset.DeltaDescriptor_ADD, Arg: typeddelta.MarshalInt64(n)}
	}

	commit("tx-seed", &kvrwset.KVWrite{Key: "acct", Value: typeddelta.MarshalInt64(100)})
	commit("tx-sub30", &kvrwset.KVWrite{Key: "acct", Delta: sub(30)})
	commit("tx-sub20", &kvrwset.KVWrite{Key: "acct", Delta: sub(20)})
	commit("tx-add5", &kvrwset.KVWrite{Key: "acct", Delta: add(5)})

	qe, err := historydb.NewQueryExecutor(store)
	require.NoError(t, err)
	itr, err := qe.GetHistoryForKey("ns1", "acct")
	require.NoError(t, err)

	var got []int64
	for {
		kmod, err := itr.Next()
		require.NoError(t, err)
		if kmod == nil {
			break
		}
		km := kmod.(*queryresult.KeyModification)
		require.False(t, km.IsDelete, "typed-delta history entry should not be a delete")
		v, uerr := typeddelta.UnmarshalInt64(km.Value)
		require.NoError(t, uerr, "history value must be canonical materialized int64")
		got = append(got, v)
	}

	require.Equal(t, []int64{55, 50, 70, 100}, got)
}

func TestHistoryForTypedDeltaMultiWritePerTx(t *testing.T) {
	env := newTestHistoryEnv(t)
	defer env.cleanup()
	provider := env.testBlockStorageEnv.provider
	store, err := provider.Open("ledger1")
	require.NoError(t, err)
	defer store.Shutdown()

	bg, gb := testutil.NewBlockGenerator(t, "ledger1", false)
	historydb := env.testHistoryDBProvider.GetDBHandle("ledger1")
	require.NoError(t, store.AddBlock(gb))
	require.NoError(t, historydb.Commit(gb))

	commit := func(txid string, writes ...*kvrwset.KVWrite) {
		kvRWSet := &kvrwset.KVRWSet{Writes: writes}
		kvBytes, err := proto.Marshal(kvRWSet)
		require.NoError(t, err)
		txRWSet := &rwset.TxReadWriteSet{
			NsRwset: []*rwset.NsReadWriteSet{{Namespace: "ns1", Rwset: kvBytes}},
		}
		txBytes, err := proto.Marshal(txRWSet)
		require.NoError(t, err)
		blk := bg.NextBlockWithTxid([][]byte{txBytes}, []string{txid})
		require.NoError(t, store.AddBlock(blk))
		require.NoError(t, historydb.Commit(blk))
	}
	sub := func(n int64) *kvrwset.DeltaDescriptor {
		return &kvrwset.DeltaDescriptor{Op: kvrwset.DeltaDescriptor_SUB, Arg: typeddelta.MarshalInt64(n)}
	}

	commit("tx-seed", &kvrwset.KVWrite{Key: "acct", Value: typeddelta.MarshalInt64(100)})
	commit("tx-two-subs",
		&kvrwset.KVWrite{Key: "acct", Delta: sub(30)},
		&kvrwset.KVWrite{Key: "acct", Delta: sub(20)},
	)

	qe, err := historydb.NewQueryExecutor(store)
	require.NoError(t, err)
	itr, err := qe.GetHistoryForKey("ns1", "acct")
	require.NoError(t, err)

	var got []int64
	for {
		kmod, err := itr.Next()
		require.NoError(t, err)
		if kmod == nil {
			break
		}
		v, uerr := typeddelta.UnmarshalInt64(kmod.(*queryresult.KeyModification).Value)
		require.NoError(t, uerr)
		got = append(got, v)
	}
	require.Equal(t, []int64{50, 100}, got)
}
