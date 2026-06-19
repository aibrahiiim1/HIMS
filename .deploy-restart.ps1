#requires -Version 5.1
<#
.SYNOPSIS
  HIMS deploy: apply DB migrations (gated), then swap + restart the API service.

.DESCRIPTION
  Migrations run HERE, as part of deploy -- NOT on API startup (hims-api never
  auto-migrates). Order is strict and safe:
    1. resolve HIMS_DATABASE_URL (same DSN the API uses)
    2. build a FRESH hims-migrate (so it embeds the current migration set)
    3. print migration status BEFORE
    4. hims-migrate up   -- if this fails, STOP: no binary swap, no restart, exit 1
    5. print migration status AFTER
    6. stop service, swap hims-api.new.exe to hims-api.exe (rollback on start failure)
    7. start service
    8. (-RunSeed only) health-check, then POST /vendor-fingerprints/seed, print result

  No binary swap or restart happens unless migrations succeed first.
  Every failure is loud: red message, .deploy-result.txt = ERR, non-zero exit code.

  NOTE: keep this file ASCII-only. Windows PowerShell 5.1 reads a UTF-8-no-BOM
  script as ANSI, and a stray smart-quote/dash byte corrupts parsing.

.PARAMETER RunSeed
  After restart + health check, POST /api/v1/vendor-fingerprints/seed and print the
  result. Off by default -- seeding is never automatic.

.PARAMETER DryRun
  Safe mode: resolve DSN, build hims-migrate, print status (incl. pending) and exit.
  Applies NO migrations, performs NO swap/restart/seed. Does not require admin.

.PARAMETER DbUrl
  Override the database DSN. Default: $env:HIMS_DATABASE_URL, then the User/Machine
  HIMS_DATABASE_URL environment variable.

.PARAMETER PgContainer
  Postgres docker container used by -RunSeed to mint a short-lived admin session.
  Default: hims-pg.

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File .\.deploy-restart.ps1 -DryRun

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File .\.deploy-restart.ps1

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File .\.deploy-restart.ps1 -RunSeed
#>
[CmdletBinding()]
param(
  [switch]$RunSeed,
  [switch]$DryRun,
  [string]$DbUrl,
  [string]$PgContainer = 'hims-pg',
  [int]$HealthTimeoutSec = 45
)

$ErrorActionPreference = 'Stop'

$Repo   = 'D:\WebProjects\HIMS'
$Bin    = Join-Path $Repo 'bin'
$Svc    = 'HIMS API'
$ApiUrl = 'http://localhost:8090'
$Log    = Join-Path $Repo '.deploy-result.txt'

function Info    ($m) { Write-Host "[deploy] $m" }
function Section ($m) { Write-Host ""; Write-Host "==== $m ====" -ForegroundColor Cyan }
function MaskDsn ($u) { if ($u) { return ($u -replace '(://[^:/@]+:)[^@]+(@)', '$1****$2') } return $u }
function Die ($m) {
  Write-Host ""
  Write-Host "[deploy] FAILED: $m" -ForegroundColor Red
  "ERR $(Get-Date -Format o) :: $m" | Set-Content $Log
  exit 1
}
function NativeOK ($what) { if ($LASTEXITCODE -ne 0) { Die "$what (exit code $LASTEXITCODE)" } }

