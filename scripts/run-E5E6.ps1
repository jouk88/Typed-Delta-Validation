# run-E5E6.ps1 — E5 deterministic safety + E6 randomized-stress safety.
# Each cell reboots, loads, fetches blocks, and runs prefixanalyzer
# (Exp E5/E6 triggers the runner's safety branch).
#
# E5 deterministic: conditions {ours,nocheck,ht}; InitBalance=100, Count=500, Amount=1, Conc=50.
#   Expected: ours=0 prefix violations; nocheck and ht > 0.
# E6 randomized:    seeds {42,137,256,314,628} x {ours,nocheck,ht};
#   AmountMax=10, CreditRatio=30, InitBalance=200, Count=500, Conc=50.
#   Expected: ours=0 for ALL seeds; nocheck and ht > 0 with varying counts across seeds.
# vanilla is excluded from the safety comparison: the invariant scope under test is the
# delta/append update models, and vanilla's read-modify-write already fails at MVCC.
$ErrorActionPreference = "Stop"
$runner = Join-Path $PSScriptRoot "cell-runner.ps1"
$OutDir = Join-Path $PSScriptRoot "..\data"

$conds = @("ours","nocheck","ht")

# ---- E5 deterministic ----
Write-Output "=== E5 deterministic safety ==="
foreach ($cc in $conds) {
  $key = "E5-{0}-det" -f $cc
  & $runner -Exp E5 -Cc $cc -Conc 50 -Rep 1 -Count 500 -InitBalance 100 -Amount 1 -Key $key -OutDir $OutDir
}

# ---- E6 randomized stress ----
Write-Output "=== E6 randomized stress ==="
$seeds = @(42,137,256,314,628)
foreach ($cc in $conds) {
  foreach ($s in $seeds) {
    $key = "E6-{0}-seed{1}" -f $cc, $s
    & $runner -Exp E6 -Cc $cc -Conc 50 -Rep 1 -Count 500 -InitBalance 200 -Amount 1 `
              -Seed $s -AmountMax 10 -CreditRatio 30 -Key $key -OutDir $OutDir
  }
}
Write-Output "E5E6_ALL_CELLS_DONE"
