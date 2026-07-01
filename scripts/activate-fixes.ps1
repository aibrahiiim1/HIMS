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

# Rebuild hims-api.exe in place from the current source. The service holds a lock on the exe
# while running, so this MUST happen after Stop-Service (above) and before Start-Service (below);
# building here guarantees the started service carries the latest committed code, not a stale binary.
Write-Host '== Building hims-api.exe ==' -ForegroundColor Cyan
$commit = (& git -C $repo rev-parse --short HEAD).Trim()
Push-Location $repo
try {
  & go build -o $apiExe ./cmd/hims-api
  if ($LASTEXITCODE -ne 0) { throw "go build failed (exit $LASTEXITCODE)" }
} finally { Pop-Location }
Write-Host "   api   -> $apiExe (rebuilt at $commit)"

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
# Confirm the freshly-started service is running the code we just built (the on-disk binary and
# the in-memory service image can differ if the restart was skipped). The log stamps the commit.
$logCommit = (Select-String -Path 'C:\ProgramData\HIMS\API\logs\hims-api.log' -Pattern '"commit":"([0-9a-f]+)"' -ErrorAction SilentlyContinue | Select-Object -Last 1).Matches.Groups[1].Value
if ($logCommit) {
  if ($logCommit -like "$commit*") { Write-Host "   running commit: $logCommit (matches build ✓)" -ForegroundColor Green }
  else { Write-Host "   running commit: $logCommit but built $commit — service may not have restarted" -ForegroundColor Yellow }
}
& $agentDst -version
Write-Host 'Done. Agent should heartbeat as 1.2.19 within ~30s; reconciler enqueues any never-attempted host within 5 min.' -ForegroundColor Green
