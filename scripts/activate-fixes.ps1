# activate-fixes.ps1 — the SINGLE, reliable way to activate HIMS/NIMS changes on the
# local real :8090 service. It rebuilds, restarts, and PROVES the running service is the
# code you committed — or it fails loudly and exits non-zero. No fake success.
#
# Go code cannot be hot-reloaded into the already-running production service; the honest
# solution is a one-command rebuild+restart+verify, not a fake live-reload. This script is
# that command.
#
# USAGE (run ELEVATED for a real activation — stopping/starting the LocalSystem service and
# swapping C:\...\hims-api.exe require admin):
#   powershell -ExecutionPolicy Bypass -File D:\WebProjects\HIMS\scripts\activate-fixes.ps1
#
#   -Frontend       force a web/dist rebuild even if no frontend source changed
#   -SkipFrontend   never rebuild web/dist (backend-only activation)
#   -SkipTests      skip `go test ./...` (build + vet still run) — faster, less safe
#   -Force          if the service process refuses to exit, force-kill it (last resort)
#   -AllowDirty     proceed even though the git working tree has uncommitted changes
#   -DryRun         run every check + build the artifact to a temp path, but DO NOT stop,
#                   swap, or start the service. Needs no admin. Prints what WOULD activate.
#   -SelfTest       run the verification-logic self-test (proves the script fails loudly on a
#                   stale PID / wrong commit) and exit. Needs no admin, touches nothing.
#
# EXIT CODE: 0 only when the final result is ACTIVATED (or a clean DryRun/SelfTest). Any
# staleness — unchanged PID after a backend change, running commit != HEAD, health never
# ready, a failed gate, an unapplied migration — exits non-zero.

[CmdletBinding()]
param(
  [switch]$Frontend,
  [switch]$SkipFrontend,
  [switch]$SkipTests,
  [switch]$Force,
  [switch]$AllowDirty,
  [switch]$DryRun,
  [switch]$SelfTest
)

$ErrorActionPreference = 'Stop'
$repo       = 'D:\WebProjects\HIMS'
$apiExe     = Join-Path $repo 'bin\hims-api.exe'
$apiTmp     = Join-Path $repo 'bin\hims-api.new.exe'
$agentSrc   = Join-Path $repo 'bin\hims-agent.exe'
$agentDst   = 'C:\Program Files\HIMS Relay Agent\hims-agent.exe'
$svcName    = 'HIMS API'
$healthUrl  = 'http://127.0.0.1:8090/healthz'
$svcRegPath = 'HKLM:\SYSTEM\CurrentControlSet\Services\HIMS API'
$webDist    = Join-Path $repo 'web\dist'
$webIndex   = Join-Path $webDist 'index.html'

# ----- output helpers -------------------------------------------------------
function Info($m) { Write-Host $m -ForegroundColor Cyan }
function Ok($m)   { Write-Host "  OK  $m" -ForegroundColor Green }
function Warn($m) { Write-Host "  !!  $m" -ForegroundColor Yellow }

# The running summary. Printed by Show-Summary at every exit path.
$script:S = [ordered]@{
  expected_commit   = ''
  built_backend     = '(not built)'
  running_before    = ''
  running_after     = ''
  old_pid           = ''
  new_pid           = ''
  backend_change    = $false
  frontend_status   = 'unchanged'
  frontend_bundle   = ''
  sqlc_status       = 'not run'
  migration_status  = 'not checked'
  health_status     = 'not checked'
  gates             = 'not run'
  mode              = 'activate'
  result            = 'FAILED'
}

function Show-Summary {
  Write-Host ''
  Write-Host '================ ACTIVATION SUMMARY ================' -ForegroundColor White
  foreach ($k in $script:S.Keys) {
    $label = ($k -replace '_',' ').PadRight(18)
    $val   = "$($script:S[$k])"
    $color = 'Gray'
    if ($k -eq 'result') { $color = if ($val -eq 'ACTIVATED') { 'Green' } elseif ($val -like 'DRY-RUN*' -or $val -eq 'SELFTEST-PASS') { 'Cyan' } else { 'Red' } }
    Write-Host ("  {0} : {1}" -f $label, $val) -ForegroundColor $color
  }
  Write-Host '====================================================' -ForegroundColor White
}

