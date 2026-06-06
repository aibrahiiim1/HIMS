<#
.SYNOPSIS
  Restart the HIMS API Windows service and confirm it came back healthy.
.DESCRIPTION
  Stops and starts the "HIMS API" service, then waits for the HTTP health
  endpoint and prints the runtime status (encryption enabled, DB connected,
  service mode). Use after deploying a new hims-api.exe binary.
.EXAMPLE
  powershell -ExecutionPolicy Bypass -File restart-api-service.ps1
#>
[CmdletBinding()]
param(
  [string]$ServiceName = 'HIMS API',
  [string]$HealthUrl   = 'http://localhost:8090/healthz'
)

$ErrorActionPreference = 'Stop'
if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)) {
  Write-Error 'Run this script in an elevated (Administrator) PowerShell.'; exit 1
}
if (-not (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue)) {
  Write-Error "Service '$ServiceName' is not installed. Run install-api-service.ps1 first."; exit 1
}

Write-Host "Restarting '$ServiceName'..." -ForegroundColor Cyan
Restart-Service -Name $ServiceName -Force
Start-Sleep -Seconds 3
Get-Service -Name $ServiceName | Format-Table -AutoSize

# Poll health for up to ~20s.
$ok = $false
for ($i = 0; $i -lt 10; $i++) {
  try {
    $r = Invoke-WebRequest -Uri $HealthUrl -TimeoutSec 3 -UseBasicParsing
    if ($r.StatusCode -eq 200) { $ok = $true; break }
  } catch { Start-Sleep -Seconds 2 }
}
if ($ok) {
  Write-Host "Health OK ($HealthUrl)." -ForegroundColor Green
} else {
  Write-Host "Health check did not pass yet — check %ProgramData%\HIMS\API\logs\hims-api.log" -ForegroundColor Yellow
}
Write-Host "Confirm encryption Enabled + DB connected in HIMS -> System Health." -ForegroundColor Green
