# cell-runner.ps1 — run ONE fully-isolated measurement cell.
# Single cell executor; the per-experiment wrappers (run-E1/E2/E4/E5E6/E7) iterate and call it.
#
# Flow per cell:
#   1. wsl bash cell-reboot.sh <Cc>   -> verify CELL_READY  (retry once, 30s backoff, else SKIP)
#   1.5 WARM-UP: loaddriver -key warmup -count 10 ... -> discard (cold-start removal)
#   2. measurement loaddriver -key E{Exp}-{Cc}-c{Conc}-r{Rep} -> <OutDir>\<key>.json
#   3. verify JSON exists & non-empty
#   4. [safety: Exp in E5/E6] fetch-blocks.sh + prefixanalyzer -> <OutDir>\<key>-analyzer.txt
#   5. network.sh down (final cleanup)
# Progress: append [CELL][TS][cond][conc][rep] START/END/FAIL to progress.log.
[CmdletBinding()]
param(
  [Parameter(Mandatory=$true)][string]$Exp,          # E1|E2|E4|E5|E6 (tag for key + safety gating)
  [Parameter(Mandatory=$true)][ValidateSet('ours','nocheck','vanilla','ht')][string]$Cc,
  [Parameter(Mandatory=$true)][int]$Conc,
  [Parameter(Mandatory=$true)][int]$Rep,
  [Parameter(Mandatory=$true)][int]$Count,
  [string]$Key,                                       # optional explicit key; else E{Exp}-{Cc}-c{Conc}-r{Rep}
  [int64]$InitBalance = 100000000,
  [int64]$Amount = 1,
  [int]$RevealRatio,                                  # E4 (ours only)
  [int64]$Seed,                                       # E6
  [int64]$AmountMax,                                  # E6
  [int]$CreditRatio,                                  # E6
  [int]$Keys = 1,                                     # E7 multi-hot-key breadth (driver -keys, round-robin)
  [string]$OutDir
)
# NOTE: Continue (not Stop). network.sh down/up and wsl write normal progress to STDERR;
# PS 5.1 wraps native stderr as NativeCommandError which, under Stop, would terminate the
# cell (and, propagating to the wrapper's Stop, abort the whole sweep) even though the run
# succeeded. Real failures are still caught via explicit CELL_READY check + JSON-empty throw.
$ErrorActionPreference = "Continue"

# ---- paths (adjust to your environment) ----
$ScriptDir   = $PSScriptRoot
$DriverExe   = Join-Path $ScriptDir "..\driver\loaddriver.exe"
$AnalyzerExe = Join-Path $ScriptDir "..\analyzer\prefixanalyzer.exe"   # built from analyzer/ inside the modified Fabric tree
$BlocksDir   = "C:\fabric-blocks"                                     # analyzer input; must match BLOCKS_DIR in fetch-blocks.sh
$RebootBash  = (wsl -e wslpath -a (Join-Path $ScriptDir "cell-reboot.sh")) | Select-Object -First 1
$FetchBlocks = (wsl -e wslpath -a (Join-Path $ScriptDir "fetch-blocks.sh")) | Select-Object -First 1
$TestNetDown = "cd /mnt/c/develop/fabric-samples/test-network && ./network.sh down"
$ProgressLog = Join-Path $ScriptDir "progress.log"

if (-not $OutDir) { $OutDir = Join-Path $ScriptDir "..\data" }
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
if (-not $Key) { $Key = "{0}-{1}-c{2}-r{3}" -f $Exp, $Cc, $Conc, $Rep }  # $Exp already carries the 'E' (E1..E6)

if (-not (Test-Path $DriverExe)) { throw "driver not built: $DriverExe (build: cd ..\driver; go build -o loaddriver.exe .)" }

function Write-Progress-Line([string]$state) {
  $ts = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
  ("[CELL][{0}][{1}][{2}][{3}] {4}" -f $ts, $Cc, $Conc, $Rep, $state) | Add-Content -Encoding ascii $ProgressLog
}

# ---- 0. RESUME GUARD: skip (before the ~35min reboot+load) if this cell's output already exists ----
# Makes the whole campaign idempotent/resumable across interruptions: re-running any wrapper
# continues from the first missing cell instead of redoing completed ones.
$existingJson = Join-Path $OutDir ($Key + ".json")
if ((Test-Path $existingJson) -and ((Get-Item $existingJson).Length -gt 0)) {
  Write-Progress-Line "SKIP-EXISTS"
  Write-Output "SKIP-EXISTS $Key (already have $existingJson)"
  return
}

# ---- 1. reboot (retry once w/ 30s backoff) ----
function Invoke-Reboot {
  $out = wsl -e bash $RebootBash $Cc 2>&1
  $out | Write-Output
  return ($out | Select-Object -Last 1) -match 'CELL_READY'
}

