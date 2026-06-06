<#
.SYNOPSIS
  Install hims-api as a native Windows service ("HIMS API").
.DESCRIPTION
  Registers hims-api.exe with the Windows Service Control Manager. The binary
  itself implements the service protocol (golang.org/x/sys/windows/svc), so no
  NSSM or wrapper is needed. The service:
    - starts on boot (auto-start, delayed)
    - restarts automatically on failure
    - loads HIMS_ENCRYPTION_KEY / HIMS_DATABASE_URL / HIMS_ADDR / HIMS_AGENT_DIST_DIR
    - refuses to start if encrypted credentials exist but the key is missing/invalid
    - logs to %ProgramData%\HIMS\API\logs\hims-api.log

  Secrets are stored as the service's own environment block (HKLM service key),
  readable only by SYSTEM/Administrators — never written to a world-readable file
  and never echoed to the console.
.EXAMPLE
  powershell -ExecutionPolicy Bypass -File install-api-service.ps1 `
    -ExePath C:\hims\hims-api.exe `
    -DatabaseUrl 'postgres://hims:hims@localhost:5433/hims?sslmode=disable' `
    -Addr ':8090' -AgentDistDir 'C:\hims\agents'
#>
[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)][string]$ExePath,
  [string]$ServiceName  = 'HIMS API',
  [string]$DatabaseUrl  = 'postgres://hims:hims@localhost:5433/hims?sslmode=disable',
  [string]$Addr         = ':8090',
  [string]$AgentDistDir = '',
  [string]$EncryptionKey = ''   # optional; if omitted you are prompted (hidden input)
)

$ErrorActionPreference = 'Stop'

# Elevation required to register a service.
if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)) {
  Write-Error 'Run this script in an elevated (Administrator) PowerShell.'; exit 1
}
if (-not (Test-Path $ExePath)) { Write-Error "Executable not found: $ExePath"; exit 1 }
$ExePath = (Resolve-Path $ExePath).Path

# Encryption key: prompt if not supplied. Never logged.
$key = $EncryptionKey
if ([string]::IsNullOrWhiteSpace($key)) {
  $secure = Read-Host -AsSecureString 'Paste the HIMS encryption key (input hidden, leave empty to skip)'
  $key = [Runtime.InteropServices.Marshal]::PtrToStringAuto([Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure))
}

# Optional: apply DB migrations if hims-migrate.exe is alongside the API binary.
$migrate = Join-Path (Split-Path $ExePath) 'hims-migrate.exe'
if (Test-Path $migrate) {
  Write-Host 'Applying database migrations...' -ForegroundColor Cyan
  $env:HIMS_DATABASE_URL = $DatabaseUrl
  & $migrate up
  if ($LASTEXITCODE -ne 0) { Write-Error 'Migration failed — service not installed.'; exit 1 }
}

# Remove any prior instance so re-running is idempotent.
if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) {
  Write-Host "Service '$ServiceName' exists — stopping & removing for a clean reinstall." -ForegroundColor Yellow
  & sc.exe stop "$ServiceName" | Out-Null
  Start-Sleep -Seconds 2
  & sc.exe delete "$ServiceName" | Out-Null
  Start-Sleep -Seconds 1
}

# Create the service (binPath quoted for spaces). Auto-start, delayed.
Write-Host "Creating service '$ServiceName'..." -ForegroundColor Cyan
& sc.exe create "$ServiceName" binPath= "`"$ExePath`"" start= delayed-auto DisplayName= "HIMS API" | Out-Null
if ($LASTEXITCODE -ne 0) { Write-Error 'sc.exe create failed.'; exit 1 }
& sc.exe description "$ServiceName" "HIMS inventory & monitoring REST API" | Out-Null

# Restart-on-failure: 5s, 10s, then every 30s; reset the counter after 1 day.
& sc.exe failure "$ServiceName" reset= 86400 actions= restart/5000/restart/10000/restart/30000 | Out-Null

# Service environment block (HKLM, SYSTEM/Admin-readable only). Multi-string.
$svcKey = "HKLM:\SYSTEM\CurrentControlSet\Services\$ServiceName"
$envLines = @(
  "HIMS_DATABASE_URL=$DatabaseUrl",
  "HIMS_ADDR=$Addr",
  "HIMS_SERVICE_MODE=windows-service"
)
if ($AgentDistDir) { $envLines += "HIMS_AGENT_DIST_DIR=$AgentDistDir" }
if (-not [string]::IsNullOrWhiteSpace($key)) { $envLines += "HIMS_ENCRYPTION_KEY=$key" }
New-ItemProperty -Path $svcKey -Name 'Environment' -PropertyType MultiString -Value $envLines -Force | Out-Null
$key = $null  # drop from memory

Write-Host 'Starting service...' -ForegroundColor Cyan
& sc.exe start "$ServiceName" | Out-Null
Start-Sleep -Seconds 3
Get-Service -Name $ServiceName | Format-Table -AutoSize

Write-Host ''
Write-Host "Installed. Logs: %ProgramData%\HIMS\API\logs\hims-api.log" -ForegroundColor Green
Write-Host "Verify in HIMS -> System Health: encryption Enabled, DB connected, service mode = windows-service." -ForegroundColor Green