function Fail($m) {
  Write-Host ''
  Write-Host "ACTIVATION FAILED: $m" -ForegroundColor Red
  $script:S.result = 'FAILED'
  Show-Summary
  exit 1
}

function Require-Admin {
  $id = [Security.Principal.WindowsIdentity]::GetCurrent()
  if (-not (New-Object Security.Principal.WindowsPrincipal($id)).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Fail 'Not elevated. A real activation stops/starts the LocalSystem "HIMS API" service and swaps hims-api.exe — re-run this script from an Administrator PowerShell (or use -DryRun to validate without admin).'
  }
}

# Poll $healthUrl; return the parsed object (status/commit/pid/...) or $null if not ready.
function Get-Health {
  try { return Invoke-RestMethod -Uri $healthUrl -TimeoutSec 3 } catch { return $null }
}

# ----- verification logic (isolated so -SelfTest can prove it fails loudly) --
# Returns @{ ok=$bool; reasons=@(...) }. This is the gate that decides ACTIVATED vs FAILED
# for a backend change: the PID MUST change and the running commit MUST equal HEAD.
function Test-Activation {
  param(
    [string]$Expected,       # HEAD short commit we built
    [string]$RunningCommit,  # commit reported by the live /healthz after restart
    [string]$OldPid,
    [string]$NewPid,
    [bool]$BackendChange,
    [bool]$HealthOk
  )
  $reasons = @()
  if (-not $HealthOk) { $reasons += 'health endpoint never became ready' }
  if ($BackendChange) {
    if ([string]::IsNullOrEmpty($NewPid)) { $reasons += 'no running PID after restart' }
    elseif ($NewPid -eq $OldPid)          { $reasons += "PID did not change (still $OldPid) — service is running the OLD process" }
    if ([string]::IsNullOrEmpty($RunningCommit) -or $RunningCommit -eq 'unknown') { $reasons += 'running service did not report a commit' }
    elseif ($RunningCommit -notlike "$Expected*" -and $Expected -notlike "$RunningCommit*") {
      $reasons += "running commit $RunningCommit != built/HEAD $Expected — service is STALE"
    }
  }
  return @{ ok = ($reasons.Count -eq 0); reasons = $reasons }
}

# ============================================================================
# -SelfTest : prove the verification logic rejects stale PID / wrong commit.
# ============================================================================
if ($SelfTest) {
  $script:S.mode = 'selftest'
  Info '== Self-test: verification logic (no service touched) =='
  $cases = @(
    @{ name='healthy backend activation'; args=@{Expected='abc123def456';RunningCommit='abc123def456';OldPid='100';NewPid='200';BackendChange=$true;HealthOk=$true}; expect=$true },
    @{ name='stale PID (unchanged)';       args=@{Expected='abc123def456';RunningCommit='abc123def456';OldPid='100';NewPid='100';BackendChange=$true;HealthOk=$true}; expect=$false },
    @{ name='wrong commit';                args=@{Expected='abc123def456';RunningCommit='000000000000';OldPid='100';NewPid='200';BackendChange=$true;HealthOk=$true}; expect=$false },
    @{ name='health never ready';          args=@{Expected='abc123def456';RunningCommit='abc123def456';OldPid='100';NewPid='200';BackendChange=$true;HealthOk=$false}; expect=$false },
    @{ name='no commit reported';          args=@{Expected='abc123def456';RunningCommit='unknown';OldPid='100';NewPid='200';BackendChange=$true;HealthOk=$true}; expect=$false },
    @{ name='frontend-only (no backend)';  args=@{Expected='abc123def456';RunningCommit='old';OldPid='100';NewPid='100';BackendChange=$false;HealthOk=$true}; expect=$true }
  )
  $bad = 0
  foreach ($c in $cases) {
    $a = $c.args
    $r = Test-Activation @a
    $got = $r.ok
    if ($got -eq $c.expect) { Ok "$($c.name): verdict=$got (expected)" }
    else { Warn "$($c.name): verdict=$got but expected $($c.expect) — LOGIC BUG"; $bad++ }
  }
  if ($bad -gt 0) { Fail "$bad self-test case(s) misbehaved — the fail-loud verification is broken." }
  $script:S.result = 'SELFTEST-PASS'
  Show-Summary
  exit 0
}

