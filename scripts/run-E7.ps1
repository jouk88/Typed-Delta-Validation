# run-E7.ps1 — E7 multi-hot-key breadth (N=10).
# ours vs vanilla; keys {1,10,100} uniform round-robin; concurrency 50; count 1000;
# large initial balance (no overdraft).
# 2 conditions x 3 key-counts x 10 reps = 60 fully-isolated cells (per-cell reboot + warm-up).
$ErrorActionPreference = "Stop"
$runner = Join-Path $PSScriptRoot "cell-runner.ps1"
$OutDir = Join-Path $PSScriptRoot "..\data"

$ccs      = @("ours","vanilla")
$keysList = @(1,10,100)
$reps     = 1..10
$Conc = 50; $Count = 1000; $Bal = 100000000; $Amount = 1

foreach ($cc in $ccs) {
  foreach ($k in $keysList) {
    foreach ($r in $reps) {
      $key = "E7-{0}-k{1}-r{2}" -f $cc, $k, $r
      & $runner -Exp E7 -Cc $cc -Conc $Conc -Rep $r -Count $Count -InitBalance $Bal -Amount $Amount `
                -Keys $k -Key $key -OutDir $OutDir
    }
  }
}
Write-Output "E7_ALL_CELLS_DONE"
