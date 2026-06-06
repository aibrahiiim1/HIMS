<#
.SYNOPSIS
  Stop and remove the HIMS API Windows service.
.DESCRIPTION
  Stops the "HIMS API" service and deletes its registration. Does NOT remove the
  binary, database, or logs. The stored service environment block (including the
  encryption key) is removed with the service key.
.EXAMPLE
  powershell -ExecutionPolicy Bypass -File uninstall-api-service.ps1
#>
[CmdletBinding()]
param([string]$ServiceName = 'HIMS API')

$ErrorActionPreference = 'Stop'
if (-not ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)) {
  Write-Error 'Run this script in an elevated (Administrator) PowerShell.'; exit 1
}
if (-not (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue)) {
  Write-Host "Service '$ServiceName' is not installed — nothing to do." -ForegroundColor Yellow
  exit 0
}

Write-Host "Stopping '$ServiceName'..." -ForegroundColor Cyan
& sc.exe stop "$ServiceName" | Out-Null
Start-Sleep -Seconds 2
Write-Host "Deleting '$ServiceName'..." -ForegroundColor Cyan
& sc.exe delete "$ServiceName" | Out-Null
Start-Sleep -Seconds 1

if (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue) {
  Write-Host "Service still present (a handle may be open) — reboot to finish removal." -ForegroundColor Yellow
} else {
  Write-Host "Removed '$ServiceName'. Binary, database, and logs were left in place." -ForegroundColor Green
}
