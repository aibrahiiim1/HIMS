# HIMS API — Integration Guide

The HIMS REST API is the same surface the web UI uses. A machine-readable
OpenAPI 3.0 document is generated from the live router and served at
`GET /api/v1/openapi.json`; the in-app **Administration → API Documentation**
page renders it grouped by resource. Because it's generated from the router, it
never drifts from the deployed endpoints.

## Basics

- **Base URL:** `/api/v1` (e.g. `http://hims-host:8090/api/v1`).
- **Content type:** `application/json` for request + response bodies.
- **Liveness:** `GET /healthz` → `{"status":"ok"}`.
- **Runtime/version:** `GET /api/v1/system/runtime`.

## Authentication & attribution

HIMS is an internal tool and does not require a bearer token today (deploy it on
a trusted management LAN / behind your reverse proxy). Mutating requests may
include an optional **`X-Actor`** header naming the operator; it is recorded on
the audit trail (`/api/v1/audit-log`). Without it, actions are attributed to
`operator`.

```
curl -X POST http://hims:8090/api/v1/devices \
  -H 'Content-Type: application/json' \
  -H 'X-Actor: alice' \
  -d '{"name":"sw-lobby","primary_ip":"10.0.0.10","category":"switch"}'
```

## Secrets

Credential secrets, notification-channel targets and device-config backups are
AES-256-GCM encrypted at rest and are **never returned** by the API. Credential
endpoints return metadata only; the one-time key reveal is the sole exception.
Writes that need encryption return `503` until `HIMS_ENCRYPTION_KEY` is set.

## Conventions

- List endpoints: `GET /api/v1/<resource>` (often with `?` filters, e.g.
  `/audit-log?category=credential&from=2026-01-01`, `/netflow/top-talkers?window=60`).
- Item endpoints: `GET/PATCH/DELETE /api/v1/<resource>/{id}` (id = UUID).
- Sub-resources: `GET /api/v1/devices/{id}/interfaces`, `/work-orders`, `/lifecycle`, …
- Exports stream files with `Content-Disposition` (reports `?format=xlsx|csv`,
  audit `/audit-log/export`, backup `/admin/backup/export`).

## Common examples

```
# Inventory (all categories)
curl http://hims:8090/api/v1/devices?category=all

# Per-site rollup
curl http://hims:8090/api/v1/sites/overview

# Open a work order
curl -X POST http://hims:8090/api/v1/work-orders \
  -H 'Content-Type: application/json' \
  -d '{"title":"AP offline","priority":"high","problem_type":"network"}'

# Export an Excel inventory report
curl -OJ 'http://hims:8090/api/v1/reports/inventory/export?format=xlsx'

# DR readiness
curl http://hims:8090/api/v1/admin/dr-readiness

# The full machine-readable spec
curl http://hims:8090/api/v1/openapi.json
```

## Generating clients

Feed `openapi.json` to any OpenAPI generator (openapi-generator, swagger-codegen,
oapi-codegen for Go, etc.) to produce typed clients in your language of choice.
