# HIMS — Full System Handover (2026-06-18)

> Authoritative, self-contained handover for an engineer/AI taking over HIMS. Read this
> top to bottom once. It captures the architecture, the data + state model, the discovery/
> collection pipeline (where most recent work happened), how to build/test/deploy, the
> environment gotchas that will bite you, the recent fix chain and *why*, the current
> in-flight acceptance test, and what to do next.
>
> Companion docs: `docs/RUNBOOK.md` (per-collector live-validation procedures),
> `docs/PROGRESS.md` (phase-by-phase build log), `docs/adr/` (architecture decisions),
> `docs/windows-remote-management-gpo.md` (Windows WinRM/WMI GPO checklist).

---

## 0. What HIMS is

**HIMS** (Hotel Infrastructure Management System) is an on-prem network/asset discovery,
inventory, monitoring and operations platform for a multi-hotel network (Coral Sea
Resorts). It discovers devices on the network, authenticates to them with stored
credentials over the right protocol per device class, collects deep inventory, tracks
two independent axes — **Reachability** (online/offline) and **Management** (can HIMS
authenticate + collect) — and surfaces it all in a React dashboard plus operations
features (work orders, monitoring, alerting, topology, reporting).

It is the successor/sibling to an earlier system ("NIMS"); several invariants are carried
over (see §13).

---

## 1. ⚠️ Environment gotchas — READ FIRST (these will waste hours otherwise)

1. **The real repository is `D:\WebProjects\HIMS`.** A separate, older, unrelated repo
   lives at `D:\WebProjects\Inventory Go`, and shells often default their cwd there.
   `Set-Location`/`cd` does **not** reliably persist between tool calls. **Always target
   HIMS explicitly**: `git -C 'D:\WebProjects\HIMS' …`, `go -C 'D:\WebProjects\HIMS' …`,
   `npm --prefix 'D:\WebProjects\HIMS\web' …`. Verify cwd/branch before any build/commit.

2. **Never `git add -A`.** The working tree carries untracked scratch that must not be
   committed: `.claude/`, `.mib-stage/`, `dist/`, `hims.docx`, `.sess*`, `bin/*.new.exe`,
   `.deploy-result.txt`, scratch under `D:\tmp`. Always `git add <explicit paths>`.

3. **Postgres runs in Docker, container `hims-pg`, host port `5433`** (NOT 5432). DB
   `hims`, user `hims`, password `hims`. The API's hard-coded default DSN in
   `cmd/hims-api/main.go` says `:5432` — the real DSN is supplied via the
   `HIMS_DATABASE_URL` env var = `postgres://hims:hims@localhost:5433/hims?sslmode=disable`.
   Query the DB directly with: `docker exec hims-pg psql -U hims -d hims -c "…"`.

4. **The API does NOT auto-migrate.** Migrations run only via the deploy script
   (`.deploy-restart.ps1`). See §4.

5. **Production UI is the API Windows service serving the built SPA** from `web/dist` on
   `http://localhost:8090`. It is NOT `npm run dev`. A dev server you spawn dies with your
   session → the user gets "Failed to fetch". Durable changes must be built + deployed.

6. **Deploys are admin-gated (UAC).** Swapping the service exe + running migrations needs
   elevation. Use the elevated scripts in §4.

7. **DB stores timestamps in UTC.** Local time ≈ UTC+3. Don't filter with literal local
   clock strings; use `now() - interval '…'`.

---

## 2. Architecture

```
                         ┌─────────────────────────────────────────┐
   Operator browser ───► │  HIMS API  (Go, chi router, :8090)        │
                         │  - serves built React SPA from web/dist   │
                         │  - REST API  /api/v1/*  (311 routes)       │
                         │  - discovery/scan orchestrator            │
                         │  - collectors (SNMP/SSH/WinRM/WMI/HTTP/…)  │
                         │  - monitoring loop, alerting, reporting    │
                         │  - relay-agent job queue + protocol        │
                         │  Windows service name: "HIMS API"          │
                         └───────────────┬───────────────────────────┘
                                         │ pgx / sqlc
                         ┌───────────────▼───────────────┐
                         │  Postgres  (docker hims-pg :5433) │
                         └───────────────▲───────────────┘
                                         │ HTTP (poll jobs / post results)
                         ┌───────────────┴───────────────────────────┐
                         │  HIMS Relay Agent (Go, Windows service)    │
                         │  "HIMSRelayAgent"                          │
                         │  - in-domain identity; collects Windows    │
                         │    hosts via WinRM-shell + WMI/DCOM         │
                         │  - pulls collect_os jobs, posts results     │
                         └────────────────────────────────────────────┘
```

