/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package typeddelta

import (
	"testing"

	"github.com/golang/protobuf/proto"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/rwsetutil"
	"github.com/stretchr/testify/require"
)

func TestBuildDescriptorCanonical(t *testing.T) {
	d1, err := BuildDescriptor("balance_int64", OpSub, 5, []byte("v1"))
	require.NoError(t, err)
	d2, err := BuildDescriptor("balance_int64", OpSub, 5, []byte("v1"))
	require.NoError(t, err)

	b1, err := proto.Marshal(d1)
	require.NoError(t, err)
	b2, err := proto.Marshal(d2)
	require.NoError(t, err)
	require.Equal(t, b1, b2, "same inputs => byte-identical descriptor")

	require.Len(t, d1.Arg, Int64Width, "arg encoded as canonical 8-byte int64")
	require.Equal(t, "balance_int64", d1.TypeId)
	require.Equal(t, []byte("v1"), d1.SchemaVersion)

	if _, err := BuildDescriptor("t", OpAdd, -1, nil); err != ErrNegativeArg {
		t.Fatalf("want ErrNegativeArg, got %v", err)
	}
	if _, err := BuildDescriptor("t", OpUnspecified, 1, nil); err != ErrUnknownOp {
		t.Fatalf("want ErrUnknownOp, got %v", err)
	}
}

func TestMultiEndorserRwsetDeterminism(t *testing.T) {
	endorse := func() []byte {
		stub := NewStub("balance_int64", []byte("v1"), OpAdd, OpSub)
		require.NoError(t, stub.PutDelta("A", OpSub, 10))
		require.NoError(t, stub.PutDelta("B", OpAdd, 10))

		b := rwsetutil.NewRWSetBuilder()
		for _, w := range stub.Writes() {
			b.AddToDeltaWriteSet("mycc", w.Key, w.Delta)
		}
		res, err := b.GetTxSimulationResults()
		require.NoError(t, err)
		raw, err := proto.Marshal(res.PubSimulationResults)
		require.NoError(t, err)
		return raw
	}

	e1 := endorse()
	e2 := endorse()
	require.NotEmpty(t, e1)
	require.Equal(t, e1, e2, "identical proposal inputs must yield byte-identical rwset across endorsers")
}

func TestStubStrictM1(t *testing.T) {
	s := NewStub("t", []byte("v1"), OpAdd, OpSub)
	s.GetState("x")
	require.ErrorIs(t, s.PutDelta("A", OpSub, 1), ErrReadBeforeDelta)

	s = NewStub("t", []byte("v1"), OpAdd, OpSub)
	s.RangeQuery()
	require.ErrorIs(t, s.PutDelta("A", OpSub, 1), ErrReadBeforeDelta)

	s = NewStub("t", []byte("v1"), OpAdd, OpSub)
	s.RichQuery()
	require.ErrorIs(t, s.PutDelta("A", OpSub, 1), ErrReadBeforeDelta)

	s = NewStub("t", []byte("v1"), OpAdd, OpSub)
	require.NoError(t, s.PutState("A"))
	require.ErrorIs(t, s.PutDelta("A", OpSub, 1), ErrPlainAndDelta)

	s = NewStub("t", []byte("v1"), OpAdd, OpSub)
	require.NoError(t, s.PutDelta("A", OpSub, 1))
	require.ErrorIs(t, s.PutState("A"), ErrPlainAndDelta)

	s = NewStub("t", []byte("v1"), OpAdd)
	require.ErrorIs(t, s.PutDelta("A", OpSub, 1), ErrOpNotAllowed)

	s = NewStub("t", []byte("v1"), OpAdd, OpSub)
	require.ErrorIs(t, s.PutDelta("A", OpSub, -1), ErrNegativeArg)

	s = NewStub("t", []byte("v1"), OpAdd, OpSub)
	require.NoError(t, s.PutDelta("A", OpSub, 10))
	require.NoError(t, s.PutDelta("B", OpAdd, 10))
	require.NoError(t, s.PutState("C"))
	require.Len(t, s.Writes(), 2)
}
