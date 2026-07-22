/*
prefixanalyzer — measurement tool (read-only; independent of the prototype core).

Reads committed Fabric block files (peer channel fetch *.block) and counts NONNEG
*prefix* invariant violation commits: it replays the VALID commits in block order
(and tx-index order within a block) under a baseline-specific write semantics, and
flags any committed tx that drives a tracked balance below 0 AT THAT PREFIX POINT.

This is deliberately prefix-based, not final-state-based: a block whose net effect
is legal can still contain a tx that overdrafts at an intermediate prefix. ours
rejects those at commit (they are INVALID, hence not replayed) => 0 violations by
construction; a baseline without commit-time invariant reject (FabricCRDT-style)
can commit them => >0.

Metrics emitted are COUNTS (throughput/latency come from the load driver):
  blocks, total/valid/invalid/config tx, invalid-by-code, delta/plain writes,
  prefix invariant violation commits (+ offending tx ids), final balances.

Trap guarded: validity is read from the block's TRANSACTIONS_FILTER (commit-time
validationCode), never from invoke/endorsement success.
*/
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hyperledger/fabric-protos-go/common"
	"github.com/hyperledger/fabric-protos-go/ledger/rwset/kvrwset"
	"github.com/hyperledger/fabric-protos-go/peer"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/rwsetutil"
	"github.com/hyperledger/fabric/core/ledger/kvledger/txmgmt/typeddelta"
	"github.com/hyperledger/fabric/internal/pkg/txflags"
	"github.com/hyperledger/fabric/protoutil"
)