- **Why the relay agent exists**: the API service runs as **LocalSystem** (no domain
  identity), so direct WinRM/WMI to domain Windows hosts fails auth even with correct
  stored creds. The relay agent runs *inside* the domain network (often as a domain or
  local-admin context) and is the real path to Windows hosts. On the current dev box
  (`CHV-MISMGR` / 172.21.60.20) the API, Postgres, and the agent all run on the same
  machine, so the agent is locally redeployable.

- **Module**: `github.com/coralsearesorts/hims`, Go 1.26.

### Binaries (`cmd/`)
| Binary | Purpose |
|---|---|
| `hims-api` | The API + SPA server (main service). |
| `hims-agent` | Relay agent (Windows service `HIMSRelayAgent`). |
| `hims-migrate` | Applies `migrations/NNNNNN_*.up.sql` (golang-migrate format). Built fresh by the deploy script. |
| `hims-collector` | Standalone/aux collector entrypoint. |
| `isapi-probe` | Scratch/diagnostic tool for Hikvision ISAPI (CCTV). |

### Backend packages (`internal/`) — the important ones
| Package | Role |
|---|---|
| `api` | HTTP handlers, routing (`server.go`), the **management-state model** (`device_status.go`, `access_coverage.go`), discovery orchestration (`discovery.go`), relay-agent server side (`relay_agents.go`, `agent_routing.go`, `agent_job_reaper.go`), per-collector wiring (`os_inventory.go`, `cctv_collect.go`, `vsphere_collect.go`, …), reports, credtest. **This is where you'll spend most time.** |
| `osinv` | OS inventory collectors + WinRM client + error classification (`winrm.go`, `windows.go`, `linux.go`, `persist.go`, `scrub.go`). |
| `discovery` | Scan pipeline primitives: target parsing (IP/range/CIDR/multi), host probing, classification inputs, cred attempts. |
| `scan` | Scan engine internals. |
| `apply` | Discovery→CMDB reconcile/apply worker (writes discovered devices, preserves classification). |
| `credresolver` | Resolves which credentials to try for a device (per class, per site-subnet exclusivity). |
| `credtest` | Credential-test categories + persistence helpers. |
| `domain` | Core domain types + credential-kind constants. |
| `driver` / `drivers` | SNMP driver engine + vendor drivers (Aruba/HPE, Cisco IOS, Huawei VRP, FortiGate, …). |
| `snmp`, `ssh`, `redfish`, `vsphere`, `hyperv`, `onvif`, `isapi`, `unifi`, `omada`, `ruckus`/`ruckuszd`, `extreme`/`extremexcc`, `cucm`, `omnipcx` | Protocol/vendor collectors. |
| `mibpack`, `mibparse`, `mibs` | MIB-upload engine (custom SNMP wireless collection). |
| `monitoring` | Reachability checks + SNMP-metric sampling loop. |
| `alerting`, `operations`, `topology`, `reports`, `netflow`, `adimport`, `classify`, `fingerprint` | Self-describing. |
| `secret` | Credential encryption (key startup-guarded). |
| `storage/postgres` | sqlc: `queries/*.sql` (source), `db/*.go` (generated). |
| `config`, `auth`, `backup`, `notify`, `collect`, `migrate`, `osdiscovery` | Support. |

### Frontend (`web/`)
- Vite + React + TypeScript. `web/src/pages/` (~61 pages), `web/src/components/`,
  `web/src/nav.tsx` (sidebar), `web/src/api.ts` (typed API client), `web/src/lib/`.
- Built with `npm --prefix web run build` → emits `web/dist`, which the API serves.
- Notable pages for the recent work: `Inventory`, `Discovery`, `ScanJobs`,
  `ScanJobResults`, `UnmanagedDevices`, `MissingClassification`, `Agents`,
  `components/ConnectivityReport.tsx`, the Data Quality surface.

---

## 3. Build / test / deploy

### Build (from `D:\WebProjects\HIMS`)
```powershell
# API (host-native is fine for dev; GOOS=windows for the service exe)
$env:GOOS='windows'; $env:GOARCH='amd64'
go -C 'D:\WebProjects\HIMS' build -o 'D:\WebProjects\HIMS\bin\hims-api.new.exe'   ./cmd/hims-api
go -C 'D:\WebProjects\HIMS' build -o 'D:\WebProjects\HIMS\bin\hims-agent.new.exe' ./cmd/hims-agent
# Frontend
npm --prefix 'D:\WebProjects\HIMS\web' run build      # -> web/dist
```

