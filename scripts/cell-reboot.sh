#!/usr/bin/env bash
# Per-cell reboot (WSL/bash). Brings up a completely fresh Fabric network and deploys
# only the chaincode the condition needs, so every measurement cell starts with zero
# ledger/cache/process carryover (full cell isolation).
#
# Usage:  bash cell-reboot.sh <ours|nocheck|vanilla|ht>
#   ours    -> typeddelta      (../typed-delta-chaincode)
#   nocheck -> typeddelta      (same chaincode; nocheck is a driver-side seed variant)
#   vanilla -> vanilla         (../vanilla-rmw-chaincode)
#   ht      -> highthroughput  (../high-throughput-chaincode)
# The chaincode directories hold the corresponding files from chaincode/ in this repository.
# On success the LAST line printed is exactly:  CELL_READY
# Any earlier failure exits non-zero WITHOUT printing CELL_READY (the PS1 runner keys on that).
set -u

COND="${1:-}"
if [ -z "$COND" ]; then
  echo "usage: cell-reboot.sh <ours|nocheck|vanilla|ht>" >&2
  exit 2
fi

# Toolchain env for the chaincode build (adjust to your environment).
export GOROOT="$HOME/goroot-ccenv"
export PATH="$GOROOT/bin:$PATH"
export GOCACHE=/tmp/gocache
export GOPATH=/tmp/gopath

# fabric-samples test-network checkout (override with TESTNET).
TESTNET="${TESTNET:-/mnt/c/develop/fabric-samples/test-network}"
cd "$TESTNET" || { echo "FATAL: cannot cd $TESTNET" >&2; exit 3; }

# Map condition -> (chaincode name, chaincode path).
case "$COND" in
  ours|nocheck) CCN="typeddelta";     CCP="../typed-delta-chaincode" ;;
  vanilla)      CCN="vanilla";        CCP="../vanilla-rmw-chaincode" ;;
  ht)           CCN="highthroughput"; CCP="../high-throughput-chaincode" ;;
  *) echo "FATAL: unknown condition '$COND' (want ours|nocheck|vanilla|ht)" >&2; exit 2 ;;
esac

echo ">>> [$COND] network down (remove containers + volumes + ledger)"
./network.sh down >/tmp/cr_down.log 2>&1; rc=$?; echo "down rc=$rc"
if [ $rc -ne 0 ]; then echo "FATAL: network down failed (rc=$rc)" >&2; tail -5 /tmp/cr_down.log >&2; exit 4; fi

echo ">>> [$COND] network up createChannel -c mychannel (fresh LevelDB genesis)"
./network.sh up createChannel -c mychannel >/tmp/cr_up.log 2>&1; rc=$?; echo "up rc=$rc"; tail -1 /tmp/cr_up.log
if [ $rc -ne 0 ]; then echo "FATAL: network up failed (rc=$rc)" >&2; tail -5 /tmp/cr_up.log >&2; exit 5; fi

echo ">>> [$COND] deployCC -ccn $CCN -ccp $CCP -ccl go"
./network.sh deployCC -ccn "$CCN" -ccp "$CCP" -ccl go >/tmp/cr_deploy.log 2>&1; rc=$?; echo "deploy rc=$rc"
grep -aE "Chaincode definition committed|Deploying chaincode failed" /tmp/cr_deploy.log | tail -1
if [ $rc -ne 0 ]; then echo "FATAL: deployCC $CCN failed (rc=$rc)" >&2; tail -5 /tmp/cr_deploy.log >&2; exit 6; fi

echo CELL_READY