func main() {
	dir := flag.String("blocks", "", "directory of *.block files (peer channel fetch — NOTE: orderer-sourced blocks lack validation codes)")
	blockfile := flag.String("blockfile", "", "peer blockfile (length-prefixed; has TRANSACTIONS_FILTER validation codes) — preferred source")
	ns := flag.String("ns", "typeddelta", "chaincode namespace holding the balances")
	baseline := flag.String("baseline", "ours", "ours|vanilla|fabriccrdt (write-semantics adapter)")
	schemaPrefix := flag.String("schemaPrefix", "typeddelta~schema~", "schema key prefix to ignore")
	keyFilter := flag.String("key", "", "if set, only track this single data key (ignore other keys in the namespace)")
	invalidTxids := flag.String("invalidTxids", "", "file of invalid tx ids (one per line); used when blocks lack TRANSACTIONS_FILTER, e.g. orderer-sourced blocks")
	verbose := flag.Bool("v", false, "print each violation/valid delta")
	flag.Parse()

	var invalidSet map[string]bool
	if *invalidTxids != "" {
		invalidSet = map[string]bool{}
		raw, rerr := os.ReadFile(*invalidTxids)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "read invalidTxids: %v\n", rerr)
			os.Exit(1)
		}
		for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
			if t := strings.TrimSpace(line); t != "" {
				invalidSet[t] = true
			}
		}
	}
	if *dir == "" && *blockfile == "" {
		fmt.Fprintln(os.Stderr, "usage: prefixanalyzer (-blockfile <peer blockfile> | -blocks <dir>) [-ns ..] [-baseline ours|vanilla]")
		os.Exit(2)
	}

	var blocks []*common.Block
	var err error
	if *blockfile != "" {
		blocks, err = loadBlockfile(*blockfile)
	} else {
		blocks, err = loadBlocks(*dir)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "load blocks: %v\n", err)
		os.Exit(1)
	}

	bal := map[string]int64{}
	var nBlocks, totalTx, validTx, invalidTx, configTx, deltaWrites, plainWrites, violationCommits, noValidation, overflowEvents int
	invalidByCode := map[string]int{}

	for _, blk := range blocks {
		nBlocks++
		flags := txflags.ValidationFlags(metadata(blk, int(common.BlockMetadataIndex_TRANSACTIONS_FILTER)))
		for i, envBytes := range blk.Data.Data {
			totalTx++
			env, err := protoutil.UnmarshalEnvelope(envBytes)
			if err != nil {
				continue
			}
			chHdr, err := channelHeader(env)
			if err != nil || chHdr.Type != int32(common.HeaderType_ENDORSER_TRANSACTION) {
				configTx++
				continue
			}
			// Commit validity: prefer the block's TRANSACTIONS_FILTER (peer blocks);
			// fall back to an external invalid-txid set when the filter is absent
			// (orderer-sourced blocks carry no validation codes).
			valid := true
			switch {
			case i < len(flags):
				code := flags.Flag(i)
				valid = code == peer.TxValidationCode_VALID
				if !valid {
					invalidByCode[code.String()]++
				}
			case invalidSet != nil:
				valid = !invalidSet[chHdr.TxId]
			default:
				noValidation++
				continue
			}
			if !valid {
				invalidTx++
				continue
			}
			validTx++

			ccAction, err := protoutil.GetActionFromEnvelopeMsg(env)
			if err != nil {
				continue
			}
			txrw := &rwsetutil.TxRwSet{}
			if err := txrw.FromProtoBytes(ccAction.Results); err != nil {
				continue
			}

			touched := map[string]bool{}
			for _, nsrw := range txrw.NsRwSets {
				if nsrw.NameSpace != *ns || nsrw.KvRwSet == nil {
					continue
				}
				for _, w := range nsrw.KvRwSet.Writes {
					if *baseline == "highthroughput" {
						// append/lazy-aggregate model: physical key "<acct>~<txid>",
						// value = SIGNED 8-byte BE delta. Map to the logical account
						// (prefix before '~') and accumulate.
						logical := w.Key
						if idx := strings.IndexByte(w.Key, '~'); idx >= 0 {
							logical = w.Key[:idx]
						}
						if *keyFilter != "" && logical != *keyFilter {
							continue
						}
						if len(w.Value) != typeddelta.Int64Width {
							continue
						}
						deltaWrites++
						if sum, ok := addChecked(bal[logical], int64(binary.BigEndian.Uint64(w.Value))); ok {
							bal[logical] = sum
							touched[logical] = true
						} else {
							overflowEvents++
							fmt.Fprintf(os.Stderr, "WARNING: int64 overflow accumulating key=%s blk=%d tx=%d (value skipped)\n", logical, blk.Header.Number, i)
						}
						continue
					}
					if strings.HasPrefix(w.Key, *schemaPrefix) {
						continue // schema record, not a balance
					}
					if *keyFilter != "" && w.Key != *keyFilter {
						continue // only track the requested key
					}
					switch {
					case w.Delta != nil:
						deltaWrites++
						delta, derr := applyDelta(*baseline, w.Delta)
						if derr != nil {
							continue
						}
						if sum, ok := addChecked(bal[w.Key], delta); ok {
							bal[w.Key] = sum
							touched[w.Key] = true
						} else {
							overflowEvents++
							fmt.Fprintf(os.Stderr, "WARNING: int64 overflow accumulating key=%s blk=%d tx=%d (delta skipped)\n", w.Key, blk.Header.Number, i)
						}
					case len(w.Value) == typeddelta.Int64Width:
						plainWrites++
						v, verr := typeddelta.UnmarshalInt64(w.Value)
						if verr != nil {
							continue
						}
						bal[w.Key] = v // absolute write (vanilla RMW result / plain seed)
						touched[w.Key] = true
					}
				}
			}

			violated := false
			for k := range touched {
				if bal[k] < 0 {
					violated = true
					if *verbose {
						fmt.Printf("  PREFIX VIOLATION blk=%d tx=%d key=%s -> %d\n", blk.Header.Number, i, k, bal[k])
					}
				}
			}
			if violated {
				violationCommits++
			}
		}
	}

	fmt.Printf("baseline=%s ns=%s blocks=%d\n", *baseline, *ns, nBlocks)
	fmt.Printf("tx: total=%d valid=%d invalid=%d config/system=%d no-validation-info=%d\n", totalTx, validTx, invalidTx, configTx, noValidation)
	if noValidation > 0 {
		fmt.Println("WARNING: blocks lack validation codes (orderer-sourced?). Use -blockfile from a peer for accurate validity.")
	}
	fmt.Printf("writes: delta=%d plain=%d\n", deltaWrites, plainWrites)
	if len(invalidByCode) > 0 {
		fmt.Printf("invalid-by-code: %v\n", invalidByCode)
	}
	fmt.Printf("PREFIX INVARIANT VIOLATION COMMITS (NONNEG) = %d\n", violationCommits)
	fmt.Printf("safe valid commits (valid - violations) = %d\n", validTx-violationCommits)
	if overflowEvents > 0 {
		fmt.Printf("WARNING: int64 overflow events = %d (those writes were skipped; balances/violation counts may be affected)\n", overflowEvents)
	}
	printBalances(bal, *schemaPrefix)
}