### Test / vet / lint
```powershell
go -C 'D:\WebProjects\HIMS' vet ./...
go -C 'D:\WebProjects\HIMS' test ./...           # API tests are PURE (no DB needed)
npm --prefix 'D:\WebProjects\HIMS\web' run lint
npm --prefix 'D:\WebProjects\HIMS\web' run build # tsc typecheck happens here
```
A **pre-commit hook** runs `gofmt -w` on staged Go files (it may reformat comments).

### sqlc regeneration (when you change `internal/storage/postgres/queries/*.sql`)
```powershell
cd 'D:\WebProjects\HIMS'; sqlc generate     # sqlc v1.27.0; config = sqlc.yaml
```
The query SQL is embedded as a Go string constant in `internal/storage/postgres/db/*.go`,
so **you MUST regenerate** for a `.sql` change to take effect, even if the column shape is
unchanged.

### Deploy the API (elevated — UAC prompt; runs migrations THEN swaps + restarts)
```powershell
# after building bin/hims-api.new.exe
Start-Process powershell -Verb RunAs -Wait -ArgumentList `
  '-ExecutionPolicy','Bypass','-File','D:\WebProjects\HIMS\.deploy-restart.ps1', `
  '-DbUrl','postgres://hims:hims@localhost:5433/hims?sslmode=disable'
# result written to D:\WebProjects\HIMS\.deploy-result.txt  ("OK … :: migrated+swapped")
```
`.deploy-restart.ps1` = the canonical deploy: resolve DSN → build fresh `hims-migrate` →
print migration status → `migrate up` (STOP on failure, no swap) → stop "HIMS API" service
→ swap `hims-api.new.exe`→`hims-api.exe` (rollback on start failure) → start service.
`-DryRun` = status only, no changes, no admin. `-RunSeed` = also POST the
vendor-fingerprint seed after health check.

### Deploy the relay agent (elevated — swap exe + restart service)
```powershell
# after building bin/hims-agent.new.exe ; D:\tmp\deploy-agent.ps1 stops the service,
# backs up + swaps C:\Program Files\HIMS Relay Agent\hims-agent.exe, restarts, prints version.
Start-Process powershell -Verb RunAs -Wait -ArgumentList `
  '-NoProfile','-ExecutionPolicy','Bypass','-File','D:\tmp\deploy-agent.ps1'