Write-Progress-Line "START"
$ready = Invoke-Reboot
if (-not $ready) {
  Write-Output "reboot failed for $Key; 30s backoff then retry once..."
  Start-Sleep -Seconds 30
  $ready = Invoke-Reboot
}
if (-not $ready) {
  Write-Progress-Line "SKIP"
  Write-Output "SKIP $Key (reboot failed twice)"
  return
}

try {
  # ---- 1.5 WARM-UP (discard) ----
  $warm = Join-Path $OutDir ("warmup-{0}.json" -f $Key)
  & $DriverExe -cc $Cc -key warmup -count 10 -concurrency $Conc -amount $Amount -initBalance $InitBalance -out $warm | Out-Null
  Remove-Item -Force -ErrorAction SilentlyContinue $warm, ($warm + ".csv")

  # ---- 2. measurement ----
  $jsonOut = Join-Path $OutDir ($Key + ".json")
  $dargs = @('-cc', $Cc, '-key', $Key, '-count', $Count, '-concurrency', $Conc,
             '-amount', $Amount, '-initBalance', $InitBalance, '-keys', $Keys, '-out', $jsonOut)
  if ($PSBoundParameters.ContainsKey('RevealRatio')) { $dargs += @('-revealRatio', $RevealRatio) }
  if ($PSBoundParameters.ContainsKey('Seed'))        { $dargs += @('-seed', $Seed) }
  if ($PSBoundParameters.ContainsKey('AmountMax'))   { $dargs += @('-amountMax', $AmountMax) }
  if ($PSBoundParameters.ContainsKey('CreditRatio')) { $dargs += @('-creditRatio', $CreditRatio) }
  & $DriverExe @dargs | Out-Null

  # ---- 3. verify JSON non-empty ----
  if (-not (Test-Path $jsonOut) -or (Get-Item $jsonOut).Length -eq 0) {
    throw "measurement output missing/empty: $jsonOut"
  }

  # ---- 4. safety analysis (E5/E6 only) ----
  if ($Exp -in @('E5','E6')) {
    $ns       = if ($Cc -eq 'ht') { 'highthroughput' } else { 'typeddelta' }
    # prefixanalyzer -baseline is a WRITE-SEMANTICS adapter (ours|vanilla|highthroughput|fabriccrdt),
    # NOT the condition name. nocheck uses the typeddelta delta model => decode as 'ours'
    # (applyDelta ignores baseline except for the ht physical-key mapping; ours/nocheck decode identically).
    $baseline = switch ($Cc) { 'ht' { 'highthroughput' } 'vanilla' { 'vanilla' } default { 'ours' } }
    # Orderer-fetched blocks lack TRANSACTIONS_FILTER validation codes, so the analyzer cannot tell
    # which txs the peer rejected -> it would process 0 writes. Supply the invalid txids from the
    # driver's own per-tx CSV (code != VALID).
    $csv     = $jsonOut + ".csv"
    $invFile = Join-Path $OutDir ($Key + "-invalid.txt")
    $inv = @(Get-Content $csv | ForEach-Object {
      $p = $_.Split(','); if ($p.Length -ge 3 -and $p[2] -ne 'VALID' -and $p[2] -ne '' -and $p[2] -ne 'code') { $p[1] }
    } | Where-Object { $_ -ne '' })
    # ALWAYS create the file, even when 0 invalid txids (nocheck/ht commit all) — else the
    # analyzer errors "file not found". Empty file => analyzer treats every tx as VALID.
    Set-Content -Path $invFile -Encoding ascii -Value ($inv -join "`n")
    Write-Output ("=== [safety] invalid_txids={0}; fetch blocks + prefixanalyzer ($Key) ===" -f $inv.Count)
    wsl -e bash $FetchBlocks 2>&1 | Select-Object -Last 1
    $azOut = Join-Path $OutDir ($Key + "-analyzer.txt")
    if (Test-Path $AnalyzerExe) {
      & $AnalyzerExe -blocks $BlocksDir -ns $ns -key $Key -baseline $baseline -invalidTxids $invFile *>&1 | Tee-Object -FilePath $azOut
    } else {
      Write-Output "WARN: analyzer not found at $AnalyzerExe (build prefixanalyzer.exe from analyzer/); skipped for $Key"
    }
  }

  Write-Progress-Line "END"
  Write-Output "OK $Key -> $jsonOut"
}
catch {
  Write-Progress-Line "FAIL"
  Write-Output ("FAIL {0}: {1}" -f $Key, $_.Exception.Message)
}
finally {
  # ---- 5. final cleanup ----
  wsl -e bash -c $TestNetDown 2>&1 | Select-Object -Last 1 | Out-Null
}
