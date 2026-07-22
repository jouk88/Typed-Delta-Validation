# run-E4.ps1 — E4 reveal-ratio sweep (ours only).
# Reveal points {0,10,50,100} with variable N: endpoints N=20 (tight CI), midpoints N=10.
# Total = 20+10+10+20 = 60 fully-isolated cells. Count=500, Conc=50. Key embeds the reveal ratio.
$ErrorActionPreference = "Stop"
$runner = Join-Path $PSScriptRoot "cell-runner.ps1"
$OutDir = Join-Path $PSScriptRoot "..\data"

# reveal ratio -> repetition count
$plan  = @(@{rr=0; n=20}, @{rr=10; n=10}, @{rr=50; n=10}, @{rr=100; n=20})
$Count = 500; $Conc = 50; $Bal = 100000000; $Amount = 1

foreach ($p in $plan) {
  foreach ($r in 1..$p.n) {
    $key = "E4-rr{0}-r{1:D2}" -f $p.rr, $r
    & $runner -Exp E4 -Cc ours -Conc $Conc -Rep $r -Count $Count -InitBalance $Bal -Amount $Amount `
              -RevealRatio $p.rr -Key $key -OutDir $OutDir
  }
}
Write-Output "E4_ALL_CELLS_DONE"