# ============================================================================
# Real pipeline
# ============================================================================
Set-Location $repo
if ($DryRun) { $script:S.mode = 'dry-run' } else { Require-Admin }

# --- prerequisites ----------------------------------------------------------
foreach ($tool in 'go','npm','git') {
  if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) { Fail "$tool is not on PATH — cannot build." }
}

# --- git identity + cleanliness --------------------------------------------
$head      = (& git rev-parse HEAD).Trim()
$headShort = (& git rev-parse --short=12 HEAD).Trim()
$script:S.expected_commit = $headShort
$dirty = (& git status --porcelain)
if ($dirty) {
  if ($AllowDirty) { Warn "git tree is DIRTY — building HEAD ($headShort); uncommitted changes are NOT in the artifact." }
  else { Fail "git working tree has uncommitted changes. Commit them first so the built commit == HEAD (or pass -AllowDirty to build HEAD anyway). Acceptance rule: commit locally before activating." }
}

# --- read the service's own DB URL (so migrations target the SAME database) --
$dbUrl = ''
try {
  $envLines = (Get-ItemProperty -Path $svcRegPath -ErrorAction Stop).Environment
  foreach ($line in $envLines) { if ($line -like 'HIMS_DATABASE_URL=*') { $dbUrl = $line.Substring('HIMS_DATABASE_URL='.Length) } }
} catch { Warn "could not read service registry env ($svcRegPath): $($_.Exception.Message)" }
if (-not $dbUrl) { Warn 'HIMS_DATABASE_URL not found in service env; falling back to current shell env.'; $dbUrl = $env:HIMS_DATABASE_URL }

# --- current running state (pre-change) ------------------------------------
$before = Get-Health
$oldPid    = if ($before) { "$($before.pid)" } else { '' }
$oldCommit = if ($before) { "$($before.commit)" } else { '' }
$svc = Get-Service -Name $svcName -ErrorAction SilentlyContinue
$svcRunning = ($svc -and $svc.Status -eq 'Running')
$script:S.old_pid = if ($oldPid) { $oldPid } else { '(service down)' }
$script:S.running_before = if ($oldCommit) { $oldCommit } else { '(unknown)' }
Info "Running before: commit=$($script:S.running_before) pid=$($script:S.old_pid)  |  HEAD=$headShort"

# --- change detection -------------------------------------------------------
# Backend must be re-activated if the running commit != HEAD, or the service is not up.
$backendChange = ($oldCommit -notlike "$headShort*") -or (-not $svcRunning)
$script:S.backend_change = $backendChange

# Frontend rebuild if forced, or web/dist is missing, or any frontend source is newer than
# the built bundle. -SkipFrontend suppresses.
function Get-NewestMTime([string[]]$globs) {
  $newest = [datetime]'1970-01-01'
  foreach ($g in $globs) {
    Get-ChildItem -Path $g -Recurse -File -ErrorAction SilentlyContinue | ForEach-Object {
      if ($_.LastWriteTime -gt $newest) { $newest = $_.LastWriteTime }
    }
  }
  return $newest
}
$frontendChange = $false
if (-not $SkipFrontend) {
  if ($Frontend) { $frontendChange = $true }
  elseif (-not (Test-Path $webIndex)) { $frontendChange = $true }
  else {
    $srcNewest  = Get-NewestMTime @("$repo\web\src", "$repo\web\index.html", "$repo\web\package.json", "$repo\web\vite.config.ts", "$repo\web\tsconfig*.json")
    $distTime   = (Get-Item $webIndex).LastWriteTime
    if ($srcNewest -gt $distTime) { $frontendChange = $true }
  }
}

# sqlc regeneration if any query/schema .sql is newer than the generated Go.
$sqlcChange = $false
$genDir = Join-Path $repo 'internal\storage\postgres'
$sqlNewest = Get-NewestMTime @("$repo\internal\storage\postgres\queries", "$repo\migrations")
$genNewest = Get-NewestMTime @("$genDir\*.go")
if ($sqlNewest -gt $genNewest) { $sqlcChange = $true }

