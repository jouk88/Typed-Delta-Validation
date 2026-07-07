/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package validation

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hyperledger/fabric-protos-go/ledger/rwset/kvrwset"
	"github.com/hyperledger/fabric-protos-go/peer"
	"github.com/hyperledger/fabric/core/ledger/internal/version"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/privacyenabledstate"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/rwsetutil"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/typeddelta"
	"github.com/stretchr/testify/require"
)

const (
	tdNS   = "acct"
	tdType = "balance_int64"
	tdVer  = "v1"
)

func newTypedDeltaValidator(t *testing.T, enabled bool) *validator {
	env := &privacyenabledstate.LevelDBTestEnv{}
	env.Init(t)
	db := env.GetDBHandle("typeddeltadb")
	v := &validator{db: db, hashFunc: testHashFunc, typedDeltaEnabled: enabled}
	t.Cleanup(env.Cleanup)
	return v
}

func nonNeg(ver string) *storedSchema {
	return &storedSchema{
		TypeID:     tdType,
		AllowedOps: []int32{int32(typeddelta.OpAdd), int32(typeddelta.OpSub)},
		Invariant:  "NONNEG",
		Version:    ver,
	}
}

func commitAt(t *testing.T, v *validator, blkNum uint64, schemas map[string]*storedSchema, initial map[string]int64) {
	batch := privacyenabledstate.NewUpdateBatch()
	h := version.NewHeight(blkNum, 0)
	for key, s := range schemas {
		js, err := json.Marshal(s)
		require.NoError(t, err)
		batch.PubUpdates.Put(tdNS, schemaStorageKey(key), js, h)
	}
	for key, val := range initial {
		batch.PubUpdates.Put(tdNS, key, typeddelta.MarshalInt64(val), h)
	}
	require.NoError(t, v.db.ApplyPrivacyAwareUpdates(batch, version.NewHeight(blkNum, 100)))
}

func tdDelta(key string, op kvrwset.DeltaDescriptor_Op, n int64) *kvrwset.KVWrite {
	return tdDeltaVer(key, op, n, tdType, tdVer)
}

func tdDeltaVer(key string, op kvrwset.DeltaDescriptor_Op, n int64, typeID, ver string) *kvrwset.KVWrite {
	return &kvrwset.KVWrite{Key: key, Delta: &kvrwset.DeltaDescriptor{
		TypeId:        typeID,
		SchemaVersion: []byte(ver),
		Op:            op,
		Arg:           typeddelta.MarshalInt64(n),
	}}
}

func deltaTx(id string, idx int, writes ...*kvrwset.KVWrite) *transaction {
	return &transaction{
		id:             id,
		indexInBlock:   idx,
		validationCode: peer.TxValidationCode_VALID,
		rwset:          rwsetWith(tdNS, writes...),
	}
}

func plainRWTx(id string, idx int, key string, readVer *version.Height, writeVal []byte) *transaction {
	kvrw := &kvrwset.KVRWSet{
		Reads:  []*kvrwset.KVRead{{Key: key, Version: &kvrwset.Version{BlockNum: readVer.BlockNum, TxNum: readVer.TxNum}}},
		Writes: []*kvrwset.KVWrite{{Key: key, Value: writeVal}},
	}
	return &transaction{
		id:             id,
		indexInBlock:   idx,
		validationCode: peer.TxValidationCode_VALID,
		rwset:          &rwsetutil.TxRwSet{NsRwSets: []*rwsetutil.NsRwSet{{NameSpace: tdNS, KvRwSet: kvrw}}},
	}
}

func validateAndCommit(t *testing.T, v *validator, num uint64, txs ...*transaction) {
	blk := &block{num: num, txs: txs}
	u, _, err := v.validateAndPrepareBatch(blk, true)
	require.NoError(t, err)
	commitUpdates(t, v, u, num, len(txs))
}

func commitUpdates(t *testing.T, v *validator, u *publicAndHashUpdates, num uint64, n int) {
	batch := privacyenabledstate.NewUpdateBatch()
	batch.PubUpdates = u.publicUpdates
	batch.HashUpdates = u.hashUpdates
	require.NoError(t, v.db.ApplyPrivacyAwareUpdates(batch, version.NewHeight(num, uint64(n))))
}

func dbInt(t *testing.T, v *validator, key string) (int64, bool) {
	vv, err := v.db.GetState(tdNS, key)
	require.NoError(t, err)
	if vv == nil {
		return 0, false
	}
	val, err := typeddelta.UnmarshalInt64(vv.Value)
	require.NoError(t, err)
	return val, true
}

