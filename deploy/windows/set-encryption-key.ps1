<#
.SYNOPSIS
  Sets/updates the HIMS encryption key on the HIMS-API Windows Service and
  restarts it — the one command a customer runs after generating/rotating a key.

.DESCRIPTION
  Updates the service-scoped HIMS_ENCRYPTION_KEY (via NSSM) and restarts the
  service so the new key takes effect. Run elevated. The key is read from a
  hidden prompt and never written to logs or the database.

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File set-encryption-key.ps1
#>
param([string]$ServiceName = 'HIMS-API')

$ErrorActionPreference = 'Stop'
if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)) {
  Write-Error 'Run this script in an elevated (Administrator) PowerShell.'; exit 1
}
$nssm = (Get-Command nssm -ErrorAction SilentlyContinue).Source
if (-not $nssm) { Write-Error 'NSSM not found. Install with: winget install NSSM.NSSM'; exit 1 }
if (-not (Get-Service $ServiceName -ErrorAction SilentlyContinue)) { Write-Error "Service '$ServiceName' not found. Run install-service.ps1 first."; exit 1 }

$secure = Read-Host -AsSecureString 'Paste the HIMS encryption key (input hidden)'
$key = [Runtime.InteropServices.Marshal]::PtrToStringAuto([Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure))
if ([string]::IsNullOrWhiteSpace($key)) { Write-Error 'No key entered.'; exit 1 }

# Preserve existing DB URL / addr, replace only the key.
$existing = (& $nssm get $ServiceName AppEnvironmentExtra) -split "`r?`n" | Where-Object { $_ -and ($_ -notmatch '^HIMS_ENCRYPTION_KEY=') }
& $nssm set $ServiceName AppEnvironmentExtra (@("HIMS_ENCRYPTION_KEY=$key") + $existing) | Out-Null
$key = $null

Write-Host "Restarting $ServiceName..." -ForegroundColor Cyan
Restart-Service $ServiceName
Start-Sleep -Seconds 2
Get-Service $ServiceName | Format-Table -AutoSize
Write-Host "Done. Check HIMS → Administration → Encryption → Status." -ForegroundColor Green
