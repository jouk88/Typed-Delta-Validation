/*
Copyright IBM Corp. All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package validation

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/hyperledger/fabric-protos-go/ledger/rwset/kvrwset"
	"github.com/hyperledger/fabric/core/ledger/internal/version"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/privacyenabledstate"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/typeddelta"
)

func benchKey(i int) string { return fmt.Sprintf("acct%05d", i) }

func benchValidator(b *testing.B, invariant string, n int) *validator {
	env := &privacyenabledstate.LevelDBTestEnv{}
	env.Init(b)
	db := env.GetDBHandle("benchtypeddelta")
	b.Cleanup(env.Cleanup)
	v := &validator{db: db, hashFunc: testHashFunc, typedDeltaEnabled: true}

	s := &storedSchema{
		TypeID:     tdType,
		AllowedOps: []int32{int32(typeddelta.OpAdd), int32(typeddelta.OpSub)},
		Invariant:  invariant,
		Version:    tdVer,
	}
	if invariant == "RANGE" {
		s.Lo = math.MinInt64
		s.Hi = math.MaxInt64
	}
	js, _ := json.Marshal(s)
	batch := privacyenabledstate.NewUpdateBatch()
	h := version.NewHeight(1, 0)
	for i := 0; i < n; i++ {
		batch.PubUpdates.Put(tdNS, schemaStorageKey(benchKey(i)), js, h)
		batch.PubUpdates.Put(tdNS, benchKey(i), typeddelta.MarshalInt64(1_000_000), h)
	}
	if err := v.db.ApplyPrivacyAwareUpdates(batch, version.NewHeight(1, 100)); err != nil {
		b.Fatal(err)
	}
	return v
}

func plainValidator(b *testing.B) *validator {
	env := &privacyenabledstate.LevelDBTestEnv{}
	env.Init(b)
	db := env.GetDBHandle("benchplain")
	b.Cleanup(env.Cleanup)
	return &validator{db: db, hashFunc: testHashFunc, typedDeltaEnabled: true}
}

func deltaBlock(n int) *block {
	txs := make([]*transaction, n)
	for i := 0; i < n; i++ {
		txs[i] = deltaTx(fmt.Sprintf("tx%05d", i), i, tdDelta(benchKey(i), kvrwset.DeltaDescriptor_ADD, 1))
	}
	return &block{num: 2, txs: txs}
}

func plainBlock(n int) *block {
	txs := make([]*transaction, n)
	for i := 0; i < n; i++ {
		txs[i] = deltaTx(fmt.Sprintf("tx%05d", i), i, plainWrite("plain"+benchKey(i), typeddelta.MarshalInt64(1)))
	}
	return &block{num: 2, txs: txs}
}

func BenchmarkTypedDeltaValidation(b *testing.B) {
	for _, n := range []int{10, 50, 100, 500} {
		b.Run(fmt.Sprintf("plain/deltas=%d", n), func(b *testing.B) {
			v := plainValidator(b)
			blk := plainBlock(n)
			b.ResetTimer()
			for j := 0; j < b.N; j++ {
				if _, _, err := v.validateAndPrepareBatch(blk, true); err != nil {
					b.Fatal(err)
				}
			}
		})
		for _, inv := range []string{"NONNEG", "RANGE"} {
			b.Run(fmt.Sprintf("%s/deltas=%d", inv, n), func(b *testing.B) {
				v := benchValidator(b, inv, n)
				blk := deltaBlock(n)
				b.ResetTimer()
				for j := 0; j < b.N; j++ {
					if _, _, err := v.validateAndPrepareBatch(blk, true); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
