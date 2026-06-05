# HIMS — Project Rules

## Completeness rule (no deferral of required functionality)

Do not defer missing functionality just because it is large or requires multiple
commits. If something is required for the product to work correctly, implement it
now in safe staged commits until it is complete.

Do not say "future follow-up", "later", or "not needed now" UNLESS it is
genuinely blocked by one of:

- missing real credentials
- missing vendor / device access
- missing operator decision
- unavailable hardware
- unavailable external service
- a safety / security risk

If something is blocked, still implement everything possible around the gate:

- detection
- UI placeholder with an honest gate
- documentation
- a Data Quality issue
- a next-action flow
- acceptance criteria

Every feature is completed as much as technically possible, verified live where
possible, and never left half-done. Mark something "gated" only for the blockers
above — and even then build the detection, UI, docs, and next-action around it.

## Working conventions (observed in this repo)

- Backend: Go, chi/v5, sqlc (pgx/v5). Migrations in `migrations/` (`*.up.sql`
  only, sequential `NNNNNN_name.up.sql`); apply with `go run ./cmd/hims-migrate`.
  Regenerate after query/schema changes with `sqlc generate`.
- Every commit compiles + `go vet` clean; fix vet/eslint in dedicated commits.
- Secrets (passwords / SNMP communities / tokens) are NEVER stored, logged, or
  returned — only encrypted at rest (AES-256-GCM) and decrypted in-memory to use.
- Credentials are bound to a device ONLY on a successful authentication. Open
  ports are classification hints, never "managed access".
- Preserve manual classification locks. Respect RBAC + site-scope.
- Dev API: `bin/hims-api.exe`, key from gitignored `bin/dev-encryption-key`,
  Postgres on `localhost:5433` (container `hims-pg`), listens `:8090`.

## Discovery scan onboarding — protocol coverage + honest gates

A scan detects all classes (broad ports + safe evidence), classifies with
confidence, tries the right credentials, binds ONLY on auth success, persists
every attempt to credential-test history, and shows a per-device next-action.

Authenticated-in-scan today: SNMP (switch/router/firewall/printer/UPS), WinRM
(Windows), SSH (Linux), ONVIF (camera/NVR/DVR), vSphere (ESXi virtual_host).

Config-gated (NOT a silent defer — detection + classification + next-action +
the Controllers-import path are all built): **wireless controllers** and **CUCM
voice** require vendor-specific connection parameters a `user:password`
credential can't carry — UniFi `site`, Omada `controller-id`, Ruckus `apiBase`,
CUCM AXL `schema version` + service-account URL. These onboard via
`Discovery → Controllers import` (POST /discovery/controller-import) where the
operator supplies that config; the scan classifies them as candidates and points
there. Lifting this gate = letting the scan carry those params (operator config),
not a missing capability.
