<#
.SYNOPSIS
  Installs the HIMS API as a Windows Service so it starts automatically and
  survives reboots — no manual launching required.

.DESCRIPTION
  Uses NSSM (the Non-Sucking Service Manager) to run the HIMS API executable as
  a service, with the encryption key and database URL set as service-scoped
  environment variables. Run from an elevated (Administrator) PowerShell.

  The encryption key is stored only in the service configuration / machine
  environment — never in the HIMS database or logs.

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File install-service.ps1 -ExePath C:\hims\hims-api.exe
#>
param(
  [Parameter(Mandatory = $true)][string]$ExePath,
  [string]$ServiceName = 'HIMS-API',
  [string]$DatabaseUrl = 'postgres://hims:hims@localhost:5433/hims?sslmode=disable',
  [string]$Addr = ':8090'
)

$ErrorActionPreference = 'Stop'

# Require elevation — service + machine env changes need it.
if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)) {
  Write-Error 'Run this script in an elevated (Administrator) PowerShell.'; exit 1
}
if (-not (Test-Path $ExePath)) { Write-Error "Executable not found: $ExePath"; exit 1 }

# NSSM reliably runs any executable as a service (a plain Go exe does not speak
# the Windows Service Control Manager protocol on its own).
$nssm = (Get-Command nssm -ErrorAction SilentlyContinue).Source
if (-not $nssm) {
  Write-Host 'NSSM is not installed. Install it once with:  winget install NSSM.NSSM' -ForegroundColor Yellow
  Write-Host 'then re-run this script.'; exit 1
}

# Prompt for the encryption key securely (never echoed, never logged).
$secure = Read-Host -AsSecureString 'Paste the HIMS encryption key (input hidden)'
$key = [Runtime.InteropServices.Marshal]::PtrToStringAuto([Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure))
if ([string]::IsNullOrWhiteSpace($key)) { Write-Error 'No key entered.'; exit 1 }

Write-Host "Installing service '$ServiceName'..." -ForegroundColor Cyan
& $nssm install $ServiceName $ExePath | Out-Null
& $nssm set $ServiceName AppDirectory (Split-Path $ExePath) | Out-Null
& $nssm set $ServiceName Start SERVICE_AUTO_START | Out-Null
# Service-scoped environment — key lives here, not in the DB.
& $nssm set $ServiceName AppEnvironmentExtra "HIMS_ENCRYPTION_KEY=$key" "HIMS_DATABASE_URL=$DatabaseUrl" "HIMS_ADDR=$Addr" | Out-Null
$key = $null  # drop from memory

& $nssm start $ServiceName | Out-Null
Start-Sleep -Seconds 2
Get-Service $ServiceName | Format-Table -AutoSize
Write-Host "Done. Verify in HIMS → Administration → Encryption → Status (should be Enabled)." -ForegroundColor Green