```
(If `D:\tmp\deploy-agent.ps1` is missing, recreate it: Stop-Service HIMSRelayAgent →
Copy-Item bin/hims-agent.new.exe over the Program Files exe → Start-Service → verify
`& '…\hims-agent.exe' -version`.)

### Bring-up from scratch — see `docs/RUNBOOK.md` §14.

---

## 4. Data model essentials

All tables in Postgres `hims`. Key ones for the recent work:

- **`devices`** — the CMDB. `primary_ip inet`, `category` (endpoint/server/switch/router/
  firewall/camera/nvr/printer/ups/wireless_controller/…), `os_family`, `vendor`,
  `status` (up/down/warning/needs_attention), `credential_id` (bound cred), `location_id`
  (site/location-tree node), `confidence_score`, `classification_locked`, `is_virtual`,
  `deleted_at`. **Unique index on IP**; reconcile-by-IP on re-scan. Hard delete cascades
  to inventory child rows (`DeleteDevice` query).
- **`credentials`** — encrypted secrets, `kind` (snmp_v2c/snmp_v3/ssh/winrm/wmi/http_basic/
  api_token/cli/…). Encryption key is startup-guarded (`internal/secret`).
- **`os_inventory`** — deep Windows/Linux inventory. **`collection_method`** is critical:
  `winrm` (direct), `winrm-agent` (relay-agent WinRM shell), `winrm-native`, `wmi`, `ssh`.
  A row exists **only on a successful authenticated collection** → it is durable PROOF of
  management (see §6).
- **`credential_test_results`** — every credential attempt (success + failure) with
  `kind`, `category`, `success`, `tested_at`. Append-only (never deleted by a re-test).
  Latest-per-(device,kind) drives failure reasons; ANY success ever = durable proof.
- **`agent_jobs`** — relay-agent work queue. `kind` (`collect_os`, `test`), `status`
  (queued/dispatched/done/failed), `protocol`, `target`, `attempt`, `max_attempts`,
  `next_attempt_at`, `category`, `error`, `request`, `result`. Retry-waiting = a
  `queued` row with `next_attempt_at` in the future. Migration `000080` added the retry cols.
- **`relay_agents`** — registered agents (name, status, version, last_heartbeat, location_id).
- **`discovery_jobs`** — scan runs (status, started/finished, found_count, scanned_count).
- Location tree (sites→buildings→…→rooms), **subnet credentials** (per-Site-subnet
  exclusive credential sets — anti-spray/lockout), camera_info/nvr_info, wlan_controller_info,
  virtual_machines, bmc_info, firewall_status, ups_status, printer_supplies, interfaces,
  pbx_phones, monitoring checks/samples, alerts, work orders, fingerprints, MIB packs.

---

## 5. The discovery → collection pipeline (end-to-end)

This is the operational heart and the focus of all recent fixes.

```
Operator: POST /api/v1/discovery/scan { mode:"targets", targets:"172.21.60.0/24",
                                        credential_ids:[] }   ← [] means "try ALL stored creds"
   │
   ├─ resolveScanHosts: parse targets (IP / a-b range / CIDR / hostname / lists)
   ├─ for each host (concurrent, capped):
   │    ├─ aliveness probe (ping / TCP)
   │    ├─ port/banner probe → classification inputs
   │    ├─ credential ladder per device class (credresolver) — auto-tries all applicable
   │    │   stored creds; subnet-scoped creds are EXCLUSIVE to their subnet (no spray)
   │    ├─ classify (fingerprint + heuristics) → category/vendor/os_family
   │    └─ apply.reconcile → upsert device (match by IP; preserve operator classification)
   │
   ├─ deep collection per class:
   │    • SNMP devices (switch/router/firewall/printer/ups): walked inline by the API
   │    • Linux (ssh), camera/nvr (onvif/isapi), wireless (REST/SNMP/SSH),
   │      vSphere (govmomi), BMC (redfish): collected inline by the API
   │    • Windows hosts: routed to the RELAY AGENT (LocalSystem can't auth domain) →
   │      enqueue an agent_jobs collect_os job (agent_routing.go)
   │
   └─ discovery_jobs.finished_at set when the PROBE phase completes.
        Collection (esp. agent jobs) DRAINS AFTERWARD — discovery "complete" ≠ collection done.
```

**Two distinct completion phases** (don't conflate): discovery/probe completion vs
collection-queue drain. The job detail exposes a `collection` object
(`queued/dispatched/running/done/failed/retry_waiting/settled`) via `CollectionProgressForJob`;
the UI shows "Discovery complete · collecting N" until the queue settles.

### The relay agent collection path (`cmd/hims-agent/main.go`)
The server hands the agent a `collect_os` job with the **ordered candidate credential
list** (`agentJobOut.Credentials`). The agent (`runJob`):
1. Tries each candidate credential in order; **stops at the first success**.
2. For Windows it runs a **two-rung ladder** (`collectWindows`):
   - **Rung 1 — WinRM command shell** (`osinv.CollectWindows` over Go-winrm). Works for
     **domain AND local-admin** accounts because its `Get-CimInstance`/registry reads run
     *locally on the target inside the shell*. Writes `collection_method='winrm-agent'`.
   - **Rung 2 — WMI/DCOM** (PowerShell `Get-WmiObject`, then `New-CimSession` over WSMan).
     Fallback for hosts with WinRM disabled but RPC open. Writes `collection_method='wmi'`.
   - **Short-circuits** (does NOT fall to WMI) on: `auth_failed` (same cred fails WMI too
     → avoid lockout), and transient WinRM transport categories `winrm_connect_timeout` /
     `winrm_negotiate_error` (a pre-auth transport blip is not credential-specific — retry
     WinRM rather than run a WMI logon for every candidate and risk domain-account lockout).
   - On a transient transport category, `runJob` also **breaks the candidate loop** (stop
     hammering an overloaded/unreachable WinRM listener with every cred).
3. Records EVERY attempt (success/failure + non-secret category) and posts the winner +
   attempts. Never logs secrets.

Server side (`relay_agents.go`): `agentJobResult` persists attempts via
`persistScanCredAttempts`, binds the winning credential, and — for a **transient/retryable**
failure category and `attempt+1 < max_attempts` — **requeues** the job with backoff
(`agentRetryBackoff`: 30s / 2m / 5m, `max_attempts` default 3). `agent_job_reaper.go`
requeues stale `dispatched` jobs (>8m). Caps are configurable via env:
`HIMS_AGENT_DISPATCH_CAP` (server, default 8, clamp 1–64) and `HIMS_AGENT_MAX_CONCURRENT`
(agent worker pool, default 8, clamp 1–32).

---

## 6. The management-state model (MOST IMPORTANT — read carefully)

Two **independent** axes, never conflated (`internal/api/device_status.go`):

- **Reachability** (`reachabilityFromStatus`): online / offline / warning / unknown — from
  monitoring `device.status`. "Has an open port" is **never** management.
- **Management** (`deriveManagement`): can HIMS authenticate + collect. Derived purely from
  **proven evidence**, never from open ports or bound-but-untested creds.

### Management states (constants in `device_status.go`)
`managed`, `partially_managed`, `unmanaged`, `needs_credential`, `credential_failed`,
`needs_agent`, `agent_offline`, `collection_failed`, `pending_collection` (an agent job is
queued/dispatched/retry-waiting *right now*), `not_attempted` (reachable Windows-like host
that was never even attempted — an enqueue gap; should be 0 after a settled from-zero),
`virtual`.

### `deriveManagement(device)` precedence (the order MATTERS)
1. **`access.hasProven()` → `managed`.** Proven = collection evidence OR a successful
   credential test. **This wins over everything below.** A later failed attempt must never
   override proven success.
2. NVR-channel camera (managed via the recorder) → `managed`.
3. `activeCollect` (agent job in flight, incl. retry-waiting) → `pending_collection`
   (or `agent_offline` if the assigned site agent is offline).
4. legacy WSMan (`auth_ok_operation_fault`) on a Windows host → `needs_agent`.
5. `deviceTestStatus.authFailed` → `credential_failed`.
6. bound credential but nothing collected → `collection_failed`.
7. tested (non-auth failure, e.g. transport) → `collection_failed`.
8. Windows-like, never attempted → `not_attempted`.
9. credentialed class (switch/server/linux/…) never attempted → `needs_credential`.
10. else → `unmanaged`.

### Where "proven" comes from: `ListDeviceAccessSignals` (`queries/access.sql`)
Emits one `(device, protocol, source)` row per real management signal. `source` ∈
`bound_credential` (a binding — display only, NOT proven) | `evidence` (a child table that
exists only because an authenticated collection succeeded) | `test_result` (a
`credential_test_results` row that EVER succeeded). `buildAccessMap` (`access_coverage.go`)
folds these into `deviceAccess{proven: map[protocol]bool}`; `hasProven()` = any proven
protocol.

**Critical invariant (fixed this session — see §7):** os_inventory presence is durable
evidence for **every** row (`WHERE collection_method <> ''`, mapped wmi→wmi / ssh→ssh /
else→winrm). Do NOT regress this to an enumerated method IN-list — that silently dropped
`winrm-agent` and let later failures flip managed hosts to `credential_failed`.

### Credential-test categories → classification
`categoryIsAuthFailure(cat)` returns true ONLY for `auth_failed`, `access_denied`,
`wmi_access_denied` → these drive `credential_failed`. Everything else (transport, RPC,
WMI errors, `winrm_connect_timeout`, `winrm_negotiate_error`, `auth_ok_operation_fault`) is
NOT an auth failure → at worst `collection_failed` (operator/transport-fixable), never
`credential_failed`. The UI hint "Fix the rejected credential (user/password)" is keyed off
the `credential_failed` state only (`web/src/pages/UnmanagedDevices.tsx`).

### Single source of truth
Inventory, Device Detail, Unmanaged Devices page, badge counts, and the Connectivity report
all derive management from the **same** `deriveManagement`. The Connectivity report
(`connectivity_report.go` + `components/ConnectivityReport.tsx`) lists raw failed attempts
as *history*; it takes the management state from the device list — so history never
contradicts the managed state.

---

## 7. Recent fix chain (this session) — the deep logic + WHY

Branch `feat/discovery-acceptance-credkind`. The goal: make from-zero discovery of the
Windows subnet `172.21.60.0/24` settle correctly and automatically, with honest states and
NO manual recovery. Commit ladder (newest first):

| Commit | What & why |
|---|---|
| `5ef125a` | **WinRM `401 - invalid content type` → retryable `winrm_negotiate_error`, not `auth_failed`.** Under an 82-host storm the WinRM listeners returned a 401 whose body is an HTML error page (NTLM negotiation rejected at HTTP layer = overloaded listener), NOT a wrong password. `ClassifyWinRMError` matched the `"401"` substring → `auth_failed` → terminal `credential_failed`, never retried; 10 hosts (incl. `.119`/`.12`/`.120`/`.130`, all winrm-agent-managed the run before with the *same creds*) regressed. Fix: classify `invalid content type` BEFORE the generic 401; short-circuit before WMI; **`runJob` breaks the per-cred loop on any transient WinRM transport/negotiation category** to cut the load that *causes* the storm 401s. Agent v1.2.2. |
| `6187cab` | **WinRM TCP connect-timeout → retryable `winrm_connect_timeout`.** The connectex "did not properly respond" message contains none of refused/reset/timeout, so it fell to generic `"error"`, the ladder fell to WMI, WMI's UAC `wmi_access_denied` (terminal + auth-classified) masked the transient timeout → terminal `credential_failed`, no retry. Fix: distinct retryable category; short-circuit before WMI (avoids domain-cred lockout); retried with backoff → `collection_failed` only when exhausted. Agent v1.2.1. |
| `b8c4aad` | **Durable os_inventory evidence (state-regression fix).** `access.sql` counted os_inventory as `evidence` only for an enumerated `collection_method IN (...)` that omitted `winrm-agent`, so agent-collected hosts' managed state hung on a fragile credential-test row → a later WMI/UAC failure self-reverted them to `credential_failed`. Fix: every os_inventory row is durable evidence (`collection_method <> ''`). Read-model change → applies to existing data on deploy. |
| `1f56943` | **Agent WinRM-shell-first ladder.** Non-domain hosts managed by a LOCAL admin block remote WMI/CIM via UAC `LocalAccountTokenFilterPolicy` → `New-CimSession: Access is denied`; the agent only ran WMI. Added the two-rung ladder (WinRM-shell first, WMI fallback). Agent v1.2.0. Fixed kiosks `.49`/`.50`. |
| `1b721c8` | Agent **multi-credential** path (try ordered candidate list, stop on success, record every attempt, bind winner) + configurable dispatch/concurrency caps + job collection-progress object. `access_denied`/`wmi_access_denied` classify as auth failure. |
| `5aa5e44` | Agent concurrency (frequent heartbeats so a busy serial agent isn't seen offline) + never-drop-enqueue (route to the assigned agent even if momentarily offline) — closed the from-zero `not_attempted` gap. |
| `dc32708` | From-zero hardening: dispatch throttle, bounded retry, `pending_collection` state, stale-dispatched reaper, queue-visibility counts. |
| `819f957` | Scan-hardening: honest unmanaged reasons; structural OS-collection guard; attempted+non-auth ⇒ `collection_failed` not `needs_credential`. |
| `3bc8f48` | `osinv.scrubReport` strips NUL/invalid-UTF8 before persist (WinRM strings with `0x00` failed Postgres 22021 → collection succeeded but device stayed unmanaged). |

**The throughline:** a transient/non-auth failure must never become a terminal
`credential_failed`, and a successful collection (os_inventory) must durably keep a host
`managed` regardless of later failed attempts. Auth failures are reserved for clean
credential rejections after all applicable creds were tried.

### Tests (all pure, run with `go test ./...`)
- `internal/osinv/winrm_classify_test.go` — connect-timeout / negotiate-error / refused / 401-auth classification.
- `cmd/hims-agent/collect_windows_test.go` — `pickWindowsFailCat` (transient transport wins over WMI verdict).
- `internal/api/state_regression_test.go` — proven evidence outranks failed-cred history (7 scenarios).
- `internal/api/transport_retry_test.go` — transient categories are retryable + non-auth; pending-while-retrying; exhausted → collection_failed.
- `internal/api/scan_hardening_test.go` — the earlier 12 scan-hardening classes.

---

## 8. ⏳ CURRENT IN-FLIGHT WORK (pick this up first)

**Final from-zero acceptance test of `172.21.60.0/24` is RUNNING** as of this handover
(agent v1.2.2). Scan job id is in `D:\tmp\fz3_job.txt` (job `de11b693-…`). A background DB
poller logs to `D:\tmp\fz4.log` and settles when discovery is complete AND
`queued = dispatched = retry_waiting = 0`.

**Acceptance gates (user-defined — do NOT close until ALL pass with NO manual re-collect):**
- `.49`, `.50`, `.106`, `.119` all `managed` automatically (no targeted re-collect).
- `not_attempted = 0`, `pending_collection = 0`, `persist_error = 0`, `unknown_reason = 0`.
- `needs_credential = 0` unless a real auth failure after all applicable creds tried.
- `collection_failed` only for real transport/offline/non-auth after retry exhaustion.
- No stale failure overrides a later success; no "Fix rejected credential" UI for a
  transport timeout / retry-waiting.
- Powered-off hosts may be absent / operator-side only.

**How to check settle + buckets** (see §9 for the queries). If a host is still
`credential_failed`, inspect its `agent_jobs.error` + `credential_test_results.category` —
distinguish a *real* clean auth rejection (legitimate `credential_failed`, operator fixes
the cred) from a transient transport/negotiation miss (a pipeline bug to fix, like the last
three). **Diagnose before recovering; never manual-recover to make a gate pass.**

The user's standing rules for this work: **do not push** (commit locally only);
**no manual targeted re-collect / no manual recovery** to pass a gate; if a gate fails,
diagnose the exact pipeline gap and fix it.

---

## 9. Operational runbook (commands you'll reuse)

### Mint an API session (cookie auth) for direct API calls
```powershell
$token = -join ((1..32) | ForEach-Object { '{0:x2}' -f (Get-Random -Maximum 256) })
$sha  = [System.Security.Cryptography.SHA256]::Create()
$hash = ($sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($token)) | ForEach-Object {'{0:x2}' -f $_}) -join ''
docker exec hims-pg psql -U hims -d hims -c "INSERT INTO sessions (token_hash,user_id,expires_at) VALUES ('$hash','3c689426-6e17-4613-9956-2966a5285c8c', now()+interval '4 hours')"
$H = @{ Cookie = "hims_session=$token" }
# then: Invoke-WebRequest -Uri 'http://localhost:8090/api/v1/devices?category=all' -Headers $H
```
(User id `3c689426-6e17-4613-9956-2966a5285c8c` is the existing admin. Cookies are not
port-specific, so a browser tab on :8090 can also fetch the API same-origin.)

### Trigger a scan
```
POST http://localhost:8090/api/v1/discovery/scan
{ "mode":"targets", "targets":"172.21.60.0/24", "credential_ids":[] }
```

### Reset a subnet for a true from-zero (hard delete cascades)
```
docker exec hims-pg psql -U hims -d hims -c "DELETE FROM devices WHERE primary_ip << inet '172.21.60.0/24'"
```

### Settlement + buckets (DB-direct — no API session needed; survives session death)
```sql
-- collection queue for the subnet
SELECT count(*) FILTER (WHERE j.status='queued'      AND j.next_attempt_at<=now()) queued_runnable,
       count(*) FILTER (WHERE j.status='queued'      AND j.next_attempt_at> now()) retry_waiting,
       count(*) FILTER (WHERE j.status='dispatched') dispatched,
       count(*) FILTER (WHERE j.status='done')  done,
       count(*) FILTER (WHERE j.status='failed') failed
