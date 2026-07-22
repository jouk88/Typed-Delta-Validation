# Artifact manifest

## Base

- Upstream: https://github.com/hyperledger/fabric
- Tag: v2.5.16
- Commit: f871cf92a026aba7b12e6f06d71ded3e6e659d71

## Repository layout

| Directory | Location in the Fabric source tree |
|---|---|
| `fabric-core/typeddelta.go`, `fabric-core/endorse.go` (+ tests) | `core/ledger/kvledger/txmgmt/typeddelta/` (new package: delta descriptor algebra, canonical encoding, endorsement-time checks) |
| `fabric-core/typeddelta_hooks.go` (+ bench/integration tests) | `core/ledger/kvledger/txmgmt/validation/` (commit-time block-order fold and ordered-prefix invariant check) |
| `fabric-core/kv_ledger.go`, `fabric-core/typeddelta_capability_test.go` | `core/ledger/kvledger/` (ledger wiring, capability read at ledger open) |
| `shim/` | `vendor/github.com/hyperledger/fabric-chaincode-go/shim/` (`PutDelta` API and the no-read-before-delta contract) |
| `history/` | `core/ledger/kvledger/history/` (history query executer folding typed-delta writes) |
| `chaincode/` | standalone chaincodes: typed delta, vanilla read-modify-write, append/high-throughput |
| `data/` | raw result data for experiments E1-E7, benchstat outputs, aggregated tables |

## Files modified relative to the base commit

```
common/capabilities/application.go
common/channelconfig/util.go
core/chaincode/handler.go
core/chaincode/handler_test.go
core/chaincode/mock/tx_simulator.go
core/endorser/fake/tx_simulator.go
core/ledger/kvledger/history/db_test.go
core/ledger/kvledger/history/query_executer.go
core/ledger/kvledger/kv_ledger.go
core/ledger/kvledger/kv_ledger_provider.go
core/ledger/kvledger/snapshot.go
core/ledger/kvledger/txmgmt/rwsetutil/rwset_builder.go
core/ledger/kvledger/txmgmt/txmgr/lockbased_txmgr.go
core/ledger/kvledger/txmgmt/txmgr/tx_simulator.go
core/ledger/kvledger/txmgmt/validation/batch_preparer.go
core/ledger/kvledger/txmgmt/validation/batch_preparer_test.go
core/ledger/kvledger/txmgmt/validation/mock/txsim.go
core/ledger/kvledger/txmgmt/validation/tx_ops.go
core/ledger/kvledger/txmgmt/validation/validator.go
core/ledger/ledger_interface.go
core/ledger/mock/tx_simulator.go
vendor/github.com/hyperledger/fabric-chaincode-go/shim/handler.go
vendor/github.com/hyperledger/fabric-chaincode-go/shim/stub.go
vendor/github.com/hyperledger/fabric-protos-go/ledger/rwset/kvrwset/kv_rwset.pb.go
vendor/github.com/hyperledger/fabric-protos-go/peer/chaincode_shim.pb.go
```

## Files added relative to the base commit

```
common/capabilities/typeddelta_test.go
common/channelconfig/typeddelta_extract_test.go
core/ledger/kvledger/history/query_executer_delta_test.go
core/ledger/kvledger/txmgmt/txmgr/tx_simulator_delta_test.go
core/ledger/kvledger/txmgmt/typeddelta/
core/ledger/kvledger/txmgmt/validation/typeddelta_bench_test.go
core/ledger/kvledger/txmgmt/validation/typeddelta_hooks.go
core/ledger/kvledger/txmgmt/validation/typeddelta_hooks_test.go
core/ledger/kvledger/txmgmt/validation/typeddelta_integration_test.go
core/ledger/kvledger/typeddelta_capability_test.go
prefixanalyzer/
vendor/github.com/hyperledger/fabric-chaincode-go/shim/stub_putdelta_test.go
```

## Docker images

Peer and orderer images are built from the modified source with the standard Fabric build (`make docker`) at the base commit above with the listed modifications applied.
