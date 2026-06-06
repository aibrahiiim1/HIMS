#!/usr/bin/env bash
# Install hims-api as a systemd service on Linux.
#
# - creates an unprivileged 'hims' service account
# - installs the binary to /opt/hims/hims-api
# - installs /etc/hims/hims-api.env (from the example) if absent
# - installs + enables the systemd unit (start on boot, restart on failure)
#
# Usage (run as root):
#   sudo ./install-linux-service.sh /path/to/hims-api [/path/to/hims-migrate]
set -euo pipefail

if [[ $EUID -ne 0 ]]; then
  echo "Run as root (sudo)." >&2; exit 1
fi

API_BIN="${1:-}"
MIGRATE_BIN="${2:-}"
if [[ -z "$API_BIN" || ! -f "$API_BIN" ]]; then
  echo "Usage: sudo $0 /path/to/hims-api [/path/to/hims-migrate]" >&2; exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
INSTALL_DIR=/opt/hims
ENV_DIR=/etc/hims
UNIT=/etc/systemd/system/hims-api.service

# 1. Service account (no login, no home).
if ! id hims &>/dev/null; then
  echo "Creating service account 'hims'..."
  useradd --system --no-create-home --shell /usr/sbin/nologin hims
fi

# 2. Binary.
echo "Installing binary to $INSTALL_DIR/hims-api ..."
install -d -o root -g root "$INSTALL_DIR"
install -o root -g root -m 0755 "$API_BIN" "$INSTALL_DIR/hims-api"

# 3. Config (don't clobber an existing filled-in env file).
install -d -o root -g hims -m 0750 "$ENV_DIR"
if [[ ! -f "$ENV_DIR/hims-api.env" ]]; then
  echo "Installing example env to $ENV_DIR/hims-api.env (EDIT IT before starting!)"
  install -o root -g hims -m 0640 "$SCRIPT_DIR/hims-api.env.example" "$ENV_DIR/hims-api.env"
else
  echo "Keeping existing $ENV_DIR/hims-api.env"
fi

# 4. Optional migrations.
if [[ -n "$MIGRATE_BIN" && -f "$MIGRATE_BIN" ]]; then
  echo "Applying database migrations..."
  # shellcheck disable=SC1091
  set -a; source "$ENV_DIR/hims-api.env"; set +a
  "$MIGRATE_BIN" up
fi

# 5. Unit.
echo "Installing systemd unit -> $UNIT"
install -o root -g root -m 0644 "$SCRIPT_DIR/hims-api.service" "$UNIT"
systemctl daemon-reload
systemctl enable hims-api.service
systemctl restart hims-api.service
sleep 2
systemctl --no-pager --full status hims-api.service || true

echo
echo "Installed. Logs: journalctl -u hims-api -f"
echo "If you just created the env file, edit $ENV_DIR/hims-api.env (set HIMS_ENCRYPTION_KEY + HIMS_DATABASE_URL) then: sudo systemctl restart hims-api"
echo "Verify in HIMS -> System Health: encryption Enabled, DB connected, service mode = systemd."