func TestTypedDeltaCapabilityOffFailsafe(t *testing.T) {
	v := newTypedDeltaValidator(t, false)
	commitAt(t, v, 1, map[string]*storedSchema{"A": nonNeg(tdVer)}, map[string]int64{"A": 5})

	tx := deltaTx("tx1", 0, tdDelta("A", kvrwset.DeltaDescriptor_ADD, 3))
	validateAndCommit(t, v, 2, tx)

	require.Equal(t, peer.TxValidationCode_INVALID_OTHER_REASON, tx.validationCode)
	got, _ := dbInt(t, v, "A")
	require.Equal(t, int64(5), got, "no materialization when capability is off")
}

func TestTypedDeltaOrderedPrefixNonNeg(t *testing.T) {
	v := newTypedDeltaValidator(t, true)
	commitAt(t, v, 1, map[string]*storedSchema{"A": nonNeg(tdVer)}, map[string]int64{"A": 5})

	tx1 := deltaTx("tx1", 0, tdDelta("A", kvrwset.DeltaDescriptor_SUB, 6))
	tx2 := deltaTx("tx2", 1, tdDelta("A", kvrwset.DeltaDescriptor_ADD, 5))
	validateAndCommit(t, v, 2, tx1, tx2)

	require.Equal(t, peer.TxValidationCode_INVALID_OTHER_REASON, tx1.validationCode)
	require.Equal(t, peer.TxValidationCode_VALID, tx2.validationCode)
	got, ok := dbInt(t, v, "A")
	require.True(t, ok)
	require.Equal(t, int64(10), got)
}

func TestTypedDeltaWithinTxAtomicFold(t *testing.T) {
	v := newTypedDeltaValidator(t, true)
	commitAt(t, v, 1, map[string]*storedSchema{"A": nonNeg(tdVer)}, map[string]int64{"A": 5})

	tx := deltaTx("tx1", 0,
		tdDelta("A", kvrwset.DeltaDescriptor_SUB, 10),
		tdDelta("A", kvrwset.DeltaDescriptor_ADD, 8),
	)
	validateAndCommit(t, v, 2, tx)

	require.Equal(t, peer.TxValidationCode_VALID, tx.validationCode)
	got, ok := dbInt(t, v, "A")
	require.True(t, ok)
	require.Equal(t, int64(3), got)
}

func TestTypedDeltaTransferAtomicFail(t *testing.T) {
	v := newTypedDeltaValidator(t, true)
	commitAt(t, v, 1,
		map[string]*storedSchema{"A": nonNeg(tdVer), "B": nonNeg(tdVer)},
		map[string]int64{"A": 5, "B": 0},
	)

	tx := deltaTx("tx1", 0,
		tdDelta("A", kvrwset.DeltaDescriptor_SUB, 10),
		tdDelta("B", kvrwset.DeltaDescriptor_ADD, 10),
	)
	validateAndCommit(t, v, 2, tx)

	require.Equal(t, peer.TxValidationCode_INVALID_OTHER_REASON, tx.validationCode)
	a, _ := dbInt(t, v, "A")
	b, _ := dbInt(t, v, "B")
	require.Equal(t, int64(5), a, "A unchanged (all-or-nothing)")
	require.Equal(t, int64(0), b, "B credit not applied when A debit fails")
}

func TestTypedDeltaUnknownInvariantInvalid(t *testing.T) {
	v := newTypedDeltaValidator(t, true)
	bogus := &storedSchema{
		TypeID:     tdType,
		AllowedOps: []int32{int32(typeddelta.OpAdd), int32(typeddelta.OpSub)},
		Invariant:  "BOGUS",
		Version:    tdVer,
	}
	commitAt(t, v, 1, map[string]*storedSchema{"A": bogus}, map[string]int64{"A": 5})

	tx := deltaTx("tx1", 0, tdDelta("A", kvrwset.DeltaDescriptor_ADD, 3))
	validateAndCommit(t, v, 2, tx)

	require.Equal(t, peer.TxValidationCode_INVALID_OTHER_REASON, tx.validationCode)
	got, _ := dbInt(t, v, "A")
	require.Equal(t, int64(5), got, "no materialization under an unknown-invariant schema")
}

func TestTypedDeltaMalformedRangeInvalid(t *testing.T) {
	v := newTypedDeltaValidator(t, true)
	bad := &storedSchema{
		TypeID:     tdType,
		AllowedOps: []int32{int32(typeddelta.OpAdd), int32(typeddelta.OpSub)},
		Invariant:  "RANGE",
		Lo:         10,
		Hi:         5,
		Version:    tdVer,
	}
	commitAt(t, v, 1, map[string]*storedSchema{"A": bad}, map[string]int64{"A": 5})

	tx := deltaTx("tx1", 0, tdDelta("A", kvrwset.DeltaDescriptor_ADD, 3))
	validateAndCommit(t, v, 2, tx)

	require.Equal(t, peer.TxValidationCode_INVALID_OTHER_REASON, tx.validationCode)
	got, _ := dbInt(t, v, "A")
	require.Equal(t, int64(5), got, "no materialization under a malformed RANGE schema")
}

