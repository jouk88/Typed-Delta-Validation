/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package typeddelta

import (
	"math"
	"testing"
)

func TestFoldNetCommutative(t *testing.T) {
	a := []Delta{{OpAdd, 10}, {OpSub, 3}, {OpAdd, 5}}
	b := []Delta{{OpSub, 3}, {OpAdd, 5}, {OpAdd, 10}}
	na, err := FoldNet(a)
	if err != nil {
		t.Fatalf("fold a: %v", err)
	}
	nb, err := FoldNet(b)
	if err != nil {
		t.Fatalf("fold b: %v", err)
	}
	if na != nb || na != 12 {
		t.Fatalf("non-commutative fold: na=%d nb=%d want 12", na, nb)
	}
}

func TestApplyAndInvariant(t *testing.T) {
	got, err := Apply(5, []Delta{{OpSub, 10}, {OpAdd, 8}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got != 3 {
		t.Fatalf("apply got %d want 3", got)
	}
	if !(NonNeg{}).Holds(got) {
		t.Fatalf("NONNEG should hold for %d", got)
	}

	got2, err := Apply(5, []Delta{{OpSub, 6}})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got2 != -1 {
		t.Fatalf("apply got %d want -1", got2)
	}
	if (NonNeg{}).Holds(got2) {
		t.Fatalf("NONNEG must fail for %d", got2)
	}
}

func TestRangeInvariant(t *testing.T) {
	r := Range{Lo: 0, Hi: 10}
	cases := []struct {
		v    int64
		want bool
	}{{-1, false}, {0, true}, {7, true}, {10, true}, {11, false}}
	for _, c := range cases {
		if r.Holds(c.v) != c.want {
			t.Fatalf("Range.Holds(%d)=%v want %v", c.v, r.Holds(c.v), c.want)
		}
	}
}

func TestNegativeArgRejected(t *testing.T) {
	if _, err := FoldNet([]Delta{{OpAdd, -1}}); err != ErrNegativeArg {
		t.Fatalf("want ErrNegativeArg, got %v", err)
	}
}

func TestUnknownOpRejected(t *testing.T) {
	if _, err := FoldNet([]Delta{{OpUnspecified, 1}}); err != ErrUnknownOp {
		t.Fatalf("want ErrUnknownOp, got %v", err)
	}
}

func TestOverflowDetected(t *testing.T) {
	if _, err := ApplyNet(math.MaxInt64, 1); err != ErrOverflow {
		t.Fatalf("add overflow not detected: %v", err)
	}
	if _, err := ApplyNet(math.MinInt64, -1); err != ErrOverflow {
		t.Fatalf("sub overflow not detected: %v", err)
	}
	if _, err := Apply(-2, []Delta{{OpSub, math.MaxInt64}}); err != ErrOverflow {
		t.Fatalf("fold/apply overflow not detected: %v", err)
	}
}

func TestCanonicalRoundTrip(t *testing.T) {
	for _, v := range []int64{0, 1, -1, math.MaxInt64, math.MinInt64, 1234567890} {
		b := MarshalInt64(v)
		if len(b) != Int64Width {
			t.Fatalf("width=%d want %d", len(b), Int64Width)
		}
		got, err := UnmarshalInt64(b)
		if err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got != v {
			t.Fatalf("round-trip got %d want %d", got, v)
		}
	}
	if _, err := UnmarshalInt64([]byte{0x01}); err != ErrBadCanonical {
		t.Fatalf("want ErrBadCanonical, got %v", err)
	}
}

func TestCanonicalDeterministic(t *testing.T) {
	got := MarshalInt64(258)
	want := []byte{0, 0, 0, 0, 0, 0, 0x01, 0x02}
	if len(got) != len(want) {
		t.Fatalf("len %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte[%d]=%#x want %#x", i, got[i], want[i])
		}
	}
}