Info "Plan: backend=$backendChange frontend=$frontendChange sqlc=$sqlcChange dryrun=$($DryRun.IsPresent)"

# --- sqlc generate (before gates, so generated code is compiled/tested) -----
if ($sqlcChange) {
  Info '== sqlc generate (query/schema changed) =='
  if (Get-Command sqlc -ErrorAction SilentlyContinue) {
    & sqlc generate
    if ($LASTEXITCODE -ne 0) { Fail 'sqlc generate failed.' }
    $stillDirty = (& git status --porcelain -- $genDir)
    if ($stillDirty -and -not $AllowDirty) { Warn 'sqlc produced changes to generated code — commit them so HEAD matches what is built.' }
    $script:S.sqlc_status = 'regenerated'
    Ok 'sqlc generate complete'
  } else { Warn 'sqlc not on PATH — skipping regeneration (generated code assumed current).'; $script:S.sqlc_status = 'skipped (no sqlc)' }
} else { $script:S.sqlc_status = 'current' }

# ============================================================================
# QUALITY GATES — run with the service still UP so a failing gate never takes
# production down. Any failure exits non-zero and the service is untouched.
# ============================================================================
Info '== Quality gates =='
& go build ./... ; if ($LASTEXITCODE -ne 0) { Fail 'go build ./... failed.' } ; Ok 'go build ./...'
& go vet ./...   ; if ($LASTEXITCODE -ne 0) { Fail 'go vet ./... failed.' }   ; Ok 'go vet ./...'
if ($SkipTests) { Warn 'go test ./... SKIPPED (-SkipTests)' } else {
  & go test ./... ; if ($LASTEXITCODE -ne 0) { Fail 'go test ./... failed.' } ; Ok 'go test ./...'
}
if ($frontendChange) {
  Info '== Frontend build (tsc -b && vite build) =='
  Push-Location (Join-Path $repo 'web')
  try { & npm run build ; if ($LASTEXITCODE -ne 0) { Fail 'npm run build (tsc/vite) failed.' } }
  finally { Pop-Location }
  Ok 'web/dist rebuilt'
  $script:S.frontend_status = 'rebuilt'
} else { Ok 'frontend unchanged — web/dist reused' }
$script:S.gates = if ($SkipTests) { 'build+vet (tests skipped)' } else { 'build+vet+test+frontend' }

# --- confirm frontend bundle + cache busting --------------------------------
if (Test-Path $webIndex) {
  $html = Get-Content $webIndex -Raw
  $m = [regex]::Match($html, 'assets/(index-[A-Za-z0-9_\-]+\.js)')
  if ($m.Success) {
    $bundle = $m.Groups[1].Value
    $script:S.frontend_bundle = $bundle
    if (Test-Path (Join-Path $webDist "assets\$bundle")) {
      Ok "frontend bundle $bundle present — content-hashed filename IS the cache-bust (new build => new URL)"
    } else { Warn "index.html references $bundle but the file is missing in web/dist/assets — stale/broken build." }
  } else { Warn 'could not find a content-hashed bundle reference in index.html — cache busting unverified.' }
} else { Warn 'web/dist/index.html missing — UI will not be served.' }

# --- build the backend artifact to a TEMP path (service still up = no lock) --
if ($backendChange -or $DryRun) {
  Info '== Building hims-api.exe (stamped) =='
  if (Test-Path $apiTmp) { Remove-Item $apiTmp -Force }
  & go build -ldflags "-X main.commit=$headShort" -o $apiTmp ./cmd/hims-api
  if ($LASTEXITCODE -ne 0) { Fail 'go build of hims-api.exe failed.' }
  if (-not (Test-Path $apiTmp)) { Fail 'hims-api.exe artifact was not produced.' }
  # Prove the artifact really carries the commit we intend to ship.
  $built = (& $apiTmp -version) 2>&1
  if ("$built" -notmatch [regex]::Escape($headShort)) { Fail "built artifact reports '$built' — does not contain HEAD $headShort." }
  $script:S.built_backend = $headShort
  Ok "artifact built and reports commit $headShort ($built)"
} else { Ok 'no backend change — skipping artifact build'; $script:S.built_backend = "$oldCommit (unchanged)" }