func TestTypedDeltaVersionBumpInvalidatesStalePlain(t *testing.T) {
	v := newTypedDeltaValidator(t, true)
	commitAt(t, v, 1, map[string]*storedSchema{"A": nonNeg(tdVer)}, map[string]int64{"A": 5})

	tx1 := deltaTx("tx1", 0, tdDelta("A", kvrwset.DeltaDescriptor_ADD, 3))
	tx2 := plainRWTx("tx2", 1, "A", version.NewHeight(1, 0), typeddelta.MarshalInt64(0))
	validateAndCommit(t, v, 2, tx1, tx2)

	require.Equal(t, peer.TxValidationCode_VALID, tx1.validationCode)
	require.Equal(t, peer.TxValidationCode_MVCC_READ_CONFLICT, tx2.validationCode)
	got, ok := dbInt(t, v, "A")
	require.True(t, ok)
	require.Equal(t, int64(8), got)
}

func TestPlainBeforeDelta(t *testing.T) {
	v := newTypedDeltaValidator(t, true)
	commitAt(t, v, 1, map[string]*storedSchema{"A": nonNeg(tdVer)}, nil)

	tx1 := deltaTx("tx1", 0, plainWrite("A", typeddelta.MarshalInt64(100)))
	tx2 := deltaTx("tx2", 1, tdDelta("A", kvrwset.DeltaDescriptor_ADD, 5))
	validateAndCommit(t, v, 2, tx1, tx2)

	require.Equal(t, peer.TxValidationCode_VALID, tx1.validationCode)
	require.Equal(t, peer.TxValidationCode_VALID, tx2.validationCode)
	got, ok := dbInt(t, v, "A")
	require.True(t, ok)
	require.Equal(t, int64(105), got)
}

func TestTypedDeltaMalformedWriteRejected(t *testing.T) {
	v := newTypedDeltaValidator(t, true)
	commitAt(t, v, 1, map[string]*storedSchema{"A": nonNeg(tdVer)}, map[string]int64{"A": 5})

	bad := &kvrwset.KVWrite{
		Key:   "A",
		Value: typeddelta.MarshalInt64(100),
		Delta: &kvrwset.DeltaDescriptor{TypeId: tdType, SchemaVersion: []byte(tdVer), Op: kvrwset.DeltaDescriptor_SUB, Arg: typeddelta.MarshalInt64(1)},
	}
	tx := deltaTx("tx1", 0, bad)
	validateAndCommit(t, v, 2, tx)

	require.Equal(t, peer.TxValidationCode_INVALID_OTHER_REASON, tx.validationCode)
	got, _ := dbInt(t, v, "A")
	require.Equal(t, int64(5), got)
}

func TestTypedDeltaRecoveryMaterializesValid(t *testing.T) {
	v := newTypedDeltaValidator(t, true)
	commitAt(t, v, 1, map[string]*storedSchema{"A": nonNeg(tdVer)}, map[string]int64{"A": 5})

	tx := deltaTx("tx1", 0, tdDelta("A", kvrwset.DeltaDescriptor_ADD, 7))
	blk := &block{num: 2, txs: []*transaction{tx}}
	u, _, err := v.validateAndPrepareBatch(blk, false)
	require.NoError(t, err)
	commitUpdates(t, v, u, 2, 1)

	got, ok := dbInt(t, v, "A")
	require.True(t, ok)
	require.Equal(t, int64(12), got)
}

func TestTypedDeltaSchemaBindingRejected(t *testing.T) {
	v := newTypedDeltaValidator(t, true)
	commitAt(t, v, 1, map[string]*storedSchema{"A": nonNeg(tdVer)}, map[string]int64{"A": 5})

	cases := []struct {
		name  string
		write *kvrwset.KVWrite
	}{
		{"no schema for key", tdDelta("undeclared", kvrwset.DeltaDescriptor_ADD, 1)},
		{"wrong type_id", tdDeltaVer("A", kvrwset.DeltaDescriptor_ADD, 1, "other_type", tdVer)},
		{"stale schema_version", tdDeltaVer("A", kvrwset.DeltaDescriptor_ADD, 1, tdType, "v0")},
		{"op not allowed", tdDeltaVer("A", kvrwset.DeltaDescriptor_SETMAX, 1, tdType, tdVer)},
	}
	for i, c := range cases {
		tx := deltaTx(c.name, 0, c.write)
		validateAndCommit(t, v, uint64(10+i), tx)
		require.Equalf(t, peer.TxValidationCode_INVALID_OTHER_REASON, tx.validationCode, "case %q must be invalid", c.name)
	}
	got, _ := dbInt(t, v, "A")
	require.Equal(t, int64(5), got)
}

