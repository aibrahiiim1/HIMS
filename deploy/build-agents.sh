#!/usr/bin/env bash
# Stage the HIMS Relay Agent binaries the API serves in its installer packages.
# DEPLOYER/admin step (once per release) — NOT an operator action. Cross-compiles
# the agent for Windows + Linux into the dist dir the API reads
# (HIMS_AGENT_DIST_DIR, default ./dist/agents).
set -euo pipefail
DIST="${1:-$(pwd)/dist/agents}"
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mkdir -p "$DIST"
echo "Building HIMS Relay Agent binaries into $DIST"
cd "$REPO"
GOOS=windows GOARCH=amd64 go build -o "$DIST/hims-agent-windows-amd64.exe" ./cmd/hims-agent
echo "  [ok] windows/amd64"
GOOS=linux GOARCH=amd64 go build -o "$DIST/hims-agent-linux-amd64" ./cmd/hims-agent
echo "  [ok] linux/amd64"
echo "Done. Set HIMS_AGENT_DIST_DIR=$DIST for the API (or place these next to the API binary in an 'agents' folder)."
