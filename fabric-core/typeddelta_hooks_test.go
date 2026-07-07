/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package validation

import (
	"testing"

	"github.com/hyperledger/fabric-protos-go/ledger/rwset/kvrwset"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/rwsetutil"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/typeddelta"
)

func deltaWrite(key string, op kvrwset.DeltaDescriptor_Op, n int64) *kvrwset.KVWrite {
	return &kvrwset.KVWrite{Key: key, Delta: &kvrwset.DeltaDescriptor{Op: op, Arg: typeddelta.MarshalInt64(n)}}
}

func rwsetWith(ns string, writes ...*kvrwset.KVWrite) *rwsetutil.TxRwSet {
	return &rwsetutil.TxRwSet{
		NsRwSets: []*rwsetutil.NsRwSet{
			{NameSpace: ns, KvRwSet: &kvrwset.KVRWSet{Writes: writes}},
		},
	}
}

func fakeLookup(schemas map[string]*storedSchema) schemaLookup {
	return func(ns, key string) (*storedSchema, bool, error) {
		s, ok := schemas[ns+"\x00"+key]
		return s, ok, nil
	}
}

func TestTxHasDeltaWrites(t *testing.T) {
	if txHasDeltaWrites(rwsetWith("acct", &kvrwset.KVWrite{Key: "x", Value: []byte("1")})) {
		t.Fatal("plain write should not be detected as delta")
	}
	if !txHasDeltaWrites(rwsetWith("acct", deltaWrite("a", kvrwset.DeltaDescriptor_SUB, 5))) {
		t.Fatal("delta write not detected")
	}
}

func TestToDelta(t *testing.T) {
	d, err := toDelta(&kvrwset.DeltaDescriptor{Op: kvrwset.DeltaDescriptor_SUB, Arg: typeddelta.MarshalInt64(7)})
	if err != nil {
		t.Fatalf("toDelta: %v", err)
	}
	if d.Op != typeddelta.OpSub || d.Arg != 7 {
		t.Fatalf("toDelta got %+v want {SUB 7}", d)
	}
	if _, err := toDelta(&kvrwset.DeltaDescriptor{Op: kvrwset.DeltaDescriptor_SETMAX, Arg: typeddelta.MarshalInt64(1)}); err == nil {
		t.Fatal("SETMAX should be unsupported in this slice")
	}
}

func TestFoldPublicDeltasSameKey(t *testing.T) {
	rws := rwsetWith("acct",
		deltaWrite("a", kvrwset.DeltaDescriptor_SUB, 10),
		deltaWrite("a", kvrwset.DeltaDescriptor_ADD, 8),
		deltaWrite("b", kvrwset.DeltaDescriptor_ADD, 3),
	)
	folded, err := foldPublicDeltas(rws)
	if err != nil {
		t.Fatalf("foldPublicDeltas: %v", err)
	}
	na, err := typeddelta.Apply(5, folded[compositeKey{"acct", "", "a"}])
	if err != nil {
		t.Fatalf("apply a: %v", err)
	}
	if na != 3 {
		t.Fatalf("a: got %d want 3", na)
	}
	nb, err := typeddelta.Apply(0, folded[compositeKey{"acct", "", "b"}])
	if err != nil {
		t.Fatalf("apply b: %v", err)
	}
	if nb != 3 {
		t.Fatalf("b: got %d want 3", nb)
	}
}

func plainWrite(key string, val []byte) *kvrwset.KVWrite {
	return &kvrwset.KVWrite{Key: key, Value: val}
}

func TestPlainTypedWriteBypass(t *testing.T) {
	lookup := fakeLookup(map[string]*storedSchema{
		"acct\x00bal": {TypeID: "balance_int64", Invariant: "NONNEG", Version: "v1"},
	})
	valid := func(rws *rwsetutil.TxRwSet) bool {
		ok, err := plainTypedWritesValid(rws, lookup)
		if err != nil {
			t.Fatalf("plainTypedWritesValid: %v", err)
		}
		return ok
	}
	touches := func(rws *rwsetutil.TxRwSet) bool {
		ok, err := txTouchesTypedState(rws, lookup)
		if err != nil {
			t.Fatalf("txTouchesTypedState: %v", err)
		}
		return ok
	}

	if !valid(rwsetWith("acct", plainWrite("bal", typeddelta.MarshalInt64(100)))) {
		t.Fatal("canonical non-negative plain write should be valid")
	}
	if valid(rwsetWith("acct", plainWrite("bal", typeddelta.MarshalInt64(-1)))) {
		t.Fatal("negative plain write to NONNEG key must be rejected")
	}
	if valid(rwsetWith("acct", plainWrite("bal", []byte("oops")))) {
		t.Fatal("non-canonical plain write to typed key must be rejected")
	}
	if !valid(rwsetWith("acct", plainWrite("other", []byte("anything")))) {
		t.Fatal("plain write to non-typed key must be unaffected")
	}
	if !touches(rwsetWith("acct", plainWrite("bal", typeddelta.MarshalInt64(1)))) {
		t.Fatal("txTouchesTypedState should detect plain write to typed key")
	}
	if touches(rwsetWith("acct", plainWrite("other", []byte("x")))) {
		t.Fatal("txTouchesTypedState should be false for non-typed plain write")
	}
}

