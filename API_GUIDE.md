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

## Authentication & sessions

HIMS uses **server-side sessions** behind a username/password login — there is
no bearer token. Authenticate, keep the session cookie, and send it on every
request.

1. **Log in:** `POST /api/v1/auth/login` with `{"username","password"}`. On
   success the server sets an **httpOnly** cookie `hims_session` (12h TTL,
   `SameSite=Lax`) and returns your identity (username, permissions, site, admin
   flag). The stored secret is only a `sha256` of the token — the raw token
   lives in the cookie.
2. **Send the cookie** on subsequent calls. Browsers do this automatically; for
   `curl`, use a cookie jar (`-c`/`-b`).
3. **Who am I:** `GET /api/v1/auth/me`. **Log out:** `POST /api/v1/auth/logout`
   (invalidates the session). **Change own password:** `POST /api/v1/auth/password`.

```
# Log in (store the session cookie in cookies.txt)
curl -c cookies.txt -X POST http://hims:8090/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"alice","password":"••••••"}'

# Reuse the cookie on every call
curl -b cookies.txt -X POST http://hims:8090/api/v1/devices \
  -H 'Content-Type: application/json' \
  -d '{"name":"sw-lobby","primary_ip":"10.0.0.10","category":"switch"}'
```

The first admin is created from `HIMS_ADMIN_USER` / `HIMS_ADMIN_PASSWORD` at
startup; admins onboard other users and set passwords via `POST
/api/v1/rbac/users/{id}/password`. Deploy behind your reverse proxy / TLS —
the cookie is httpOnly but not `Secure`, so terminate TLS at the proxy.

### Authorization (RBAC + site scope)

Every endpoint is permission-gated and the policy is enforced server-side:

- **Unauthenticated** request to a protected route → `401` (`authentication required`).
- **Authenticated but missing the required permission** → `403`
  (`forbidden: requires permission <code>`). Admins (holding `rbac.manage`)
  bypass permission checks. Permission codes are read/write-split per resource
  (e.g. `devices.read`/`devices.write`, `credentials.manage`, `discovery.run`,
  `reports.view`/`reports.schedule`, `audit.read`).
- **Site scope** — a user pinned to a site (`location_id`) only sees and acts on
  devices within that site's location subtree; cross-site device access →
  `403 forbidden: device is outside your site`. Global users (no site) see all.

`GET /api/v1/auth/me` returns your `permissions`, `site_id` and `admin` flag so a
client can hide controls it can't use (the server still enforces).

### Actor attribution

Mutations are attributed on the audit trail (`/api/v1/audit-log`) to the
**authenticated** user automatically. The legacy **`X-Actor`** header is only a
**fallback** used when no authenticated identity is present — i.e. in *open mode*
(see below) or local/dev calls; authenticated requests ignore it. With neither,
actions are attributed to `operator`.

### Open mode (first-run / dev)

Until the first password exists (no admin bootstrapped yet), HIMS runs in
**open mode**: the auth middleware does not enforce, so the API is usable to get
set up. As soon as a password is set (bootstrap admin or any user), enforcement
turns on for all subsequent requests. `GET /api/v1/auth/me` reports
`auth_active` so you can tell which mode a deployment is in. Run production with
a bootstrapped admin so enforcement is active.

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

When enforcement is active, send the session cookie (`-b cookies.txt`) obtained
from `/auth/login`. `GET /healthz` and `GET /api/v1/openapi.json` need no auth.

```
# Inventory (all categories)
curl -b cookies.txt http://hims:8090/api/v1/devices?category=all

# Per-site rollup
curl -b cookies.txt http://hims:8090/api/v1/sites/overview

# Open a work order
curl -b cookies.txt -X POST http://hims:8090/api/v1/work-orders \
  -H 'Content-Type: application/json' \
  -d '{"title":"AP offline","priority":"high","problem_type":"network"}'

# Export an Excel inventory report
curl -b cookies.txt -OJ 'http://hims:8090/api/v1/reports/inventory/export?format=xlsx'

# DR readiness
curl -b cookies.txt http://hims:8090/api/v1/admin/dr-readiness

# The full machine-readable spec (no auth required)
curl http://hims:8090/api/v1/openapi.json
```

## Generating clients

Feed `openapi.json` to any OpenAPI generator (openapi-generator, swagger-codegen,
oapi-codegen for Go, etc.) to produce typed clients in your language of choice.