FROM agent_jobs j JOIN devices d ON d.id=j.device_id
WHERE d.primary_ip << inet '172.21.60.0/24' AND j.kind='collect_os';

-- failed-job categories (to classify unmanaged devices)
SELECT j.category, count(*) FROM agent_jobs j JOIN devices d ON d.id=j.device_id
WHERE d.primary_ip << inet '172.21.60.0/24' AND j.kind='collect_os' AND j.status='failed'
GROUP BY j.category ORDER BY 2 DESC;
```
Management distribution is authoritative via the API (`GET /devices?category=all`, group by
`management`) because it runs `deriveManagement`; raw DB tables don't carry the derived state.

### Watch live progress in the browser
Discovery → Scan Jobs → a job → "Discovery complete · collecting N" banner; Live Discovery
board; SSE stream `/api/v1/.../stream`.

---

## 10. Credentials & subnet scoping (anti-lockout)

- Discovery default = **try all applicable stored credentials** for the device class
  (operator selects none). `credresolver` builds the ordered candidate list.
- **Subnet-scoped credentials**: a credential set assigned to a Site subnet (Locations →
  subnet) is **exclusive** to IPs inside that subnet — scans of those IPs try ONLY those
  creds. This prevents credential spray and account lockouts (especially CCTV/Hikvision and
  domain accounts). See migration 000074/000075, `credresolver.Input.Exclusive`,
  `GET/PUT /subnets/{id}/credentials`.
- **Lockout discipline**: never try the same cred twice in a cycle; auth_failed
  short-circuits the WMI fallback; transient WinRM transport blips break the per-cred loop
  (don't submit logons to an overloaded host).
- Secrets are encrypted; the encryption key is required at startup (guarded). Never log
  secrets; the agent sanitizes errors.

---

## 11. Windows host management specifics (the hard part)

- Most Windows workstations block WinRM + WMI by default → unmanaged until a GPO is applied
  (see `docs/windows-remote-management-gpo.md`). This is NOT a credential problem.
- `rpc_unreachable` = WMI firewall-blocked, not auth_failed.
- Non-domain hosts managed by a LOCAL admin block remote WMI/CIM via UAC
  `LocalAccountTokenFilterPolicy` → `New-CimSession: Access is denied`; the WinRM-shell path
  works for them (hence the WinRM-first ladder).
- The API (LocalSystem) cannot auth domain Windows directly → Windows collection routes to
  the in-domain relay agent.
- WinRM error taxonomy (see `osinv/winrm.go` `ClassifyWinRMError`): `auth_failed` (clean
  401/unauthorized) | `winrm_connect_timeout` (no response, retryable) |
  `winrm_negotiate_error` (401 invalid-content-type / NTLM glitch under load, retryable) |
  `unreachable` (refused/reset) | `auth_ok_operation_fault` (legacy WSMan 2.0 → needs agent).

---

## 12. Other subsystems (pointers)

- **Collectors & live validation**: `docs/RUNBOOK.md` has per-collector procedures
  (SNMP/switches, Redfish/BMC, vSphere, Hyper-V, ONVIF/CCTV, UniFi/Omada/Ruckus/Extreme
  wireless, CUCM voice, AD import, monitoring loop, Settings timeouts).
- **CCTV**: Hikvision recorders use web/ISAPI port = `8000 + host-octet`; recorder
  detection keys on model code; cameras that are NVR channels are managed *via* the recorder
  (don't flag RTSP-only feeds as credential failures).
- **MIB-upload engine** (`mibpack`/`mibparse`): operators upload vendor MIBs to drive SNMP
  wireless collection; specificity-scored pack matching.
- **Monitoring/alerting**: reachability checks + SNMP-metric sampling; alert→work-order
  bridge.
- **Reporting / Endpoint Intelligence**: executive dashboards + per-device intel aggregates.
- **Phase history**: `docs/PROGRESS.md` (every phase, closed with live-validation triggers).

---

## 13. Invariants & working principles to PRESERVE

These are hard-won; violating them reintroduces fixed bugs:

1. **Reachability ≠ Management.** Open ports are never management. Management = proven
   authenticated collection or a successful credential test.
2. **Proven success is durable and wins.** os_inventory presence (any method) keeps a host
   `managed`; later failed attempts are *history*, not a downgrade.
3. **Honest reason codes.** Prefer multiple specific codes over one generic. A transient
   transport/negotiation failure is NEVER `credential_failed`. `credential_failed` =
   clean auth rejection after all applicable creds tried.
4. **No-spray / anti-lockout.** Subnet-scoped exclusive creds; short-circuit WMI on
   auth_failed; break the cred loop on transient WinRM transport.
5. **Discovery is zero-decision for the operator.** Type targets → managed. The engine
   auto-tries all creds + falls back across protocols; no operator-facing port/auth knobs in
   the default flow.
6. **Every commit compiles, vets, and tests.** Pure unit tests for state logic; fix vet/lint
   in their own commit.
7. **Durable fixes, not session workarounds.** Don't leave the user depending on a process
   you spawned (it dies with the session). The durable UI is the API service serving
   `web/dist`.
8. **Acceptance is DB-proven**, per-device bucketed, with each unmanaged device given a clear
   reason split into HIMS-fixable vs operator/GPO-fixable.

### User working preferences (the operator/owner)
- Writes Arabic sometimes; not deep in git. **Act decisively, minimize questions** — decide
  the sensible path, execute end-to-end, report in one short summary. Ask only on genuine
  human-only blockers.
- **Publish completed changes live immediately** (build → test → migrate/deploy → quick
  smoke → commit local). **PUSH only when explicitly asked.**
- Authorizes merging PRs once CI is green (no `gh` CLI installed; use GitHub REST API +
  Git Credential Manager) — but for this branch the standing instruction has been **do not
  push**.

---

## 14. Suggested next steps (after the in-flight test passes)

1. **Close the from-zero acceptance** (§8): confirm all gates, report the 15-row bucket
   table + per-IP reasons, then mark the `172.21.60.0/24` fresh-discovery pipeline accepted.
2. If any host is still wrongly `credential_failed`: diagnose the agent_jobs error vs a clean
   auth rejection (same pattern as the §7 fixes).
3. **Push + open a PR** for branch `feat/discovery-acceptance-credkind` (ONLY when the user
   asks) — it carries the NUL-scrub, scan-hardening, agent multi-cred + WinRM-first ladder,
   durable-evidence, and transient-transport-retry fixes.
4. Consider a **BACKLOG** item: load-aware agent concurrency / per-host WinRM connection
   throttle, since the storm 401s were load-induced (the cred-loop break + retry mitigates
   it, but a global concurrency governor would prevent it at source).
5. Roll the same from-zero acceptance to other production subnets once `172.21.60.0/24` is
   accepted.

---

## 15. Lineage / context

HIMS reuses patterns from NIMS (the prior system): security invariants (no secrets in logs,
encrypted creds, startup key guard), the deploy-then-migrate discipline, and the
collector/credential-resolver shape. The production NIMS deploy target is a separate Linux
box (`150.0.0.120`, compose project `nims-prod`); HIMS currently runs on the Windows dev/ops
box `CHV-MISMGR` (172.21.60.20). Do not conflate the two.
