<#
.SYNOPSIS  Apply HIMS database migrations (idempotent).
.DESCRIPTION
  Runs the bundled hims-migrate tool against HIMS_DATABASE_URL. Use before
  starting/upgrading the service. On a database that was migrated by hand before
  hims-migrate existed, run `hims-migrate baseline` ONCE first to adopt the
  ledger, then `up` thereafter.
.EXAMPLE
  powershell -File migrate.ps1 -ExeDir C:\hims -Command up
#>
param(
  [string]$ExeDir = (Split-Path $PSScriptRoot -Parent | Split-Path -Parent),
  [ValidateSet('up','status','baseline')][string]$Command = 'up',
  [string]$DatabaseUrl = $env:HIMS_DATABASE_URL
)
$ErrorActionPreference = 'Stop'
if ([string]::IsNullOrWhiteSpace($DatabaseUrl)) { Write-Error 'Set -DatabaseUrl or $env:HIMS_DATABASE_URL'; exit 1 }
$exe = Join-Path $ExeDir 'hims-migrate.exe'
if (-not (Test-Path $exe)) { Write-Error "hims-migrate.exe not found in $ExeDir"; exit 1 }
$env:HIMS_DATABASE_URL = $DatabaseUrl
& $exe $Command
exit $LASTEXITCODE
