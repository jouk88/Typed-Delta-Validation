# run-E2.ps1 — E2 append (ht) full sweep.
# 1 condition (ht) x 6 concurrency x 10 reps = 60 fully-isolated cells.
# Count=1000 (matches the E1 sweep), InitBalance=100000000, Amount=1.
$ErrorActionPreference = "Stop"
$runner = Join-Path $PSScriptRoot "cell-runner.ps1"
$OutDir = Join-Path $PSScriptRoot "..\data"

$concs = @(1,5,10,25,50,100)
$reps  = 1..10
$Count = 1000; $Bal = 100000000; $Amount = 1

foreach ($c in $concs) {
  foreach ($r in $reps) {
    & $runner -Exp E2 -Cc ht -Conc $c -Rep $r -Count $Count -InitBalance $Bal -Amount $Amount -OutDir $OutDir
  }
}
Write-Output "E2_ALL_CELLS_DONE"
