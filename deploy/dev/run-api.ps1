#requires -Version 7.0
<#
.SYNOPSIS
    Developer-only single-instance launcher for hims-api.

.DESCRIPTION
    Guarantees exactly ONE hims-api process is running, so you never end up with
    a no-key instance and a keyed instance fighting over the port (which makes
    the UI show a misleading "pending_restart" encryption state).

    It stops any existing instances, verifies the required environment, confirms
    the port is free, builds, starts a single instance, then prints the safe
    /system/runtime identity.

    SAFETY: never prints HIMS_ENCRYPTION_KEY or any credential secret. The
    database URL is shown only with its password masked.

    NOT for production deployments — use the deployment runbooks for those.

.EXAMPLE
    $env:HIMS_ENCRYPTION_KEY = "<base64-32-byte-key>"
    $env:HIMS_DATABASE_URL   = "postgres://hims:hims@localhost:5433/hims?sslmode=disable"
    ./deploy/dev/run-api.ps1
#>
[CmdletBinding()]
param(
    [string]$Addr     = $(if ($env:HIMS_ADDR) { $env:HIMS_ADDR } else { ':8090' }),
    [string]$RepoRoot = 'D:\WebProjects\HIMS'
)

$ErrorActionPreference = 'Stop'

function Mask-DbUrl([string]$url) {
    if ([string]::IsNullOrWhiteSpace($url)) { return '(not set)' }
    return ($url -replace '://([^:/@]+):[^@]+@', '://$1:****@')
}

Write-Host '== HIMS dev API launcher (single instance) ==' -ForegroundColor Cyan

# 1) Stop any existing instances ----------------------------------------------
$existing = Get-Process hims-api -ErrorAction SilentlyContinue
if ($existing) {
    Write-Host ("Stopping {0} existing hims-api process(es): PID {1}" -f $existing.Count, ($existing.Id -join ', ')) -ForegroundColor Yellow
    $existing | Stop-Process -Force
    Start-Sleep -Milliseconds 800   # let the OS release the socket + the .exe file lock
} else {
    Write-Host 'No existing hims-api process found.' -ForegroundColor DarkGray
}

# 2) Verify required environment (presence only — never echo the key) ---------
if ([string]::IsNullOrWhiteSpace($env:HIMS_ENCRYPTION_KEY)) {
    Write-Error 'HIMS_ENCRYPTION_KEY is not set. Set it for this session first, e.g.:  $env:HIMS_ENCRYPTION_KEY = "<base64-32-byte-key>"'
    exit 1
}
Write-Host 'HIMS_ENCRYPTION_KEY : present (value hidden)' -ForegroundColor Green

if ([string]::IsNullOrWhiteSpace($env:HIMS_DATABASE_URL)) {
    Write-Error 'HIMS_DATABASE_URL is not set. Example:  $env:HIMS_DATABASE_URL = "postgres://hims:hims@localhost:5433/hims?sslmode=disable"'
    exit 1
}
Write-Host ("HIMS_DATABASE_URL   : {0}" -f (Mask-DbUrl $env:HIMS_DATABASE_URL)) -ForegroundColor Green

$env:HIMS_ADDR = $Addr
$portNum = ($Addr -replace '^.*:', '')
Write-Host ("Listen address      : {0}" -f $Addr) -ForegroundColor Green

# 3) Confirm the port is free (single-instance guard before we build) ----------
$inUse = Get-NetTCPConnection -State Listen -LocalPort $portNum -ErrorAction SilentlyContinue
if ($inUse) {
    $ownerPid = $inUse[0].OwningProcess
    $ownerName = (Get-Process -Id $ownerPid -ErrorAction SilentlyContinue).ProcessName
    Write-Error ("Port {0} is already in use by '{1}' (PID {2}). Free it before starting." -f $portNum, $ownerName, $ownerPid)
    exit 1
}

# 4) Build ---------------------------------------------------------------------
$exe = Join-Path $env:TEMP 'hims-api.exe'
Write-Host 'Building hims-api…' -ForegroundColor Cyan
go build -C $RepoRoot -o $exe ./cmd/hims-api
if ($LASTEXITCODE -ne 0) { Write-Error 'Build failed.'; exit 1 }

# 5) Start exactly ONE instance ------------------------------------------------
Write-Host 'Starting one hims-api instance…' -ForegroundColor Cyan
Start-Process -FilePath $exe -WindowStyle Hidden

# 6) Wait for readiness, then print the safe runtime identity ------------------
$base = "http://localhost:$portNum/api/v1"
$runtime = $null
for ($i = 0; $i -lt 25; $i++) {
    Start-Sleep -Milliseconds 400
    try { $runtime = Invoke-RestMethod "$base/system/runtime" -TimeoutSec 2; break } catch { }
}
if (-not $runtime) {
    Write-Error 'API did not become ready (no /system/runtime response). Check the port is free and the key/DB are valid.'
    exit 1
}

Write-Host ''
Write-Host '== API runtime identity (safe; no secrets) ==' -ForegroundColor Green
$runtime | Select-Object process_id, started_at, uptime, api_version, git_commit, port, environment, hostname, encryption_state, key_id, database_url_redacted | Format-List

if ($runtime.encryption_state -eq 'enabled') {
    Write-Host ("OK: encryption ENABLED on PID {0} (key id {1})." -f $runtime.process_id, $runtime.key_id) -ForegroundColor Green
} else {
    Write-Host ("WARNING: encryption_state is '{0}' on PID {1}." -f $runtime.encryption_state, $runtime.process_id) -ForegroundColor Yellow
    Write-Host "  If you expected 'enabled': confirm this is the only instance and the key matches the stored fingerprint (Encryption -> Key Management)." -ForegroundColor Yellow
}
