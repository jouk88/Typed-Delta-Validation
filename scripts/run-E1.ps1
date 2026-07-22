# run-E1.ps1 — E1 throughput sweep (N=10).
# 3 conditions x 6 concurrency x 10 reps = 180 fully-isolated cells (per-cell reboot).
# Count=1000, InitBalance=100000000 (large: isolates MVCC, no overdraft), Amount=1.
$ErrorActionPreference = "Stop"
$runner = Join-Path $PSScriptRoot "cell-runner.ps1"
$OutDir = Join-Path $PSScriptRoot "..\data"

$ccs   = @("ours","nocheck","vanilla")
$concs = @(1,5,10,25,50,100)
$reps  = 1..10
$Count = 1000; $Bal = 100000000; $Amount = 1

foreach ($cc in $ccs) {
  foreach ($c in $concs) {
    foreach ($r in $reps) {
      & $runner -Exp E1 -Cc $cc -Conc $c -Rep $r -Count $Count -InitBalance $Bal -Amount $Amount -OutDir $OutDir
    }
  }
}
Write-Output "E1_ALL_CELLS_DONE"
