# dev-live.ps1 — DEV ONLY convenience watcher. Re-runs the real activation pipeline
# (activate-fixes.ps1) whenever tracked source changes, so a developer can edit and see
# the live :8090 service update without re-typing the command.
#
# ############################################################################
# #  DEV ONLY. This is NOT a deployment tool and NOT a hot-reloader.         #
# #  It simply calls scripts/activate-fixes.ps1 on a debounce when files     #
# #  change. It NEVER pushes. By default it is SAFE: it runs the FULL gate    #
# #  set (go build/vet/test + frontend build) before every restart, exactly  #
# #  like a manual activation, so untested code is never silently deployed.   #
# ############################################################################
#
# USAGE (elevated — each activation restarts the LocalSystem service):
#   powershell -ExecutionPolicy Bypass -File D:\WebProjects\HIMS\scripts\dev-live.ps1
#
#   -Unsafe            fast dev loop: pass -SkipTests to activation (build+vet still run,
#                      go test is skipped). Prints a loud warning each cycle. NOT for deploys.
#   -IntervalSeconds   poll interval (default 3)
#   -Frontend          force a web/dist rebuild every cycle
#
# It builds the WORKING TREE (uncommitted code) via activation's -AllowDirty, because the
# whole point of a dev loop is iterating on uncommitted changes. The built binary is stamped
# with HEAD's commit (approximate in a dirty tree) — commit before a real activation so the
# running commit is exact. See scripts/activate-fixes.ps1 (the single source of truth).

[CmdletBinding()]
param(
  [switch]$Unsafe,
  [int]$IntervalSeconds = 3,
  [switch]$Frontend
)

$ErrorActionPreference = 'Stop'
$repo    = 'D:\WebProjects\HIMS'
$activate = Join-Path $repo 'scripts\activate-fixes.ps1'
$watch   = @(
  (Join-Path $repo 'internal'),
  (Join-Path $repo 'cmd'),
  (Join-Path $repo 'migrations'),
  (Join-Path $repo 'web\src')
)

Write-Host '############################################################' -ForegroundColor Magenta
Write-Host '#  dev-live.ps1 — DEV ONLY. Watches source, re-activates.  #' -ForegroundColor Magenta
Write-Host (("#  Mode: {0}" -f $(if ($Unsafe) { 'UNSAFE (go test SKIPPED)' } else { 'SAFE (full gates each cycle)' })).PadRight(59) + '#') -ForegroundColor Magenta
Write-Host '#  Never pushes. Ctrl+C to stop.                           #' -ForegroundColor Magenta
Write-Host '############################################################' -ForegroundColor Magenta
if ($Unsafe) { Write-Host 'WARNING: -Unsafe skips go test. Do NOT use this to deploy code you have not tested.' -ForegroundColor Yellow }

# A cheap fingerprint of the watched tree: newest LastWriteTime across source files.
function Get-Fingerprint {
  $newest = [datetime]'1970-01-01'
  foreach ($d in $watch) {
    Get-ChildItem -Path $d -Recurse -File -ErrorAction SilentlyContinue |
      Where-Object { $_.Extension -in '.go','.ts','.tsx','.css','.sql','.html' } |
      ForEach-Object { if ($_.LastWriteTime -gt $newest) { $newest = $_.LastWriteTime } }
  }
  return $newest.Ticks
}

# Build the argument list forwarded to the single-source-of-truth activation script.
$fwd = @('-AllowDirty')
if ($Unsafe)   { $fwd += '-SkipTests' }
if ($Frontend) { $fwd += '-Frontend' }

Write-Host "Watching: $($watch -join ', ')" -ForegroundColor Cyan
Write-Host "Forwarding to activate-fixes.ps1: $($fwd -join ' ')" -ForegroundColor Cyan

$last = Get-Fingerprint
Write-Host 'Initial activation…' -ForegroundColor Cyan
& $activate @fwd
Write-Host "(activation exit code: $LASTEXITCODE)" -ForegroundColor DarkGray

while ($true) {
  Start-Sleep -Seconds $IntervalSeconds
  $now = Get-Fingerprint
  if ($now -ne $last) {
    # Debounce: wait for the tree to settle (no further change for one interval) so a burst
    # of saves triggers a single activation, not one per file.
    do { $prev = $now; Start-Sleep -Seconds $IntervalSeconds; $now = Get-Fingerprint } while ($now -ne $prev)
    $last = $now
    Write-Host ''
    Write-Host "== change detected — re-activating ($(if ($Unsafe) {'UNSAFE'} else {'SAFE'})) ==" -ForegroundColor Magenta
    try { & $activate @fwd; Write-Host "(activation exit code: $LASTEXITCODE)" -ForegroundColor DarkGray }
    catch { Write-Host "activation error: $($_.Exception.Message)" -ForegroundColor Red }
  }
}
