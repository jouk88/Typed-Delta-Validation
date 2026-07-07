// Copyright the Hyperledger Fabric contributors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package shim

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

func be(n int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(n))
	return b
}

func TestBuildShimDeltaCanonical(t *testing.T) {
	d, err := buildShimDelta("balance_int64", DeltaSub, be(5), []byte("v1"))
	require.NoError(t, err)
	require.Equal(t, be(5), d.Arg)
	require.Equal(t, []byte("v1"), d.SchemaVersion)

	_, err = buildShimDelta("t", DeltaAdd, []byte{1, 2, 3}, []byte("v1"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "8-byte")

	_, err = buildShimDelta("t", DeltaSub, be(-1), []byte("v1"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "non-negative")

	_, err = buildShimDelta("t", DeltaOp(99), be(1), []byte("v1"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported")

	arg, sv := be(7), []byte("v9")
	d, err = buildShimDelta("t", DeltaAdd, arg, sv)
	require.NoError(t, err)
	arg[0], sv[0] = 0xFF, 'Z'
	require.Equal(t, be(7), d.Arg, "arg must be cloned")
	require.Equal(t, []byte("v9"), d.SchemaVersion, "schemaVersion must be cloned")
}

func TestInvokeChaincodePoisonsDelta(t *testing.T) {
	stub := &ChaincodeStub{TxID: "tx1", ChannelID: "ch1"}

	func() {
		defer func() { _ = recover() }()
		stub.InvokeChaincode("othercc", [][]byte{[]byte("Get"), []byte("a")}, "")
	}()
	require.True(t, stub.didRead, "InvokeChaincode must poison the delta fast path (didRead=true)")

	err := stub.PutDelta("k", "balance_int64", DeltaSub, make([]byte, 8), []byte("v1"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "after a state read")
}

func TestPutDeltaPassesWithoutRead(t *testing.T) {
	stub := &ChaincodeStub{TxID: "tx1", ChannelID: "ch1"}
	require.False(t, stub.didRead)

	var returnedErr error
	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
		}()
		returnedErr = stub.PutDelta("k", "balance_int64", DeltaSub, make([]byte, 8), []byte("v1"))
	}()

	if returnedErr != nil {
		require.NotContains(t, returnedErr.Error(), "after a state read")
		require.NotContains(t, returnedErr.Error(), "same key")
	}
	require.True(t, panicked, "without a prior read, PutDelta should pass the gate and reach the (nil) handler")
}

func TestGetStatePoisonsDelta(t *testing.T) {
	stub := &ChaincodeStub{TxID: "tx1", ChannelID: "ch1"}
	func() {
		defer func() { _ = recover() }()
		_, _ = stub.GetState("a")
	}()
	require.True(t, stub.didRead)
	err := stub.PutDelta("k", "balance_int64", DeltaSub, make([]byte, 8), []byte("v1"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "after a state read")
}
