/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package validation

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/hyperledger/fabric-protos-go/ledger/rwset/kvrwset"
	"github.com/hyperledger/fabric-protos-go/peer"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/privacyenabledstate"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/rwsetutil"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/typeddelta"
)

const typedDeltaSchemaKeyPrefix = "typeddelta~schema~"

func schemaStorageKey(dataKey string) string {
	return typedDeltaSchemaKeyPrefix + base64.RawURLEncoding.EncodeToString([]byte(dataKey))
}

func shouldSkipTypedDeltaSchemaLookup(ns, key string) bool {
	return ns == ""
}

type storedSchema struct {
	TypeID     string  `json:"type_id"`
	AllowedOps []int32 `json:"allowed_ops"`
	Invariant  string  `json:"invariant"`
	Lo         int64   `json:"lo,omitempty"`
	Hi         int64   `json:"hi,omitempty"`
	Version    string  `json:"version"`
}

func (s *storedSchema) invariant() (typeddelta.Invariant, error) {
	switch s.Invariant {
	case "NONNEG":
		return typeddelta.NonNeg{}, nil
	case "RANGE":
		if s.Lo > s.Hi {
			return nil, fmt.Errorf("typeddelta: invalid RANGE schema: lo (%d) > hi (%d)", s.Lo, s.Hi)
		}
		return typeddelta.Range{Lo: s.Lo, Hi: s.Hi}, nil
	default:
		return nil, fmt.Errorf("typeddelta: unknown invariant %q", s.Invariant)
	}
}

func (s *storedSchema) allowsOp(op typeddelta.Op) bool {
	for _, a := range s.AllowedOps {
		if typeddelta.Op(a) == op {
			return true
		}
	}
	return false
}

type schemaLookup func(ns, key string) (*storedSchema, bool, error)

func (v *validator) stateSchemaLookup(updates *publicAndHashUpdates) schemaLookup {
	return func(ns, key string) (*storedSchema, bool, error) {
		if shouldSkipTypedDeltaSchemaLookup(ns, key) {
			return nil, false, nil
		}
		sk := schemaStorageKey(key)
		vv := updates.publicUpdates.Get(ns, sk)
		if vv == nil {
			var err error
			vv, err = v.db.GetState(ns, sk)
			if err != nil {
				return nil, false, err
			}
		}
		if vv == nil || len(vv.Value) == 0 {
			return nil, false, nil
		}
		s := &storedSchema{}
		if err := json.Unmarshal(vv.Value, s); err != nil {
			return nil, false, err
		}
		return s, true, nil
	}
}

func toDelta(d *kvrwset.DeltaDescriptor) (typeddelta.Delta, error) {
	var op typeddelta.Op
	switch d.Op {
	case kvrwset.DeltaDescriptor_ADD:
		op = typeddelta.OpAdd
	case kvrwset.DeltaDescriptor_SUB:
		op = typeddelta.OpSub
	default:
		return typeddelta.Delta{}, fmt.Errorf("typeddelta: unsupported op %v", d.Op)
	}
	arg, err := typeddelta.UnmarshalInt64(d.Arg)
	if err != nil {
		return typeddelta.Delta{}, err
	}
	return typeddelta.Delta{Op: op, Arg: arg}, nil
}

func txHasDeltaWrites(txRWSet *rwsetutil.TxRwSet) bool {
	for _, nsRW := range txRWSet.NsRwSets {
		for _, w := range nsRW.KvRwSet.Writes {
			if w.Delta != nil {
				return true
			}
		}
	}
	return false
}

