# activate-fixes.ps1 — deploy the current HIMS code to the live services on CHV-MISMGR.
# MUST be run ELEVATED (Run as Administrator): stopping/starting LocalSystem services
# and writing to C:\Program Files require admin; a normal shell gets "Cannot open service".
#
#   powershell -ExecutionPolicy Bypass -File D:\WebProjects\HIMS\scripts\activate-fixes.ps1
#
# What it activates:
#   - hims-api.exe  : never-attempted reconciler, not_authorized sub-reasons, + all prior
#                     server fixes (camera false-200, dashboard tiles, scan-stability).
#   - hims-agent.exe: legacy-Windows native collection (Get-WmiObject on PowerShell 2.0),
#                     durable WMI/DCOM verdict outranks a WinRM connect-timeout (label fix).
#   (The SPA in web\dist is already served live from disk — no restart needed for the UI.)

$ErrorActionPreference = 'Stop'
$repo     = 'D:\WebProjects\HIMS'
$apiExe   = Join-Path $repo 'bin\hims-api.exe'
$agentSrc = Join-Path $repo 'bin\hims-agent.exe'
$agentDst = 'C:\Program Files\HIMS Relay Agent\hims-agent.exe'

function Require-Admin {
  $id = [Security.Principal.WindowsIdentity]::GetCurrent()
  if (-not (New-Object Security.Principal.WindowsPrincipal($id)).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Not elevated. Re-run this script from an Administrator PowerShell.'
  }
}
Require-Admin

Write-Host '== Stopping services ==' -ForegroundColor Cyan
Stop-Service 'HIMS API' -Force
Stop-Service 'HIMSRelayAgent' -Force
Start-Sleep -Seconds 2

Write-Host '== Deploying agent binary ==' -ForegroundColor Cyan
Copy-Item $agentSrc $agentDst -Force
Write-Host "   agent -> $agentDst"
# (hims-api.exe is built in place at $apiExe; the service path already points at it.)
Write-Host "   api   -> $apiExe (in place)"

Write-Host '== Starting services ==' -ForegroundColor Cyan
Start-Service 'HIMS API'
Start-Service 'HIMSRelayAgent'
Start-Sleep -Seconds 5

Write-Host '== Verify ==' -ForegroundColor Cyan
Get-Service 'HIMS API','HIMSRelayAgent' | Select-Object Name, Status | Format-Table -AutoSize
try {
  $h = Invoke-RestMethod -Uri 'http://127.0.0.1:8090/healthz' -TimeoutSec 5
  Write-Host "   /healthz: OK"
} catch { Write-Host "   /healthz: $($_.Exception.Message)" -ForegroundColor Yellow }
& $agentDst -version
Write-Host 'Done. Agent should heartbeat as 1.2.19 within ~30s; reconciler enqueues any never-attempted host within 5 min.' -ForegroundColor Green
