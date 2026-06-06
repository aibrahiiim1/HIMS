# HIMS API — Production Deployment

The HIMS API (`hims-api`) is a single static Go binary. It runs identically as a
foreground process, a **Windows service**, a **Linux systemd** service, or a
**Docker** container — only the supervisor differs. Every mode:

- starts on boot and **restarts on failure**
- loads `HIMS_ENCRYPTION_KEY`, `HIMS_DATABASE_URL`, `HIMS_ADDR`, `HIMS_AGENT_DIST_DIR`
- **refuses to start** if encrypted credentials already exist but the key is missing/invalid
- logs to a known location
- exposes `/healthz` and a runtime/health view in **System Health**

> The deployment is **not** Windows-only. Linux (systemd) and Docker are first-class.

## Configuration (all modes)

| Variable | Required | Purpose |
|---|---|---|
| `HIMS_ENCRYPTION_KEY` | yes (once any credential is stored) | base64 of 32 random bytes (`openssl rand -base64 32`). Keep an **off-box backup** — losing it makes stored credentials unrecoverable. |
| `HIMS_DATABASE_URL` | yes | PostgreSQL DSN |
| `HIMS_ADDR` | no (`:8090`) | listen host:port |
| `HIMS_AGENT_DIST_DIR` | no | dir of staged Relay Agent installer binaries (`deploy/build-agents.*`) |
| `HIMS_SERVICE_MODE` | auto | `windows-service` / `systemd` / `docker` / `foreground` (shown in System Health) |
| `HIMS_PUBLIC_URL` | no | external URL baked into generated agent installers |

### Encryption-key startup guard

On startup the API counts encrypted credentials in the DB. If that count is `> 0`
and no valid key is loaded, it **exits non-zero with a clear message** instead of
serving in a broken state. A fresh install (0 encrypted rows) starts normally so
you can set the key afterwards.

---

## Windows (native service — "HIMS API")

Scripts in `deploy/windows/` (run elevated). The binary implements the service
protocol directly (no NSSM).

```powershell
# Install + start (prompts for the key if -EncryptionKey is omitted)
./install-api-service.ps1 -ExePath C:\hims\hims-api.exe `
  -DatabaseUrl 'postgres://hims:hims@localhost:5433/hims?sslmode=disable' `
  -Addr ':8090' -AgentDistDir 'C:\hims\agents'

./restart-api-service.ps1      # after deploying a new binary
./uninstall-api-service.ps1    # stop + remove (keeps binary/DB/logs)
```

- Auto-start (delayed), auto-restart on failure (5s/10s/30s).
- Secrets live in the service's HKLM environment block (SYSTEM/Admin-readable only).
- Logs: `%ProgramData%\HIMS\API\logs\hims-api.log`.

## Linux (systemd)

Scripts + unit in `deploy/linux/` (run as root).

```bash
sudo ./install-linux-service.sh /path/to/hims-api /path/to/hims-migrate
sudoedit /etc/hims/hims-api.env       # set HIMS_ENCRYPTION_KEY + HIMS_DATABASE_URL
sudo ./restart-linux-service.sh
sudo ./uninstall-linux-service.sh [--purge]
```

- Unit: `hims-api.service` (Type=simple, `Restart=on-failure`, `WantedBy=multi-user.target`).
- Runs as an unprivileged `hims` account with systemd hardening.
- Config: `/etc/hims/hims-api.env` (root:hims `0640` — key not world-readable).
- Logs: `journalctl -u hims-api -f`.

## Docker / Compose

Files in `deploy/docker/`.

```bash
cd deploy/docker
cp .env.example .env                                   # edit POSTGRES_* etc.
printf '%s' "$(openssl rand -base64 32)" > hims_encryption_key.secret
chmod 600 hims_encryption_key.secret
docker compose up -d --build
```

- `restart: unless-stopped` → starts on boot + restarts on crash.
- The encryption key is a **Docker secret** (file at `/run/secrets/...`), never an
  env var in `docker inspect` or image layers; the entrypoint reads it indirectly.
- Migrations run at container start before the API binds.
- Volumes: `hims-pgdata` (DB), `hims-logs` (logs), `hims-agents` (staged installers).
- Healthcheck hits `/healthz`.

## Verify after any deploy (System Health)

Open **HIMS → System Health** (or `GET /api/v1/system/runtime`) and confirm:

- **service mode** matches how you deployed (`windows-service` / `systemd` / `docker`)
- **encryption** Enabled (key fingerprint matches stored)
- **database** connected
- **Relay Agent installer** available (stage with `deploy/build-agents.*` if not)
- uptime resets to ~0 and climbs (proves the restart took effect)
