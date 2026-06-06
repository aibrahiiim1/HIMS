#!/usr/bin/env bash
# Restart the HIMS API systemd service and confirm it came back healthy.
# Usage: sudo ./restart-linux-service.sh
set -euo pipefail

if [[ $EUID -ne 0 ]]; then echo "Run as root (sudo)." >&2; exit 1; fi

HEALTH_URL="${HIMS_HEALTH_URL:-http://localhost:8090/healthz}"

echo "Restarting hims-api..."
systemctl restart hims-api.service
sleep 2
systemctl --no-pager --full status hims-api.service || true

ok=false
for _ in $(seq 1 10); do
  if curl -fsS --max-time 3 "$HEALTH_URL" >/dev/null 2>&1; then ok=true; break; fi
  sleep 2
done
if $ok; then
  echo "Health OK ($HEALTH_URL)."
else
  echo "Health did not pass yet — check: journalctl -u hims-api -n 50"
fi
echo "Confirm encryption Enabled + DB connected in HIMS -> System Health."