try {
  # --- 0. resolve DSN (same one the API uses) -------------------------------
  if (-not $DbUrl) { $DbUrl = $env:HIMS_DATABASE_URL }
  if (-not $DbUrl) { $DbUrl = [Environment]::GetEnvironmentVariable('HIMS_DATABASE_URL', 'User') }
  if (-not $DbUrl) { $DbUrl = [Environment]::GetEnvironmentVariable('HIMS_DATABASE_URL', 'Machine') }
  if (-not $DbUrl) { Die "HIMS_DATABASE_URL is not set. Pass -DbUrl, or set the same HIMS_DATABASE_URL the API uses." }
  $env:HIMS_DATABASE_URL = $DbUrl

  Section "HIMS deploy   DryRun=$DryRun  RunSeed=$RunSeed"
  Info "repo:      $Repo"
  Info "service:   $Svc"
  Info "database:  $(MaskDsn $DbUrl)"

  # --- 1. preflight: tooling + (for real runs) elevation --------------------
  if (-not (Get-Command go -ErrorAction SilentlyContinue)) { Die "'go' is not on PATH (needed to build a fresh hims-migrate)." }
  $isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
  if ((-not $DryRun) -and (-not $isAdmin)) {
    Die "Administrator rights are required to stop/start the '$Svc' service. Re-run elevated (or use -DryRun to preview safely)."
  }
  if ($RunSeed -and (-not (Get-Command docker -ErrorAction SilentlyContinue))) {
    Die "'docker' is not on PATH (required by -RunSeed to mint a short-lived admin session against $PgContainer)."
  }

  # --- 2. build a FRESH hims-migrate (embeds current migration set) ---------
  Section "Build fresh hims-migrate"
  $migrateExe = Join-Path $Bin 'hims-migrate.new.exe'
  & go -C $Repo build -o $migrateExe ./cmd/hims-migrate
  NativeOK "go build ./cmd/hims-migrate"
  Info "built $migrateExe"

  # --- 3. migration status BEFORE -------------------------------------------
  Section "Migration status BEFORE"
  & $migrateExe status
  NativeOK "hims-migrate status (before)"

  if ($DryRun) {
    Section "DRY RUN - nothing changed"
    Info "No migrations applied. No binary swap. No restart. No seed."
    "DRYRUN $(Get-Date -Format o)" | Set-Content $Log
    exit 0
  }

  # --- 4. apply migrations  (HARD GATE: stop here on failure) ---------------
  Section "Apply migrations: hims-migrate up"
  & $migrateExe up
  NativeOK "hims-migrate up"   # on failure: no swap, no restart, ERR log, exit 1

  # --- 5. migration status AFTER --------------------------------------------
  Section "Migration status AFTER"
  & $migrateExe status
  NativeOK "hims-migrate status (after)"

  # migrations succeeded: keep the on-disk hims-migrate.exe current too
  Move-Item $migrateExe (Join-Path $Bin 'hims-migrate.exe') -Force

  # --- 6/7. stop, swap, start ----------------------------------------------
  Section "Restart API service"
  Info "stopping $Svc ..."
  Stop-Service -Name $Svc -Force
  Start-Sleep -Milliseconds 1000

  $apiNew = Join-Path $Bin 'hims-api.new.exe'
  $apiCur = Join-Path $Bin 'hims-api.exe'
  $apiOld = Join-Path $Bin 'hims-api.old.exe'
  $swapped = $false
  if (Test-Path $apiNew) {
    if (Test-Path $apiOld) { Remove-Item $apiOld -Force }
    Move-Item $apiCur $apiOld -Force
    Move-Item $apiNew $apiCur -Force
    $swapped = $true
    Info "swapped in new hims-api.exe (previous saved as hims-api.old.exe)"
  } else {
    Info "no hims-api.new.exe staged - restarting current binary unchanged"
  }

  try {
    Start-Service -Name $Svc
    Info "$Svc started"
  } catch {
    if ($swapped) {
      Info "service failed to start on the new binary - rolling back to previous"
      Move-Item $apiCur (Join-Path $Bin 'hims-api.failed.exe') -Force
      Move-Item $apiOld $apiCur -Force
      Start-Service -Name $Svc   # if this also fails, the outer catch reports it
      Die "new hims-api failed to start; rolled back to previous binary (bad build kept as hims-api.failed.exe). Original error: $($_.Exception.Message)"
    }
    throw
  }

  # --- 8. optional seed (only after restart + health check) -----------------
  if ($RunSeed) {
    Section "Health check before seed"
    $healthy = $false
    $deadline = (Get-Date).AddSeconds($HealthTimeoutSec)
    while ((Get-Date) -lt $deadline) {
      try { Invoke-WebRequest -Uri "$ApiUrl/api/v1/health" -TimeoutSec 4 -UseBasicParsing | Out-Null; $healthy = $true; break }
      catch { if ($_.Exception.Response) { $healthy = $true; break } }  # any HTTP reply (incl 401) = process is up
      Start-Sleep -Seconds 2
    }
    if (-not $healthy) { Die "API not reachable on $ApiUrl within $HealthTimeoutSec s - NOT seeding." }
    Info "API reachable"

    Section "Seed built-in fingerprint catalog"
    # mint a short-lived admin session in the DB, call the gated endpoint, clean up.
    $uri = [uri]$DbUrl
    $parts = $uri.UserInfo.Split(':'); $pgUser = $parts[0]; $pgPass = ''
    if ($parts.Count -gt 1) { $pgPass = $parts[1] }
    $pgDb = $uri.AbsolutePath.TrimStart('/')
    if ((-not $pgUser) -or (-not $pgDb)) { Die "could not parse pg user/db from DSN for seeding." }
    $env:PGPASSWORD = $pgPass

    $adminId = (& docker exec $PgContainer psql -U $pgUser -d $pgDb -At -c "SELECT u.id FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN role_permissions rp ON rp.role_id=ur.role_id JOIN permissions p ON p.id=rp.permission_id WHERE p.code='rbac.manage' AND u.is_active LIMIT 1;")
    if ($LASTEXITCODE -ne 0) { Die "psql lookup of an admin user failed (exit $LASTEXITCODE) - cannot seed." }
    $adminId = ("$adminId").Trim()
    if (-not $adminId) { Die "no active admin user (rbac.manage) found - cannot seed." }

    $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    $rb = New-Object byte[] 32; $rng.GetBytes($rb)
    $tok  = (($rb | ForEach-Object { $_.ToString('x2') }) -join '')
    $hash = ((([System.Security.Cryptography.SHA256]::Create()).ComputeHash([Text.Encoding]::UTF8.GetBytes($tok)) | ForEach-Object { $_.ToString('x2') }) -join '')

    & docker exec $PgContainer psql -U $pgUser -d $pgDb -c "INSERT INTO sessions (token_hash,user_id,expires_at) VALUES ('$hash','$adminId', now() + interval '15 minutes');" | Out-Null
    if ($LASTEXITCODE -ne 0) { Die "failed to mint a deploy seed session (psql exit $LASTEXITCODE)." }

    try {
      $resp = Invoke-RestMethod -Uri "$ApiUrl/api/v1/vendor-fingerprints/seed" -Method Post -Headers @{ Cookie = "hims_session=$tok" } -TimeoutSec 180
      Info ("seed result: " + ($resp | ConvertTo-Json -Depth 6 -Compress))
    } catch {
      $msg = $_.Exception.Message
      if ($_.ErrorDetails) { $msg = "$msg :: $($_.ErrorDetails.Message)" }
      & docker exec $PgContainer psql -U $pgUser -d $pgDb -c "DELETE FROM sessions WHERE token_hash='$hash';" | Out-Null
      Die "seed request failed: $msg"
    }
    & docker exec $PgContainer psql -U $pgUser -d $pgDb -c "DELETE FROM sessions WHERE token_hash='$hash';" | Out-Null
    Info "deploy seed session cleaned up"
  } else {
    Info "seed skipped (pass -RunSeed to reseed the built-in fingerprint catalog)"
  }

  Section "Deploy complete"
  $summary = "OK $(Get-Date -Format o) :: migrated"
  if ($swapped) { $summary += "+swapped" }
  if ($RunSeed) { $summary += "+seeded" }
  $summary | Set-Content $Log
  Info "done."
  exit 0
}
catch {
  $where = ''
  if ($_.InvocationInfo) { $where = "  [line $($_.InvocationInfo.ScriptLineNumber): $($_.InvocationInfo.Line.Trim())]" }
  Die "$($_.Exception.Message)$where"
}
