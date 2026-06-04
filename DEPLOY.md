# HIMS — Deployment & Upgrade Runbook

HIMS runs as two Go binaries (`hims-api`, `hims-collector`) plus the built
web UI, against a PostgreSQL database. The API also bundles the in-process
monitoring / alerting / notifier / report-scheduler / NetFlow collector loops,
so a minimal deployment is just **Postgres + hims-api + static web assets**.

Database schema is owned by **`hims-migrate`** (migrations are embedded in the
binary — no loose SQL files needed at deploy time).

---

## 1. Prerequisites

- PostgreSQL 14+ reachable on `HIMS_DATABASE_URL`.
- A base64 32-byte encryption key (`HIMS_ENCRYPTION_KEY`). Generate one and store
  it in your secrets manager. **Back it up off-box** — credential secrets are
  AES-256-GCM encrypted with it and are unrecoverable if it is lost.
- Copy `.env.example` → `.env` and fill in the values.

## 2. Build

```
go build -o bin/hims-api      ./cmd/hims-api
go build -o bin/hims-collector ./cmd/hims-collector
go build -o bin/hims-migrate   ./cmd/hims-migrate
(cd web && npm ci && npm run build)   # → web/dist, served by your reverse proxy
```

## 3. Database migrations

```
export HIMS_DATABASE_URL=postgres://hims:hims@db:5432/hims?sslmode=disable

hims-migrate status     # list applied + pending migrations
hims-migrate up         # apply pending (idempotent; safe to re-run)
```

- **Fresh install:** `up` builds the entire schema from zero.
- **Existing database migrated by hand** (before `hims-migrate` existed): run
  `hims-migrate baseline` **once** to record the current migrations as applied
  without re-running them, then use `up` for everything after.

Migrations are tracked in the `schema_migrations` table.

## 4. Run

Dev:
```
powershell -File deploy/dev/run-api.ps1     # sets env + runs hims-api
```

Production (Windows service via NSSM):
```
powershell -File deploy/windows/migrate.ps1 -ExeDir C:\hims -Command up
powershell -File deploy/windows/install-service.ps1 -ExePath C:\hims\hims-api.exe
```
`install-service.ps1` runs `hims-migrate up` automatically when `hims-migrate.exe`
is present beside the API, prompts for the encryption key (hidden input, stored
only in the service env), and starts the service.

## 5. Upgrade

1. Build the new binaries + web assets.
2. `hims-migrate up` (applies any new migrations).
3. Replace the binaries and restart the service
   (`deploy/windows/restart-service.ps1`).

## 6. Backup & DR

- **Config snapshot** (inventory + config, no raw secrets): HIMS →
  Administration → Backup & Restore → *Download config backup*, or
  `GET /api/v1/admin/backup/export`.
- **Full database** (incl. encrypted secrets): `pg_dump` the HIMS database to
  off-site storage on a schedule, and record each run under Backup & Restore so
  DR readiness reflects it.
- **Encryption key:** stored in your secrets manager, backed up off-box. Without
  it, a restored database cannot decrypt any credential.
- Verify DR posture any time at Administration → Backup & Restore (checklist +
  last-backup age + key fingerprint).

## 7. Health checks

- `GET /healthz` — liveness.
- `GET /api/v1/system/runtime` — version, uptime, encryption status, loops.
- `GET /api/v1/admin/dr-readiness` — recovery readiness.
