<#
.SYNOPSIS
  Install / run the HIMS Relay Agent (Site Collector) on a Windows host.

.DESCRIPTION
  The HIMS Relay Agent is the single official collector that replaces the old
  per-purpose helper scripts (native PowerShell collector, WMI/DCOM collector).
  Run it on one trusted machine inside a site (a domain-joined Windows box or a
  Windows Server). It registers with HIMS, polls for collection jobs, runs them
  locally (modern WinRM and legacy WMI/DCOM), and posts structured results back.
  It authenticates with a per-agent token and never logs or stores secrets.

  This script installs the agent either:
    * as a Windows Service (recommended, requires NSSM or sc.exe), or
    * as a foreground process (for a quick test).

  Get the AGENT TOKEN once from HIMS -> Agents -> (create agent). It is shown a
  single time; store it in a password manager.

.PARAMETER HimsUrl
  Base URL of the HIMS API, e.g. https://hims.example.com:8090

.PARAMETER Token
  The per-agent bearer token from the HIMS Agents page.

.PARAMETER Name
  Optional display name for this agent (defaults to the machine hostname).

.PARAMETER BinPath
  Path to hims-agent.exe (defaults to .\hims-agent.exe next to this script).

.PARAMETER InsecureTls
  Accept a self-signed HIMS TLS certificate (lab only).

.PARAMETER AsService
  Install as a Windows Service named "HIMSRelayAgent" instead of running in the
  foreground. Requires Administrator. Uses NSSM if present, else sc.exe.

.EXAMPLE
  # Quick foreground test
  .\install-relay-agent.ps1 -HimsUrl https://hims.local:8090 -Token abcd... -InsecureTls

.EXAMPLE
  # Install as a service (run from an elevated prompt)
  .\install-relay-agent.ps1 -HimsUrl https://hims.local:8090 -Token abcd... -AsService
#>
[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)] [string] $HimsUrl,
  [Parameter(Mandatory = $true)] [string] $Token,
  [string] $Name,
  [string] $BinPath = (Join-Path $PSScriptRoot 'hims-agent.exe'),
  [switch] $InsecureTls,
  [switch] $AsService
)

$ErrorActionPreference = 'Stop'
$ServiceName = 'HIMSRelayAgent'

if (-not (Test-Path $BinPath)) {
  throw "hims-agent.exe not found at '$BinPath'. Build it with 'go build -o hims-agent.exe ./cmd/hims-agent' or download it from your HIMS Agents page, then re-run with -BinPath."
}
$BinPath = (Resolve-Path $BinPath).Path
if (-not $Name) { $Name = $env:COMPUTERNAME }

Write-Host "HIMS Relay Agent installer" -ForegroundColor Cyan
Write-Host "  HIMS URL : $HimsUrl"
Write-Host "  Name     : $Name"
Write-Host "  Binary   : $BinPath"
Write-Host "  Mode     : $(if ($AsService) {'Windows Service'} else {'Foreground'})"

# Environment shared by both modes.
$envVars = @{
  'HIMS_URL'         = $HimsUrl
  'HIMS_AGENT_TOKEN' = $Token
  'HIMS_AGENT_NAME'  = $Name
}
if ($InsecureTls) { $envVars['HIMS_AGENT_INSECURE_TLS'] = '1' }

if (-not $AsService) {
  Write-Host "`nStarting agent in the foreground (Ctrl+C to stop)..." -ForegroundColor Green
  foreach ($k in $envVars.Keys) { Set-Item -Path "Env:$k" -Value $envVars[$k] }
  & $BinPath
  return
}

# --- Service install ---------------------------------------------------------
$isAdmin = ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()
).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $isAdmin) { throw "Installing as a service requires an elevated (Administrator) PowerShell." }

# Persist config as machine-level environment variables so the service sees them.
foreach ($k in $envVars.Keys) {
  [Environment]::SetEnvironmentVariable($k, $envVars[$k], 'Machine')
}

$nssm = Get-Command nssm -ErrorAction SilentlyContinue
if ($nssm) {
  Write-Host "`nInstalling service via NSSM..." -ForegroundColor Green
  & $nssm.Source stop    $ServiceName 2>$null | Out-Null
  & $nssm.Source remove  $ServiceName confirm 2>$null | Out-Null
  & $nssm.Source install $ServiceName $BinPath
  & $nssm.Source set     $ServiceName AppDirectory (Split-Path $BinPath)
  & $nssm.Source set     $ServiceName Start SERVICE_AUTO_START
  & $nssm.Source set     $ServiceName AppStdout (Join-Path (Split-Path $BinPath) 'hims-agent.log')
  & $nssm.Source set     $ServiceName AppStderr (Join-Path (Split-Path $BinPath) 'hims-agent.log')
  & $nssm.Source start   $ServiceName
} else {
  Write-Host "`nNSSM not found; installing with sc.exe..." -ForegroundColor Yellow
  sc.exe stop   $ServiceName 2>$null | Out-Null
  sc.exe delete $ServiceName 2>$null | Out-Null
  # sc.exe runs the bare exe; the agent reads its config from the machine env vars set above.
  sc.exe create $ServiceName binPath= "`"$BinPath`"" start= auto DisplayName= "HIMS Relay Agent" | Out-Null
  sc.exe description $ServiceName "HIMS Relay Agent / Site Collector" | Out-Null
  sc.exe start  $ServiceName | Out-Null
  Write-Host "Note: a bare Go binary is not a native Windows service. For reliable" -ForegroundColor Yellow
  Write-Host "service hosting, install NSSM (https://nssm.cc) and re-run this script." -ForegroundColor Yellow
}

Write-Host "`nDone. Check HIMS -> Agents; '$Name' should appear Online within ~30s." -ForegroundColor Green
