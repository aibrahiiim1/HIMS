<#
.SYNOPSIS
  Restarts the HIMS API Windows Service, then verifies it actually came back up
  by polling its health endpoint and reporting the runtime + encryption state.
.PARAMETER HealthUrl
  Runtime endpoint to probe after restart. Default assumes the API listens on
  :8080 locally; override if HIMS_ADDR differs (e.g. http://localhost:9000/api/v1/system/runtime).
.PARAMETER TimeoutSeconds
  How long to wait for the API to answer healthy before reporting failure.
.EXAMPLE
  powershell -ExecutionPolicy Bypass -File restart-service.ps1
.EXAMPLE
  powershell -ExecutionPolicy Bypass -File restart-service.ps1 -HealthUrl http://localhost:9000/api/v1/system/runtime
#>
param(
  [string]$ServiceName = 'HIMS-API',
  [string]$HealthUrl = 'http://localhost:8080/api/v1/system/runtime',
  [int]$TimeoutSeconds = 30
)
$ErrorActionPreference = 'Stop'
if (-not (Get-Service $ServiceName -ErrorAction SilentlyContinue)) { Write-Error "Service '$ServiceName' not found."; exit 1 }
Write-Host "Restarting $ServiceName..." -ForegroundColor Cyan
Restart-Service $ServiceName
Get-Service $ServiceName | Format-Table -AutoSize

# Verify the API actually answers after the restart — a running service that
# can't serve requests (bad key, DB unreachable, port conflict) is not "up".
Write-Host "Probing $HealthUrl (up to $TimeoutSeconds s)..." -ForegroundColor Cyan
$deadline = (Get-Date).AddSeconds($TimeoutSeconds)
$healthy = $false
while ((Get-Date) -lt $deadline) {
  try {
    $r = Invoke-RestMethod -Uri $HealthUrl -TimeoutSec 5 -ErrorAction Stop
    Write-Host ("  OK — version {0}, uptime {1}s, encryption: {2}" -f $r.api_version, $r.uptime_seconds, $r.encryption_state) -ForegroundColor Green
    if ($r.encryption_state -and $r.encryption_state -ne 'enabled' -and $r.encryption_state -ne 'open') {
      Write-Host ("  WARNING: encryption_state is '{0}' — check the configured HIMS_ENCRYPTION_KEY." -f $r.encryption_state) -ForegroundColor Yellow
    }
    $healthy = $true
    break
  } catch {
    Start-Sleep -Seconds 2
  }
}
if (-not $healthy) {
  Write-Error "Service restarted but the API did not respond healthy at $HealthUrl within $TimeoutSeconds s. Check the service logs."
  exit 1
}
