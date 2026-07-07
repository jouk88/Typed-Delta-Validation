/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package typeddelta

import (
	"encoding/binary"
	"errors"
	"fmt"
)

type Op uint8

const (
	OpUnspecified Op = 0
	OpAdd         Op = 1
	OpSub         Op = 2
)

func (o Op) String() string {
	switch o {
	case OpAdd:
		return "ADD"
	case OpSub:
		return "SUB"
	default:
		return "UNSPECIFIED"
	}
}

type Delta struct {
	Op  Op
	Arg int64
}

var (
	ErrNegativeArg   = errors.New("typeddelta: delta arg must be non-negative")
	ErrUnknownOp     = errors.New("typeddelta: unknown op")
	ErrOverflow      = errors.New("typeddelta: int64 overflow")
	ErrBadCanonical  = errors.New("typeddelta: malformed canonical int64 encoding")
	ErrEmptyDeltaSet = errors.New("typeddelta: empty delta set")
)

func (d Delta) signedAmount() (int64, error) {
	if d.Arg < 0 {
		return 0, ErrNegativeArg
	}
	switch d.Op {
	case OpAdd:
		return d.Arg, nil
	case OpSub:
		return -d.Arg, nil
	default:
		return 0, ErrUnknownOp
	}
}

func FoldNet(deltas []Delta) (int64, error) {
	if len(deltas) == 0 {
		return 0, ErrEmptyDeltaSet
	}
	var net int64
	for _, d := range deltas {
		s, err := d.signedAmount()
		if err != nil {
			return 0, err
		}
		var ok bool
		net, ok = addChecked(net, s)
		if !ok {
			return 0, ErrOverflow
		}
	}
	return net, nil
}

func ApplyNet(cur, net int64) (int64, error) {
	res, ok := addChecked(cur, net)
	if !ok {
		return 0, ErrOverflow
	}
	return res, nil
}

func Apply(cur int64, deltas []Delta) (int64, error) {
	net, err := FoldNet(deltas)
	if err != nil {
		return 0, err
	}
	return ApplyNet(cur, net)
}

func addChecked(a, b int64) (int64, bool) {
	s := a + b
	if (b > 0 && s < a) || (b < 0 && s > a) {
		return 0, false
	}
	return s, true
}

type Invariant interface {
	Holds(v int64) bool
	String() string
}

type NonNeg struct{}

func (NonNeg) Holds(v int64) bool { return v >= 0 }
func (NonNeg) String() string     { return "NONNEG" }

type Range struct {
	Lo, Hi int64
}

func (r Range) Holds(v int64) bool { return v >= r.Lo && v <= r.Hi }
func (r Range) String() string     { return fmt.Sprintf("RANGE(%d,%d)", r.Lo, r.Hi) }

const Int64Width = 8

func MarshalInt64(v int64) []byte {
	b := make([]byte, Int64Width)
	binary.BigEndian.PutUint64(b, uint64(v))
	return b
}

func UnmarshalInt64(b []byte) (int64, error) {
	if len(b) != Int64Width {
		return 0, ErrBadCanonical
	}
	return int64(binary.BigEndian.Uint64(b)), nil
}