func TestTypedKeyDelete(t *testing.T) {
	lookup := fakeLookup(map[string]*storedSchema{
		"acct\x00bal":   {TypeID: "balance_int64", Invariant: "NONNEG", Version: "v1"},
		"acct\x00quota": {TypeID: "quota_int64", Invariant: "RANGE", Lo: 5, Hi: 10, Version: "v1"},
	})
	del := func(key string) *kvrwset.KVWrite { return &kvrwset.KVWrite{Key: key, IsDelete: true} }
	valid := func(rws *rwsetutil.TxRwSet) bool {
		ok, err := plainTypedWritesValid(rws, lookup)
		if err != nil {
			t.Fatalf("plainTypedWritesValid: %v", err)
		}
		return ok
	}

	if !valid(rwsetWith("acct", del("bal"))) {
		t.Fatal("delete of NONNEG typed key should be allowed (0 holds)")
	}
	if valid(rwsetWith("acct", del("quota"))) {
		t.Fatal("delete of RANGE(5,10) typed key must be rejected (0 < lo)")
	}
}

func TestRejectValueAndDeltaSameKVWrite(t *testing.T) {
	w := &kvrwset.KVWrite{
		Key:   "bal",
		Value: typeddelta.MarshalInt64(100),
		Delta: &kvrwset.DeltaDescriptor{Op: kvrwset.DeltaDescriptor_SUB, Arg: typeddelta.MarshalInt64(5)},
	}
	if !malformedTypedWrites(rwsetWith("acct", w)) {
		t.Fatal("value+delta in same KVWrite must be malformed")
	}
}

func TestRejectPlainAndDeltaSameKeyInTx(t *testing.T) {
	rws := rwsetWith("acct",
		plainWrite("bal", typeddelta.MarshalInt64(100)),
		deltaWrite("bal", kvrwset.DeltaDescriptor_SUB, 5),
	)
	if !malformedTypedWrites(rws) {
		t.Fatal("plain write + delta write on same key in one tx must be malformed")
	}
	ok := rwsetWith("acct",
		plainWrite("other", typeddelta.MarshalInt64(100)),
		deltaWrite("bal", kvrwset.DeltaDescriptor_SUB, 5),
	)
	if malformedTypedWrites(ok) {
		t.Fatal("plain and delta on different keys must be allowed")
	}
}

func TestRejectDeleteAndDeltaSameKey(t *testing.T) {
	w := &kvrwset.KVWrite{
		Key:      "bal",
		IsDelete: true,
		Delta:    &kvrwset.DeltaDescriptor{Op: kvrwset.DeltaDescriptor_SUB, Arg: typeddelta.MarshalInt64(5)},
	}
	if !malformedTypedWrites(rwsetWith("acct", w)) {
		t.Fatal("delete+delta in same KVWrite must be malformed")
	}
	rws := rwsetWith("acct",
		&kvrwset.KVWrite{Key: "bal", IsDelete: true},
		deltaWrite("bal", kvrwset.DeltaDescriptor_SUB, 5),
	)
	if !malformedTypedWrites(rws) {
		t.Fatal("delete write + delta write on same key must be malformed")
	}
}

func TestStoredSchemaHelpers(t *testing.T) {
	s := &storedSchema{
		TypeID:     "balance_int64",
		AllowedOps: []int32{int32(typeddelta.OpAdd), int32(typeddelta.OpSub)},
		Invariant:  "NONNEG",
		Version:    "v1",
	}
	nn, err := s.invariant()
	if err != nil {
		t.Fatalf("NONNEG must resolve without error: %v", err)
	}
	if !nn.Holds(0) || nn.Holds(-1) {
		t.Fatal("expected NONNEG invariant semantics")
	}
	if !s.allowsOp(typeddelta.OpSub) || s.allowsOp(typeddelta.OpUnspecified) {
		t.Fatal("allowsOp should accept SUB and reject unspecified")
	}
	r := &storedSchema{Invariant: "RANGE", Lo: 5, Hi: 10}
	rinv, err := r.invariant()
	if err != nil {
		t.Fatalf("valid RANGE must resolve without error: %v", err)
	}
	if rinv.Holds(0) || !rinv.Holds(7) {
		t.Fatal("expected RANGE(5,10) semantics")
	}

	if _, err := (&storedSchema{Invariant: "BOGUS"}).invariant(); err == nil {
		t.Fatal("unknown invariant must return an error, not silently fall back to NONNEG")
	}
	if _, err := (&storedSchema{Invariant: ""}).invariant(); err == nil {
		t.Fatal("empty invariant must return an error")
	}
	if _, err := (&storedSchema{Invariant: "RANGE", Lo: 10, Hi: 5}).invariant(); err == nil {
		t.Fatal("RANGE with lo > hi must return an error")
	}
	if _, err := (&storedSchema{Invariant: "RANGE", Lo: 7, Hi: 7}).invariant(); err != nil {
		t.Fatalf("RANGE with lo == hi must be valid: %v", err)
	}
}

func TestPlainTypedWriteUnknownInvariantInvalid(t *testing.T) {
	lookup := fakeLookup(map[string]*storedSchema{
		"acct\x00bal":   {TypeID: "balance_int64", Invariant: "BOGUS", Version: "v1"},
		"acct\x00quota": {TypeID: "quota_int64", Invariant: "RANGE", Lo: 10, Hi: 5, Version: "v1"},
	})
	for _, key := range []string{"bal", "quota"} {
		ok, err := plainTypedWritesValid(rwsetWith("acct", plainWrite(key, typeddelta.MarshalInt64(100))), lookup)
		if err != nil {
			t.Fatalf("%s: malformed schema must be a per-tx invalid, not a fatal error: %v", key, err)
		}
		if ok {
			t.Fatalf("%s: plain write under an unknown/malformed invariant must be rejected", key)
		}
	}
}
