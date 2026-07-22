/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package main

import (
	"math"
	"testing"
)

// A3: addChecked must report signed int64 overflow instead of silently wrapping,
// because a wrap would corrupt the prefix-violation count this instrument produces.
func TestAddChecked(t *testing.T) {
	cases := []struct {
		name string
		a, b int64
		want int64
		ok   bool
	}{
		{"simple", 5, 3, 8, true},
		{"to-max", 0, math.MaxInt64, math.MaxInt64, true},
		{"pos-overflow", math.MaxInt64, 1, math.MaxInt64, false},     // unchanged a, not wrapped
		{"pos-overflow-2", math.MaxInt64 - 2, 5, math.MaxInt64 - 2, false},
		{"neg-overflow", math.MinInt64, -1, math.MinInt64, false},    // unchanged a, not wrapped
		{"neg-overflow-2", math.MinInt64 + 2, -5, math.MinInt64 + 2, false},
		{"mixed-sign", 10, -3, 7, true},
		{"to-zero", -5, 5, 0, true},
		{"min-plus-max", math.MinInt64, math.MaxInt64, -1, true}, // no overflow
	}
	for _, c := range cases {
		got, ok := addChecked(c.a, c.b)
		if ok != c.ok || got != c.want {
			t.Fatalf("%s: addChecked(%d,%d)=(%d,%v) want (%d,%v)", c.name, c.a, c.b, got, ok, c.want, c.ok)
		}
	}
}
