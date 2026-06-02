# HIMS — Handover

> How to build, run, and continue HIMS. Read with `PLAN.md` (architecture)
> and `PROGRESS.md` (what's done).

## Repository
- Path: `D:\WebProjects\HIMS`
- Go module: `github.com/coralsearesorts/hims` (Go 1.26)
- Layout:
  ```
  cmd/hims-api/          API server (entrypoint)
  cmd/hims-collector/    discovery + monitoring collector (entrypoint)
  internal/domain/       entities + repository interfaces (no infra deps)
  internal/driver/       driver/plugin engine: Driver interface + registry
  internal/credresolver/ credential resolver logic (pure, unit-tested)
  internal/storage/postgres/  pgx repos + sqlc-generated db package
  internal/config/       configuration loading
  migrations/            SQL migrations (golang-migrate format)
  docs/                  PLAN / PROGRESS / HANDOVER / adr/
  ```

## Build / test
```
go build ./...
go vet ./...
go test ./...                  # fast, no DB required
go test -tags=integration ./...  # requires a throwaway Postgres
```
- `sqlc generate` regenerates `internal/storage/postgres/db` from
  `migrations/` + `internal/storage/postgres/queries/`. Run after any
  schema/query change. (sqlc parses the SQL offline — no DB needed.)

## Database
- PostgreSQL + TimescaleDB. Migrations are plain SQL `NNNNNN_name.up.sql` /
  `.down.sql`. Integration tests point at `HIMS_DATABASE_URL` (default
  `postgres://hims:hims@localhost:5432/hims?sslmode=disable`) and skip if
  unreachable. **Never run integration tests against production.**

## Architecture cheat-sheet
- **Generic core + drivers.** Add a vendor by writing a driver under
  `internal/driver/<vendor>` implementing the `Driver` interface and
  registering it — do NOT add vendor columns/tables. Vendor detail goes in
  `device_facts`.
- **Credential resolver.** The collector never picks a credential per
  device interactively; it asks the resolver for ordered candidates scoped
  by site/subnet/group and binds the first that authenticates.
- **Discovery vs monitoring.** Two cadences, one device model.

## Security invariants (carried from NIMS experience)
- Credentials encrypted at rest; never logged, never returned in API
  responses, never rendered in the UI.
- Discovery has **no** per-scan/per-device credential pickers — scoping is
  Site → Subnet → Credential Group.
- Probes are read-only; no config writes without an explicit, separate
  authorization path.

## How to continue
1. Read `PROGRESS.md` for the current phase and its open sub-tasks.
2. Work the next ⬜ item; keep `go build/vet/test` green at every commit.
3. Update `PROGRESS.md` when a sub-task or phase closes.
4. Record any architectural decision as a new `docs/adr/NNNN-*.md`.

## Lineage
HIMS is a greenfield rebuild informed by the prior **NIMS** project
(`D:\WebProjects\Inventory Go`). Engines proven there (staged discovery,
SNMP/CLI collection, NATS→Timescale metrics, source-scoped topology
collectors, credential groups, the FortiGate vendor driver) are the
reference for HIMS drivers/engines — re-implemented on a correct CMDB +
driver-engine foundation rather than ported wholesale.
