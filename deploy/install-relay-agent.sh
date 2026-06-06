#!/usr/bin/env bash
# Install / run the HIMS Relay Agent (Site Collector) on a Linux host.
#
# The HIMS Relay Agent is the single official collector that replaces the old
# per-purpose helper scripts. Run it on one trusted machine inside a site. It
# registers with HIMS, polls for collection jobs, runs them locally, and posts
# structured results back. It authenticates with a per-agent token and never
# logs or stores secrets.
#
# On Linux the agent performs modern WinRM collection (pure-Go). WMI/DCOM
# collection requires the agent to run on Windows.
#
# Get the AGENT TOKEN once from HIMS -> Agents -> (create agent).
#
# Usage:
#   HIMS_URL=https://hims.example:8090 HIMS_AGENT_TOKEN=... ./install-relay-agent.sh [--service]
#
# Flags:
#   --service   install + enable a systemd unit (requires root) instead of
#               running in the foreground.
set -euo pipefail

SERVICE_NAME="hims-relay-agent"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_PATH="${HIMS_AGENT_BIN:-$SCRIPT_DIR/hims-agent}"
MODE="foreground"
[[ "${1:-}" == "--service" ]] && MODE="service"

: "${HIMS_URL:?set HIMS_URL (e.g. https://hims.example:8090)}"
: "${HIMS_AGENT_TOKEN:?set HIMS_AGENT_TOKEN (from the HIMS Agents page)}"
HIMS_AGENT_NAME="${HIMS_AGENT_NAME:-$(hostname)}"

if [[ ! -x "$BIN_PATH" ]]; then
  echo "hims-agent binary not found/executable at '$BIN_PATH'." >&2
  echo "Build it with: GOOS=linux go build -o hims-agent ./cmd/hims-agent" >&2
  echo "or download it from your HIMS Agents page, then set HIMS_AGENT_BIN." >&2
  exit 1
fi

echo "HIMS Relay Agent installer"
echo "  HIMS URL : $HIMS_URL"
echo "  Name     : $HIMS_AGENT_NAME"
echo "  Binary   : $BIN_PATH"
echo "  Mode     : $MODE"

if [[ "$MODE" == "foreground" ]]; then
  echo
  echo "Starting agent in the foreground (Ctrl+C to stop)..."
  exec env HIMS_URL="$HIMS_URL" HIMS_AGENT_TOKEN="$HIMS_AGENT_TOKEN" \
       HIMS_AGENT_NAME="$HIMS_AGENT_NAME" \
       ${HIMS_AGENT_INSECURE_TLS:+HIMS_AGENT_INSECURE_TLS="$HIMS_AGENT_INSECURE_TLS"} \
       "$BIN_PATH"
fi

# --- systemd service ---------------------------------------------------------
if [[ "$(id -u)" -ne 0 ]]; then
  echo "Installing as a service requires root (sudo)." >&2
  exit 1
fi

UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}.service"
ENV_PATH="/etc/${SERVICE_NAME}.env"

umask 077
cat > "$ENV_PATH" <<EOF
HIMS_URL=$HIMS_URL
HIMS_AGENT_TOKEN=$HIMS_AGENT_TOKEN
HIMS_AGENT_NAME=$HIMS_AGENT_NAME
${HIMS_AGENT_INSECURE_TLS:+HIMS_AGENT_INSECURE_TLS=$HIMS_AGENT_INSECURE_TLS}
EOF
chmod 600 "$ENV_PATH"

cat > "$UNIT_PATH" <<EOF
[Unit]
Description=HIMS Relay Agent / Site Collector
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=$ENV_PATH
ExecStart=$BIN_PATH
Restart=always
RestartSec=10
# Least-privilege hardening.
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now "$SERVICE_NAME"
echo
echo "Service '$SERVICE_NAME' started. Logs: journalctl -u $SERVICE_NAME -f"
echo "Check HIMS -> Agents; '$HIMS_AGENT_NAME' should appear Online within ~30s."