func TestSchemaNotVisibleWithinSameTx(t *testing.T) {
	v := newTypedDeltaValidator(t, true)
	schemaJS, err := json.Marshal(nonNeg(tdVer))
	require.NoError(t, err)
	rwset := &rwsetutil.TxRwSet{NsRwSets: []*rwsetutil.NsRwSet{
		{NameSpace: tdNS, KvRwSet: &kvrwset.KVRWSet{Writes: []*kvrwset.KVWrite{
			{Key: schemaStorageKey("A"), Value: schemaJS},
			tdDelta("A", kvrwset.DeltaDescriptor_ADD, 3),
		}}},
	}}
	tx := &transaction{id: "tx1", indexInBlock: 0, validationCode: peer.TxValidationCode_VALID, rwset: rwset}
	validateAndCommit(t, v, 2, tx)

	require.Equal(t, peer.TxValidationCode_INVALID_OTHER_REASON, tx.validationCode,
		"a delta whose schema is declared in the SAME tx must be invalid (schema not self-visible)")
}

func TestSchemaUpgradeOrdering(t *testing.T) {
	v := newTypedDeltaValidator(t, true)
	commitAt(t, v, 1, map[string]*storedSchema{"A": nonNeg("v1")}, map[string]int64{"A": 5})

	tx1 := deltaTx("tx1", 0, tdDeltaVer("A", kvrwset.DeltaDescriptor_ADD, 3, tdType, "v1"))
	validateAndCommit(t, v, 2, tx1)
	require.Equal(t, peer.TxValidationCode_VALID, tx1.validationCode)

	commitAt(t, v, 3, map[string]*storedSchema{"A": nonNeg("v2")}, nil)

	stale := deltaTx("stale", 0, tdDeltaVer("A", kvrwset.DeltaDescriptor_ADD, 1, tdType, "v1"))
	fresh := deltaTx("fresh", 1, tdDeltaVer("A", kvrwset.DeltaDescriptor_ADD, 2, tdType, "v2"))
	validateAndCommit(t, v, 4, stale, fresh)

	require.Equal(t, peer.TxValidationCode_INVALID_OTHER_REASON, stale.validationCode, "old schema_version must be rejected after upgrade")
	require.Equal(t, peer.TxValidationCode_VALID, fresh.validationCode)
	got, _ := dbInt(t, v, "A")
	require.Equal(t, int64(10), got, "8 + only fresh(v2) ADD 2")
}

func TestChaincodeLocalSchemaSeedEnablesDelta(t *testing.T) {
	v := newTypedDeltaValidator(t, true)
	schemaJS, err := json.Marshal(nonNeg(tdVer))
	require.NoError(t, err)

	seed := deltaTx("seed", 0, &kvrwset.KVWrite{Key: schemaStorageKey("A"), Value: schemaJS})
	validateAndCommit(t, v, 2, seed)
	require.Equal(t, peer.TxValidationCode_VALID, seed.validationCode,
		"seeding the schema is an ordinary plain write to the chaincode namespace")

	d := deltaTx("d", 0, tdDelta("A", kvrwset.DeltaDescriptor_ADD, 7))
	validateAndCommit(t, v, 3, d)
	require.Equal(t, peer.TxValidationCode_VALID, d.validationCode)
	got, ok := dbInt(t, v, "A")
	require.True(t, ok)
	require.Equal(t, int64(7), got)
}

func TestSchemaStorageKeyDeterministicBase64URL(t *testing.T) {
	sk := schemaStorageKey("acct:A")
	require.Equal(t, sk, schemaStorageKey("acct:A"), "deterministic")
	require.True(t, strings.HasPrefix(sk, typedDeltaSchemaKeyPrefix), "reserved printable prefix")
	require.NotContains(t, sk, "\x00", "no NUL (avoids composite-key namespace collision)")
	require.False(t, strings.HasPrefix(sk, "_"), "must NOT begin with _ (CouchDB rejects such doc IDs; #4b-9e-2)")

	enc := strings.TrimPrefix(sk, typedDeltaSchemaKeyPrefix)
	dec, err := base64.RawURLEncoding.DecodeString(enc)
	require.NoError(t, err, "encoded segment is valid base64url")
	require.Equal(t, "acct:A", string(dec), "reversible to the original data key")

	require.NotEqual(t, schemaStorageKey("A"), schemaStorageKey("B"), "distinct keys, distinct schema keys")
}
