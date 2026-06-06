<#
.SYNOPSIS
  Stage the HIMS Relay Agent binaries the API serves in its installer packages.

.DESCRIPTION
  This is a DEPLOYER/admin step (run once per release), NOT something operators
  do. It cross-compiles the agent for Windows and Linux and places the binaries
  in the dist directory the HIMS API reads (HIMS_AGENT_DIST_DIR, default
  <api-exe-dir>/agents). After staging, the "Download Installer" buttons in
  HIMS -> Relay Agents produce ready-to-run packages with no build tools needed
  on the operator side.

.PARAMETER DistDir
  Where to place the binaries. Defaults to .\dist\agents (point HIMS_AGENT_DIST_DIR here).
#>
[CmdletBinding()]
param([string] $DistDir = (Join-Path (Get-Location) 'dist\agents'))

$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
New-Item -ItemType Directory -Force -Path $DistDir | Out-Null

Write-Host "Building HIMS Relay Agent binaries into $DistDir" -ForegroundColor Cyan

Push-Location $repo
try {
  $env:GOOS = 'windows'; $env:GOARCH = 'amd64'
  go build -o (Join-Path $DistDir 'hims-agent-windows-amd64.exe') ./cmd/hims-agent
  Write-Host "  [ok] windows/amd64" -ForegroundColor Green

  $env:GOOS = 'linux'; $env:GOARCH = 'amd64'
  go build -o (Join-Path $DistDir 'hims-agent-linux-amd64') ./cmd/hims-agent
  Write-Host "  [ok] linux/amd64" -ForegroundColor Green
} finally {
  Remove-Item Env:GOOS, Env:GOARCH -ErrorAction SilentlyContinue
  Pop-Location
}

Write-Host "Done. Set HIMS_AGENT_DIST_DIR=$DistDir for the API (or place these next to hims-api.exe in an 'agents' folder)." -ForegroundColor Green
