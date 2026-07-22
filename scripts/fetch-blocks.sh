#!/usr/bin/env bash
# Fetch every committed block of the channel into BLOCKS_DIR (prefixanalyzer input).
TESTNET="${TESTNET:-/mnt/c/develop/fabric-samples/test-network}"
cd "$TESTNET"
export PATH="$PWD/../bin:$PATH"
export FABRIC_CFG_PATH="$PWD/../config"
. ./scripts/envVar.sh
setGlobals 1
OUT="${BLOCKS_DIR:-/mnt/c/fabric-blocks}"
rm -rf "$OUT"; mkdir -p "$OUT"
ORD=(-o localhost:7050 --ordererTLSHostnameOverride orderer.example.com --tls --cafile "$ORDERER_CA")
H=$(peer channel getinfo -c mychannel 2>/dev/null | sed -n 's/.*"height":\([0-9]*\).*/\1/p')
echo "channel height=$H"
for ((i=0; i<H; i++)); do
  peer channel fetch "$i" "$OUT/$i.block" "${ORD[@]}" -c mychannel >/dev/null 2>&1
done
echo "fetched $(ls "$OUT" | wc -l) block files"
echo FETCH_DONE