// addChecked adds b to a, reporting signed int64 overflow rather than silently
// wrapping. A wrap would corrupt the prefix-violation count, so this measurement
// instrument flags it and leaves the accumulator unchanged. Experiment balances are
// far from the int64 bound; the guard protects against malformed/adversarial input.
func addChecked(a, b int64) (int64, bool) {
	s := a + b
	if (b > 0 && s < a) || (b < 0 && s > a) {
		return a, false
	}
	return s, true
}

// applyDelta returns the signed change for a delta descriptor under a baseline's
// write semantics. ours and fabriccrdt both interpret a signed ADD/SUB delta the
// same way at replay time (the difference is whether the *peer* let an overdraft
// commit; that is reflected in which txs are VALID, which we read from the block).
func applyDelta(baseline string, d *kvrwset.DeltaDescriptor) (int64, error) {
	arg, err := typeddelta.UnmarshalInt64(d.Arg)
	if err != nil {
		return 0, err
	}
	switch d.Op {
	case kvrwset.DeltaDescriptor_ADD:
		return arg, nil
	case kvrwset.DeltaDescriptor_SUB:
		return -arg, nil
	default:
		return 0, fmt.Errorf("unsupported op %v", d.Op)
	}
}

func loadBlocks(dir string) ([]*common.Block, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.block"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no *.block files in %s", dir)
	}
	var blocks []*common.Block
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		blk, err := protoutil.UnmarshalBlock(b)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		blocks = append(blocks, blk)
	}
	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].Header.Number < blocks[j].Header.Number
	})
	return blocks, nil
}

// loadBlockfile parses a peer blkstorage blockfile: a concatenation of
// [varint length][marshaled common.Block]. These blocks carry the peer's
// TRANSACTIONS_FILTER (commit-time validation codes) — unlike orderer blocks.
func loadBlockfile(path string) ([]*common.Block, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var blocks []*common.Block
	for len(data) > 0 {
		n, consumed := binary.Uvarint(data)
		if consumed <= 0 {
			return nil, fmt.Errorf("bad varint length prefix at offset (remaining %d bytes)", len(data))
		}
		data = data[consumed:]
		if uint64(len(data)) < n {
			return nil, fmt.Errorf("truncated block: need %d bytes, have %d", n, len(data))
		}
		blk, err := protoutil.UnmarshalBlock(data[:n])
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, blk)
		data = data[n:]
	}
	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].Header.Number < blocks[j].Header.Number
	})
	return blocks, nil
}

func metadata(blk *common.Block, idx int) []byte {
	if blk.Metadata == nil || len(blk.Metadata.Metadata) <= idx {
		return nil
	}
	return blk.Metadata.Metadata[idx]
}

func channelHeader(env *common.Envelope) (*common.ChannelHeader, error) {
	payload, err := protoutil.UnmarshalPayload(env.Payload)
	if err != nil {
		return nil, err
	}
	if payload.Header == nil {
		return nil, fmt.Errorf("nil payload header")
	}
	return protoutil.UnmarshalChannelHeader(payload.Header.ChannelHeader)
}

func printBalances(bal map[string]int64, schemaPrefix string) {
	keys := make([]string, 0, len(bal))
	for k := range bal {
		if strings.HasPrefix(k, schemaPrefix) {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Print("final balances:")
	for _, k := range keys {
		fmt.Printf(" %s=%d", k, bal[k])
	}
	fmt.Println()
}
