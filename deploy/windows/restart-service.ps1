<#
.SYNOPSIS
  Restarts the HIMS API Windows Service and shows its status.
.EXAMPLE
  powershell -ExecutionPolicy Bypass -File restart-service.ps1
#>
param([string]$ServiceName = 'HIMS-API')
$ErrorActionPreference = 'Stop'
if (-not (Get-Service $ServiceName -ErrorAction SilentlyContinue)) { Write-Error "Service '$ServiceName' not found."; exit 1 }
Write-Host "Restarting $ServiceName..." -ForegroundColor Cyan
Restart-Service $ServiceName
Start-Sleep -Seconds 2
Get-Service $ServiceName | Format-Table -AutoSize
