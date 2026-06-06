#!/usr/bin/env bash
# Stop, disable, and remove the HIMS API systemd service.
# Leaves the binary, database, and /etc/hims/hims-api.env in place by default.
# Pass --purge to also remove the binary, config dir, and service account.
# Usage: sudo ./uninstall-linux-service.sh [--purge]
set -euo pipefail

if [[ $EUID -ne 0 ]]; then echo "Run as root (sudo)." >&2; exit 1; fi

PURGE=false
[[ "${1:-}" == "--purge" ]] && PURGE=true

UNIT=/etc/systemd/system/hims-api.service

if systemctl list-unit-files | grep -q '^hims-api.service'; then
  echo "Stopping & disabling hims-api..."
  systemctl stop hims-api.service || true
  systemctl disable hims-api.service || true
fi
rm -f "$UNIT"
systemctl daemon-reload
echo "Service unit removed."

if $PURGE; then
  echo "Purging binary, config, and service account..."
  rm -f /opt/hims/hims-api
  rm -rf /etc/hims
  if id hims &>/dev/null; then userdel hims || true; fi
  echo "Purged. (Database was NOT touched.)"
else
  echo "Left /opt/hims/hims-api, /etc/hims/hims-api.env, and the database in place."
  echo "Re-add --purge to remove the binary, config, and 'hims' account."
fi
