/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package typeddelta

import (
	"errors"

	"github.com/hyperledger/fabric-protos-go/ledger/rwset/kvrwset"
)

var (
	ErrReadBeforeDelta = errors.New("typeddelta: PutDelta after a state read (arg must derive from proposal inputs only)")
	ErrPlainAndDelta   = errors.New("typeddelta: plain write and delta on the same key in one tx")
	ErrOpNotAllowed    = errors.New("typeddelta: op not allowed by schema")
)

func BuildDescriptor(typeID string, op Op, arg int64, schemaVersion []byte) (*kvrwset.DeltaDescriptor, error) {
	if arg < 0 {
		return nil, ErrNegativeArg
	}
	var pop kvrwset.DeltaDescriptor_Op
	switch op {
	case OpAdd:
		pop = kvrwset.DeltaDescriptor_ADD
	case OpSub:
		pop = kvrwset.DeltaDescriptor_SUB
	default:
		return nil, ErrUnknownOp
	}
	var sv []byte
	if len(schemaVersion) > 0 {
		sv = append([]byte(nil), schemaVersion...)
	}
	return &kvrwset.DeltaDescriptor{
		TypeId:        typeID,
		Op:            pop,
		Arg:           MarshalInt64(arg),
		SchemaVersion: sv,
	}, nil
}

type DeltaWrite struct {
	Key   string
	Delta *kvrwset.DeltaDescriptor
}

type Stub struct {
	typeID        string
	schemaVersion []byte
	allowedOps    map[Op]bool
	didRead       bool
	plainKeys     map[string]bool
	deltaKeys     map[string]bool
	writes        []DeltaWrite
}

func NewStub(typeID string, schemaVersion []byte, allowed ...Op) *Stub {
	m := map[Op]bool{}
	for _, o := range allowed {
		m[o] = true
	}
	return &Stub{
		typeID:        typeID,
		schemaVersion: schemaVersion,
		allowedOps:    m,
		plainKeys:     map[string]bool{},
		deltaKeys:     map[string]bool{},
	}
}

func (s *Stub) GetState(string) { s.didRead = true }

func (s *Stub) RangeQuery() { s.didRead = true }
func (s *Stub) RichQuery()  { s.didRead = true }

func (s *Stub) PutState(key string) error {
	if s.deltaKeys[key] {
		return ErrPlainAndDelta
	}
	s.plainKeys[key] = true
	return nil
}

func (s *Stub) PutDelta(key string, op Op, arg int64) error {
	if s.didRead {
		return ErrReadBeforeDelta
	}
	if s.plainKeys[key] {
		return ErrPlainAndDelta
	}
	if !s.allowedOps[op] {
		return ErrOpNotAllowed
	}
	d, err := BuildDescriptor(s.typeID, op, arg, s.schemaVersion)
	if err != nil {
		return err
	}
	s.writes = append(s.writes, DeltaWrite{Key: key, Delta: d})
	s.deltaKeys[key] = true
	return nil
}

func (s *Stub) Writes() []DeltaWrite { return s.writes }