# --- migrations (apply to the service's DB before starting the new binary) ---
if ($dbUrl) {
  Info '== Migrations =='
  $env:HIMS_DATABASE_URL = $dbUrl
  $status = (& go run ./cmd/hims-migrate status) 2>&1
  $pending = 0
  $mp = [regex]::Match(("$status" | Select-Object -Last 1), '(\d+)\s+pending')
  foreach ($ln in @($status)) { $mm=[regex]::Match("$ln",'(\d+)\s+pending'); if ($mm.Success){ $pending=[int]$mm.Groups[1].Value } }
  if ($pending -gt 0) {
    if ($DryRun) { Warn "$pending migration(s) PENDING — would apply (dry-run: not applied)"; $script:S.migration_status = "$pending pending (dry-run)" }
    else {
      $up = (& go run ./cmd/hims-migrate up) 2>&1
      if ($LASTEXITCODE -ne 0) { Fail "migration apply failed: $up" }
      Ok "applied migrations: $up"
      $script:S.migration_status = "applied $pending"
    }
  } else { Ok 'database up to date — no pending migrations'; $script:S.migration_status = 'up to date' }
} else { Warn 'no DB URL — migrations not checked'; $script:S.migration_status = 'skipped (no DB URL)' }

# ============================================================================
# DRY RUN stops here — nothing on the live service was touched.
# ============================================================================
if ($DryRun) {
  if (Test-Path $apiTmp) { Remove-Item $apiTmp -Force }
  $script:S.health_status = if ($before) { 'ok (unchanged)' } else { 'service down' }
  $script:S.result = if ($backendChange) { 'DRY-RUN (backend would restart)' } else { 'DRY-RUN (frontend-only / no-op)' }
  Info 'Dry run complete — service NOT touched. Re-run elevated (without -DryRun) to activate.'
  Show-Summary
  exit 0
}

# ============================================================================
# Frontend-only path: nothing to restart (web/dist is served live from disk).
# ============================================================================
if (-not $backendChange) {
  if (Test-Path $apiTmp) { Remove-Item $apiTmp -Force }
  # Optional: refresh the relay agent binary if a newer one is staged.
  if ((Test-Path $agentSrc) -and (Test-Path (Split-Path $agentDst))) {
    try { Copy-Item $agentSrc $agentDst -Force; Ok "relay agent refreshed -> $agentDst" } catch { Warn "agent copy skipped: $($_.Exception.Message)" }
  }
  $script:S.health_status = if ($before) { 'ok (backend unchanged)' } else { 'service down' }
  $script:S.new_pid = $oldPid
  $script:S.running_after = $oldCommit
  $v = Test-Activation -Expected $headShort -RunningCommit $oldCommit -OldPid $oldPid -NewPid $oldPid -BackendChange:$false -HealthOk:([bool]$before)
  if (-not $v.ok) { Fail ("frontend-only activation checks failed: " + ($v.reasons -join '; ')) }
  $script:S.result = 'ACTIVATED'
  Info 'Frontend activated (served live from web/dist). Backend unchanged — no restart needed. Hard-reload the browser to pick up the new hashed bundle.'
  Show-Summary
  exit 0
}

# ============================================================================
# Backend activation: stop -> wait for real exit -> swap -> start -> verify.
# ============================================================================
Info '== Stopping service =='
try { Stop-Service -Name $svcName -Force -ErrorAction Stop } catch { Fail "Stop-Service failed: $($_.Exception.Message)" }