func txTouchesTypedState(txRWSet *rwsetutil.TxRwSet, lookup schemaLookup) (bool, error) {
	for _, nsRW := range txRWSet.NsRwSets {
		for _, w := range nsRW.KvRwSet.Writes {
			if w.Delta != nil {
				return true, nil
			}
			_, ok, err := lookup(nsRW.NameSpace, w.Key)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
	}
	return false, nil
}

func malformedTypedWrites(txRWSet *rwsetutil.TxRwSet) bool {
	for _, nsRW := range txRWSet.NsRwSets {
		hasDelta := map[string]bool{}
		hasNonDelta := map[string]bool{}
		for _, w := range nsRW.KvRwSet.Writes {
			if w.Delta != nil {
				if len(w.Value) != 0 || w.IsDelete {
					return true
				}
				hasDelta[w.Key] = true
			} else {
				hasNonDelta[w.Key] = true
			}
		}
		for k := range hasDelta {
			if hasNonDelta[k] {
				return true
			}
		}
	}
	return false
}

func plainTypedWritesValid(txRWSet *rwsetutil.TxRwSet, lookup schemaLookup) (bool, error) {
	for _, nsRW := range txRWSet.NsRwSets {
		for _, w := range nsRW.KvRwSet.Writes {
			if w.Delta != nil {
				continue
			}
			sc, ok, err := lookup(nsRW.NameSpace, w.Key)
			if err != nil {
				return false, err
			}
			if !ok {
				continue
			}
			inv, ierr := sc.invariant()
			if ierr != nil {
				return false, nil
			}
			if w.IsDelete {
				if !inv.Holds(0) {
					return false, nil
				}
				continue
			}
			val, err := typeddelta.UnmarshalInt64(w.Value)
			if err != nil {
				return false, nil
			}
			if !inv.Holds(val) {
				return false, nil
			}
		}
	}
	return true, nil
}

func foldPublicDeltas(txRWSet *rwsetutil.TxRwSet) (map[compositeKey][]typeddelta.Delta, error) {
	out := map[compositeKey][]typeddelta.Delta{}
	for _, nsRW := range txRWSet.NsRwSets {
		for _, w := range nsRW.KvRwSet.Writes {
			if w.Delta == nil {
				continue
			}
			d, err := toDelta(w.Delta)
			if err != nil {
				return nil, err
			}
			k := compositeKey{nsRW.NameSpace, "", w.Key}
			out[k] = append(out[k], d)
		}
	}
	return out, nil
}

func currentInt64(ns, key string, updates *publicAndHashUpdates, db *privacyenabledstate.DB) (int64, error) {
	vv := updates.publicUpdates.Get(ns, key)
	if vv == nil {
		var err error
		vv, err = db.GetState(ns, key)
		if err != nil {
			return 0, err
		}
	}
	if vv == nil || len(vv.Value) == 0 {
		return 0, nil
	}
	return typeddelta.UnmarshalInt64(vv.Value)
}

func (v *validator) validateTypedDeltaTx(txRWSet *rwsetutil.TxRwSet, updates *publicAndHashUpdates, lookup schemaLookup) (peer.TxValidationCode, error) {
	if malformedTypedWrites(txRWSet) {
		return peer.TxValidationCode_INVALID_OTHER_REASON, nil
	}
	if !v.typedDeltaEnabled {
		return peer.TxValidationCode_INVALID_OTHER_REASON, nil
	}

	folded := map[compositeKey][]typeddelta.Delta{}
	schemas := map[compositeKey]*storedSchema{}
	for _, nsRW := range txRWSet.NsRwSets {
		for _, w := range nsRW.KvRwSet.Writes {
			if w.Delta == nil {
				continue
			}
			sc, ok, err := lookup(nsRW.NameSpace, w.Key)
			if err != nil {
				return peer.TxValidationCode(-1), err
			}
			if !ok {
				return peer.TxValidationCode_INVALID_OTHER_REASON, nil
			}
			if w.Delta.TypeId != sc.TypeID {
				return peer.TxValidationCode_INVALID_OTHER_REASON, nil
			}
			if !bytes.Equal(w.Delta.SchemaVersion, []byte(sc.Version)) {
				return peer.TxValidationCode_INVALID_OTHER_REASON, nil
			}
			d, err := toDelta(w.Delta)
			if err != nil {
				return peer.TxValidationCode_INVALID_OTHER_REASON, nil
			}
			if !sc.allowsOp(d.Op) {
				return peer.TxValidationCode_INVALID_OTHER_REASON, nil
			}
			k := compositeKey{nsRW.NameSpace, "", w.Key}
			folded[k] = append(folded[k], d)
			schemas[k] = sc
		}
	}

	for k, deltas := range folded {
		curr, err := currentInt64(k.ns, k.key, updates, v.db)
		if err != nil {
			return peer.TxValidationCode(-1), err
		}
		next, err := typeddelta.Apply(curr, deltas)
		if err != nil {
			return peer.TxValidationCode_INVALID_OTHER_REASON, nil
		}
		inv, ierr := schemas[k].invariant()
		if ierr != nil {
			return peer.TxValidationCode_INVALID_OTHER_REASON, nil
		}
		if !inv.Holds(next) {
			return peer.TxValidationCode_INVALID_OTHER_REASON, nil
		}
	}

	ok, err := plainTypedWritesValid(txRWSet, lookup)
	if err != nil {
		return peer.TxValidationCode(-1), err
	}
	if !ok {
		return peer.TxValidationCode_INVALID_OTHER_REASON, nil
	}
	return peer.TxValidationCode_VALID, nil
}

func (txops txOps) applyTypedDeltas(rwset *rwsetutil.TxRwSet, precedingUpdates *publicAndHashUpdates, db *privacyenabledstate.DB) error {
	folded, err := foldPublicDeltas(rwset)
	if err != nil {
		return err
	}
	for k, deltas := range folded {
		curr, err := currentInt64(k.ns, k.key, precedingUpdates, db)
		if err != nil {
			return err
		}
		next, err := typeddelta.Apply(curr, deltas)
		if err != nil {
			return err
		}
		txops.upsert(k, typeddelta.MarshalInt64(next))
	}
	return nil
}