# Stop-Service returns before the process necessarily exits AND before the file lock is
# released. Wait for BOTH: the old PID gone and hims-api.exe writable (rename probe).
Info '== Waiting for service process to exit + release the binary =='
$deadline = (Get-Date).AddSeconds(40)
$exited = $false
while ((Get-Date) -lt $deadline) {
  $alive = $false
  if ($oldPid) { $alive = [bool](Get-CimInstance Win32_Process -Filter "ProcessId=$oldPid" -ErrorAction SilentlyContinue) }
  # Also treat any hims-api.exe still holding the path as "alive".
  $lockHeld = $false
  try { $fs = [System.IO.File]::Open($apiExe, 'Open', 'ReadWrite', 'None'); $fs.Close() } catch { $lockHeld = $true }
  if (-not $alive -and -not $lockHeld) { $exited = $true; break }
  Start-Sleep -Milliseconds 500
}
if (-not $exited) {
  if ($Force) {
    Warn "service process did not exit within 40s — force-killing (-Force)."
    if ($oldPid) { Stop-Process -Id $oldPid -Force -ErrorAction SilentlyContinue }
    Start-Sleep -Seconds 2
    try { $fs = [System.IO.File]::Open($apiExe, 'Open', 'ReadWrite', 'None'); $fs.Close() } catch { Fail 'binary still locked after force-kill — cannot swap. A handle is held; reboot or investigate.' }
  } else {
    Fail 'service process did not exit and hims-api.exe is still locked after 40s. Re-run elevated, or pass -Force to force-kill the stuck process. Refusing to swap a locked binary (that is exactly how the OLD pid survives an "activation").'
  }
}
Ok 'service stopped and binary is free'

# --- swap the binary --------------------------------------------------------
Info '== Swapping binary =='
try {
  $bak = "$apiExe.bak"
  if (Test-Path $bak) { Remove-Item $bak -Force }
  if (Test-Path $apiExe) { Rename-Item $apiExe $bak -Force }
  Move-Item $apiTmp $apiExe -Force
  Ok "hims-api.exe replaced (previous kept as hims-api.exe.bak)"
} catch { Fail "could not replace hims-api.exe: $($_.Exception.Message)" }

# --- refresh relay agent while we're deploying ------------------------------
if ((Test-Path $agentSrc) -and (Test-Path (Split-Path $agentDst))) {
  try { Copy-Item $agentSrc $agentDst -Force; Ok "relay agent refreshed -> $agentDst" } catch { Warn "agent copy skipped: $($_.Exception.Message)" }
}

# --- start + wait for health ------------------------------------------------
Info '== Starting service =='
try { Start-Service -Name $svcName -ErrorAction Stop } catch { Fail "Start-Service failed: $($_.Exception.Message). Old binary preserved at hims-api.exe.bak." }

Info '== Waiting for :8090 health =='
$deadline = (Get-Date).AddSeconds(45)
$after = $null
while ((Get-Date) -lt $deadline) {
  $after = Get-Health
  if ($after -and $after.status -eq 'ok') { break }
  Start-Sleep -Milliseconds 700
}
if (-not ($after -and $after.status -eq 'ok')) {
  $script:S.health_status = 'NEVER READY'
  Fail 'service did not report healthy on :8090 within 45s. Check C:\ProgramData\HIMS\API\logs\hims-api.log. The previous binary is at hims-api.exe.bak.'
}
$newPid    = "$($after.pid)"
$newCommit = "$($after.commit)"
$script:S.health_status  = 'ok'
$script:S.new_pid        = $newPid
$script:S.running_after  = $newCommit

# --- FINAL verification: new PID + commit == HEAD (else STALE) --------------
Info '== Verify =='
Get-Service -Name $svcName | Select-Object Name, Status | Format-Table -AutoSize | Out-String | Write-Host
$v = Test-Activation -Expected $headShort -RunningCommit $newCommit -OldPid $oldPid -NewPid $newPid -BackendChange:$true -HealthOk:$true
if (-not $v.ok) {
  Warn ("running commit=$newCommit pid=$newPid ; expected commit=$headShort ; old pid=$oldPid")
  Fail ("service is STALE after restart: " + ($v.reasons -join '; ') + ". The previous binary is at hims-api.exe.bak.")
}
Ok "PID changed $oldPid -> $newPid ; running commit $newCommit matches HEAD $headShort"
Remove-Item "$apiExe.bak" -Force -ErrorAction SilentlyContinue

$script:S.result = 'ACTIVATED'
Show-Summary
Write-Host ''
Write-Host 'ACTIVATED — the live :8090 service is now running the committed code.' -ForegroundColor Green
exit 0
