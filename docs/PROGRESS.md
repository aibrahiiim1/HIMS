# HIMS — Progress Log

> Append-only record of phase progress. Each phase closes only when its
> code builds, vets, and tests green. Newest phase at the bottom of its
> section. `PLAN.md` is the roadmap; this is the ledger.

## Legend
- ✅ done & verified (build/vet/test green)
- 🚧 in progress
- ⬜ not started

---

## Phase 0 — Foundation ✅ (closed 2026-06-03)

**Goal:** the skeleton everything hangs on — CMDB schema, driver-engine
contract, credential-resolver logic, core domain + storage, repo + CI + docs.

### Sub-tasks
- ✅ Repo scaffold (go.mod, layout, .gitignore, sqlc.yaml, CI workflow)
- ✅ Docs: README, PLAN, PROGRESS, HANDOVER, ADR-0001
- ✅ Migrations 000001–000005: location tree, subnets, credentials +
  groups + members + bindings, devices, device_roles, device_facts —
  all validated by `sqlc generate` (DDL parses clean)
- ✅ Domain types (`internal/domain`): location tree, subnet, credential(+
  group), device (generic core), device fact; enums for category/status/
  credential-kind; sentinel errors
- ✅ Driver engine (`internal/driver`): `Driver` interface + `Registry`
  (register / get / best-match by confidence) + `aruba_hpe` reference
  driver (fingerprint by HP/Aruba enterprise OID + sysDescr) + `Collector`
  forward-declaration for Phase 1. **6 registry tests + 5 Aruba tests.**
- ✅ Credential resolver (`internal/credresolver`): pure ordering —
  fingerprint filter, bind-first, subnet>location, weak-sinks-unless-bound,
  priority, dedup. **8 tests.**
- ✅ sqlc generate + storage layer (`internal/storage/postgres`): pool +
  Store + the resolver-assembly bridge (`CredentialCandidates`: IP→subnet
  containment + location anchor → ScopedGroups) + starter queries
  (locations/credentials/devices/facts/roles)
- ✅ `go build/vet/test` green; gofmt clean; committed; phase closed

### Verification (2026-06-03)
`go build ./...` ✅ · `go vet ./...` ✅ · `gofmt -l .` clean ✅ ·
`go test ./...` ✅ (credresolver + driver + aruba suites pass).
Module: pgx/v5 + google/uuid only. No DB required for the default suite.

### Carry-forward to Phase 1
- `driver.Collector` (Collect over a transport `Session`) is a marker now;
  Phase 1 defines the SNMP/SSH transport that satisfies `Session` and the
  Aruba `Collect` (interfaces/VLANs/MAC/LLDP/port-roles).
- Postgres repos beyond the resolver bridge are intentionally thin/starter;
  Phase 1 shapes the concrete queries it needs (discovery reconcile,
  topology) rather than guessing them now.

### Notes
- Greenfield repo at `D:\WebProjects\HIMS`, module
  `github.com/coralsearesorts/hims`, Go 1.26.
- Reference driver for Phase 1 = **Aruba/HPE** (fleet query against the prior
  NIMS prod DB: 22 of 26 switches are HP/Aruba; 1 FortiGate; 4
  "linux"-vendor switches are misclassified — a classification test case).

---

## Phase 1 — Switches + Topology + Credential Resolver ✅ (closed 2026-06-03)

**Goal:** the first end-to-end vertical slice — discover an Aruba/HPE
switch, collect its operational data, render the switch template, compute
topology, and resolve IP/MAC/name → switch+port+path.

### Sub-commits
- ✅ SC1 — operational schema: migrations 000006 (discovery jobs/results)
  + 000007 (interfaces, vlans, port_vlans, mac_addresses, arp_entries,
  neighbors, topology_links) — all source-scoped, sqlc-validated. Queries
  for network inventory + search + discovery.
- ✅ SC2 — SNMP transport (`internal/snmp`, gosnmp-backed Client + helpers
  + OID utils) + `internal/mibs` (IF-MIB, Q-BRIDGE, LLDP, HP/Aruba OIDs) +
  **Aruba `Collect`**: ifTable/ifXTable interfaces, dot1q VLANs, FDB
  (Q-BRIDGE + legacy bridge), LLDP neighbors, port-role derivation.
  **11 driver/aruba tests** (sysinfo, interfaces, port-role, VLANs, FDB,
  LLDP, walk-error tolerance).
- ✅ SC3 — discovery pipeline (`internal/discovery`): staged
  alive→ports→light-SNMP→classify→**resolve credentials**→authenticated
  probe (bind-on-success)→deep collect. Wired to the credential resolver.
- ✅ SC4 — topology engine (`internal/topology`): multi-source link build +
  **IP/MAC/name → switch+port+VLAN search** + graph link assembly.
  **3 topology tests** (MAC→switch, IP→ARP→MAC→port, empty).
- ✅ SC5 — REST API (`internal/api`, chi): devices, per-device
  interfaces/vlans/neighbors/topology, `/search?q=`, `/topology/links`,
  locations. Wired into `cmd/hims-api` (with a no-DB dev fallback).
  `cmd/hims-collector` one-shot discovery mode.
- ✅ SC6 — React UI (`web/`, Vite + TanStack Query + Cytoscape): Inventory
  list, **Switch detail template** (interfaces/VLANs/neighbors/topology
  tabs with port-role + status badges), **Topology graph** (Cytoscape),
  **Search page** (IP/MAC/name → switch+port). tsc + production build green.

### Verification (2026-06-03)
Backend: `go build/vet/test ./...` green (14 unit tests across driver,
aruba, credresolver, topology). Frontend: `tsc --noEmit` clean +
`npm run build` succeeds. Default Go suite is DB-free.

### Carry-forward
- Live prod verification (against the 22 HP/Aruba switches) needs a DB +
  credential bindings configured — deferred to a deploy step; the engines
  are unit-proven.
- Path-to-core multi-hop chaining is stubbed (single-hop switch+port works);
  full uplink path-walk is a Phase 2 topology enhancement.
- Persistence of collected facts (writing Collect output → interfaces/
  vlans/mac/neighbors tables) is wired at the query layer; the collector
  write-back loop lands when monitoring scheduling does (Phase 2/3).

---

## Phase 2 — More switch drivers + topology hardening ✅ (closed 2026-06-03)

**Goal:** prove the driver engine scales to multiple vendors with no schema
or UI change, and harden topology for mixed-vendor segments.

### Sub-commits
- ✅ SC1 — extracted the shared switch-collection logic into
  `internal/driver/swsnmp` (CollectSysInfo / Interfaces / VLANs / FDB /
  LLDP / DerivePortRoles / FirmwareFromDescr). Refactored the Aruba driver
  to a thin assembly over it — **Aruba tests unchanged + still green**
  (behavior preserved).
- ✅ SC2 — **Cisco IOS driver** (`internal/driver/cisco`): fingerprint by
  enterprise OID 9 / "Cisco IOS" sysDescr; Collect via swsnmp + **CDP**
  (`swsnmp.CollectCDP`, CISCO-CDP-MIB cdpCacheTable) merged with LLDP. 4 tests.
- ✅ SC3 — **Huawei VRP driver** (`internal/driver/huawei`): fingerprint by
  enterprise OID 2011 / Huawei|VRP|Quidway sysDescr; Collect via swsnmp.
  3 tests. Both registered in `drivers.Builtin()`.
- ✅ SC4 — topology hardening: `topology.NeighborMerge` dedups LLDP+CDP for
  the same neighbor (LLDP wins identity, CDP mgmt-IP folded in), keyed by
  (local-if, remote-identity); keeps distinct neighbors + LAG legs apart;
  drops unidentifiable neighbors. 4 tests.

### Verification (2026-06-03)
`go build/vet/test ./...` green. New tests: cisco 4, huawei 3, drivers
(cross-vendor disambiguation) 3, topology merge 4 — plus all Phase 0/1
suites still pass. **No frontend changes** — Cisco/Huawei switches render
through the same generic switch template (ADR-0001 payoff).

### Carry-forward
- `NeighborMerge` is a tested utility; it wires into the collect→persist
  write-back path when that lands (Phase 3 monitoring).
- Cisco per-VLAN FDB community-indexing (older IOS) not yet handled — the
  standard dot1q + legacy-bridge FDB covers modern IOS; revisit if a real
  device returns empty FDB.

---

## Phase 3a — Servers via SNMP (HOST-RESOURCES-MIB) ✅ (closed 2026-06-03)

**Goal:** bring servers into the CMDB on the proven SNMP transport —
CPU/RAM/disk + interfaces + multi-role inference — without yet needing the
heavier WinRM/SSH/vSphere transports.

### Sub-commits
- ✅ SC1 — migration 000008 `server_storage` (per-volume RAM/disk) + queries.
- ✅ SC2 — HOST-RESOURCES OIDs + `swsnmp.CollectHostResources` (uptime,
  avg CPU load, hrStorageTable → RAM/disk) + **`host_snmp` driver**:
  fingerprints net-snmp(8072)/Microsoft(311) OIDs at conf 80, OS-descr at
  conf 55 (deliberately below a switch's authoritative 90 — a Linux-based
  switch stays a switch). `discovery.InferRoles` (open-ports → candidate
  roles: DNS/DHCP/DC[88+389]/SQL/Oracle/PostgreSQL); port-scan widened;
  `domain.DeviceRole` enum added.
- ✅ SC3 — API `/devices/{id}/storage|facts|roles`; **server template UI**
  (`ServerDetail`: resource facts, storage volumes w/ used%, roles,
  interfaces). Inventory split Switches / Servers; DeviceList parameterized
  by category.
- ✅ SC4 — build/vet/test green; docs; closed.

### Verification (2026-06-03)
`go build/vet/test ./...` green. New tests: host-resources collect (CPU
avg + RAM/disk byte math), role inference (DC needs 88+389; DB ports; no
false positives), server-by-net-snmp-OID, **Linux-based-switch-stays-
switch** disambiguation. Frontend tsc + build green.

### Carry-forward → Phase 3b / 3c (new transports)
- **3b — Virtualization**: ESXi (vSphere SOAP/REST), Hyper-V (WinRM/WMI) +
  VM→host mapping. Needs a vSphere client + WinRM transport.
- **3c — iLO/iDRAC** hardware health via Redfish (HTTP/JSON) or SNMP.
- Deep server inventory (services / installed software / exact OS build)
  via WinRM/SSH — beyond the SNMP baseline.
- Role inference is port-based (candidate); LDAP-bind / SQL-handshake
  confirmation is a later enhancement.

---

## Phase 4 — FortiGate firewall driver ✅ (closed 2026-06-03)

**Goal:** port the FortiGate work onto the clean architecture, carrying
every OID lesson validated against the real exported MIB during NIMS — on
the proven SNMP transport (no new transport infra).

### Sub-commits
- ✅ SC1 — migration 000009: firewall_status (1/device), firewall_vpn_tunnels,
  firewall_ha_members, firewall_licenses (all source-scoped) + queries.
- ✅ SC2 — `internal/mibs/fortinet.go` (validated OIDs + lessons in comments)
  + **`fortigate` driver**: fingerprint PEN 12356; Collect firmware (regex),
  CPU/mem %, **disk in MEGABYTES → bytes + derived pct** (not raw-as-pct),
  sessions; HA mode + group (**fgHaInfo 7**, not 3) + **member-count-from-
  rows**; VPN tunnels via **composite {tunnel, phase2} index** with
  **Counter64** octets; license contracts; interfaces via shared collector.
  Registered.
- ✅ SC3 — API `/devices/{id}/firewall-status|vpn-tunnels|ha-members|licenses`
  + **firewall template UI** (`FirewallDetail`: HA summary + resource facts,
  VPN tunnels up/down, cluster members with sync badges, license contracts).
  Firewalls nav + route.
- ✅ SC4 — build/vet/test green; docs; closed.

### Verification (2026-06-03)
`go build/vet/test ./...` green. New tests (7): fingerprint by PEN + no-match
for Cisco; **disk-is-MB-not-percent** (54024732672 bytes, 10%); **VPN
composite-index + Counter64 octets** (2 tunnels, 67.7 GB in); **HA-count-
from-rows** (serial-less row counts 1, no detail); HA member with serial +
sync; licenses. Cross-vendor disambiguation now includes fortigate.
Frontend tsc + build green.

### Every NIMS firewall bug pre-fixed by design
The fortigate driver was written with the four bugs we hit + fixed in NIMS
already corrected: disk-MB units, VPN composite index discarding all rows,
Counter64 octets parsing as nil, and fgHaGroupName at the wrong OID. Tests
lock each one.

---

## Phase 5 — Operations A: Work Orders + Systems & Licenses ✅ (closed 2026-06-03)

**Goal:** the operator-facing mini-ITSM the spec named most prominently —
asset-linked work orders + a systems/license register with live expiry —
all on pure CRUD (no new transport).

### Sub-commits
- ✅ SC1 — migration 000010: work_orders (lifecycle + asset link + cost) +
  work_order_events (append-only timeline) + systems (license/support
  expiry register). sqlc DATE override added.
- ✅ SC2 — `internal/operations` pure helper: `ComputeLicenseStatus`
  (active / expiring-90d / due-soon-30d / critical-7d / expired / unknown)
  + `WorstStatus` rollup, with tests. API (`internal/api/operations.go`):
  work-order list/create/get/PATCH (status transitions auto-record timeline
  events; resolved_at stamped on solve/close) + systems list (status
  computed live) / create.
- ✅ SC3 — UI: **Work Orders** page (list sorted by status+priority, create
  form, detail with status buttons + timeline + note entry) and **Systems &
  Licenses** page (list with computed expiry badges, create form with date
  pickers). Nav + routes.
- ✅ SC4 — build/vet/test green; docs; closed.

### Verification (2026-06-03)
`go build/vet/test ./...` green; new license-status tests (thresholds +
worst-of rollup). Frontend tsc + build green. First write-path (POST/PATCH)
in HIMS — added `api.post`/`api.patch` to the web client.

### Carry-forward → Operations B
Spare parts (stock + min-qty + work-order consumption decrement), purchase
records, and Expenses (aggregating contracts/internet/licenses/repairs/parts
by hotel/system/vendor/category/asset). Work-order `spare_parts` is free
text until the stock link lands.

---

## Phase 6 — Monitoring Engine (reachability + history) ✅ (closed 2026-06-03)

**Goal:** stand up the Monitoring Engine (PLAN §2.5, §6) — distinct from
discovery — that polls registered devices on a short interval and records a
time-series of reachability samples, rolling a live health badge onto each
device. Core ships **TCP-reachability** checks: no credentials, no new
transport (a plain dial), so the engine is honest and runs identically on
dev and prod.

### Sub-commits
- ✅ SC1 — migration 000011: `monitoring_checks` (per-device check: kind,
  port, interval, down_threshold, live rollup columns; UNIQUE(device,kind,
  port) → idempotent re-register) + `monitoring_samples` (per-poll
  time-series, device_id denormalized; promoted to a TimescaleDB hypertable
  via a best-effort DO block when the extension is present, plain table
  otherwise). sqlc queries + regen.
- ✅ SC2 — `internal/monitoring` pure core (DB-free, sockets-free):
  `Evaluate(ok, prevFailures, downThreshold)` hysteresis (success→up/0;
  failure→warning until threshold, then down; threshold clamped ≥1) +
  `Worst`/`RollupDevice` for the device badge. Poller does `ProbeTCP` over
  an injectable `DialFunc`. Tests cover every transition (up→warning→down,
  recovery clears the counter, threshold=1 has no warning band) + rollup +
  poller success/failure/invalid-addr + default-port map.
- ✅ SC3 — `Engine` (RunDue / runOne / rollupDevice / SeedDefaults / Loop)
  over a narrow `Repo` interface (*db.Queries satisfies it; fake in tests).
  API: `/monitoring/{checks,overview,seed,run}` + per-device
  `/monitoring/{checks,samples}`. Collector grows `-monitor` (scheduled
  sweep loop, signal-aware) + `-seed` flags.
- ✅ SC4 — UI: **Monitoring** page (status-count tiles, seed + run-now
  buttons, checks table with live status/latency/fail-count + enable/disable
  + delete). Nav + route. Build/vet/test + frontend green; closed.

### Verification (2026-06-03)
`go build/vet/test ./...` green incl. new `monitoring` package (engine
tests use a fake Repo + fake dialer — no DB, no sockets). Frontend tsc +
build green. First DELETE write-path in HIMS — added `api.del`.

### Design notes
- **Transition-at-event:** status hysteresis is computed at the poll, from
  the prior failure counter — one tested place, no background sweep
  (cf. memory "evaluate state transitions at transition time").
- **No-new-transport discipline:** TCP dial reuses what we have. SNMP-metric
  checks (sysUpTime / CPU / RAM) need the credential-decrypt path in the
  collector and are deferred to **6B**; the schema already carries
  `kind='snmp'` + `oid`, so 6B is additive, not a migration.

### Carry-forward → Phase 6B (monitoring enrichment)
SNMP-metric checks via the credential-decrypt path; sample retention /
downsampling policy; alert rules over samples (→ the alert→work-order bridge
in Operations B). UNIQUE(device,kind,port) treats NULL port as distinct, so
6B's snmp checks need a partial unique index to stay idempotent.

---

## Operations B — Spare Parts + Purchases + Expenses ✅ (closed 2026-06-03)

**Goal:** complete the operations layer (PLAN §7): spare-parts stock,
work-order parts consumption, purchase records, and an expense rollup. The
alert→work-order bridge stays with the alerting engine (it has no alert
source until Monitoring 6B).

### Sub-commits
- ✅ SC1 — migration 000012: `spare_parts` (stock + reorder threshold +
  partial low-stock index) + `work_order_parts` (consumption, unit cost
  snapshotted at consume time) + `purchases` (capex/opex ledger, optional
  system/device/location links). sqlc queries + regen.
- ✅ SC2 — `internal/operations/stock.go` pure `ComputeStockStatus`
  (out / low / ok) + tests. **Atomic consume**: `ConsumePartToWorkOrder` is
  a single CTE statement — `UPDATE … WHERE quantity >= n` feeds the INSERT;
  if the precondition fails the CTE yields no row, the insert is empty, and
  `:one` returns `ErrNoRows`, which the handler maps to **409 insufficient
  stock**. No SELECT-then-UPDATE TOCTOU window (atomic-DB-signal pattern).
- ✅ SC3 — API: spare-parts CRUD + `/stock` adjust + `/low-stock`;
  work-order `/parts` (stock consume or free-text); purchases list/create/
  delete; `/expenses/by-category` + `/by-location` (aggregate the purchases
  ledger). DTOs enrich parts with computed stock status.
- ✅ SC4 — UI: **Parts** page (stock table + status badges + adjust/delete +
  create) and **Expenses** page (purchases ledger + create + by-category /
  by-location rollups with grand total). Nav + routes. gofmt + build/vet/
  test + frontend green; closed.

### Verification (2026-06-03)
`go build/vet/test ./...` green incl. stock-status tests. Frontend tsc +
build green. gofmt clean (also formatted two Phase-6 files flagged in
passing).

### Design notes
- **Expenses derive from purchases**, not a separate table — totals can't
  drift from their source rows. Work-order cost + system cost stay on their
  own pages (not merged) to avoid double-counting a purchase logged for the
  same repair/license.
- **Atomic stock decrement** is the load-bearing correctness piece; it is
  enforced in SQL, so concurrent consumes can't oversell stock.

### Carry-forward
- Alert→work-order bridge → with **Monitoring 6B** (needs an alert source).
- Atomic-consume **integration test** (behind `-tags=integration`) once a DB
  test harness is wired — the unit layer can't exercise the CTE.

---

## Monitoring 6B — Alerting engine + alert→work-order bridge ✅ (closed 2026-06-03)

**Goal:** rule-based alerting over the state the monitoring engine produces,
closing the alert→work-order bridge that Operations B and the Monitoring
engine both pointed at.

**Scope split (flagged, not silent):** the original 6B was "SNMP-metric
checks + alert rules". Reconnaissance found **no encryption-at-rest
implementation exists** (only the `DecryptFn` interface + schema columns), so
SNMP-metric checks would require building credential-decrypt infrastructure —
a platform-wide concern, not a monitoring detail. That work is split out to
**6C**; 6B delivers the alerting half, which needs no crypto and runs on
existing data.

### Sub-commits
- ✅ SC1 — migration 000013: `alert_rules` (trigger status + min-failures +
  optional category filter + severity + `auto_work_order` + WO priority) +
  `alerts` (open/acknowledged/resolved, `work_order_id` bridge link). A
  **partial unique index** `(rule_id, check_id) WHERE status <> 'resolved'`
  makes "open" idempotent — a flapping check can't pile up duplicates.
- ✅ SC2 — `internal/alerting` pure `Matches(rule, checkState)` predicate
  (status + failure-floor + category filter) with tests; `Engine.Evaluate`
  over a narrow `Repo`: resolve-recovered first (freeing the slot), then open
  newly-matching alerts via `OpenAlert` (ON CONFLICT DO NOTHING → RETURNING
  yields a row only on a real insert, so the WO bridge fires exactly once),
  then auto-create + link a work order when the rule flags it. Tests: open+
  bridge, idempotency, no-WO-when-unflagged, resolve-notes-WO. All fake-repo,
  no DB.
- ✅ SC3 — API: `/alert-rules` CRUD + enable, `/alerts` list + `/evaluate` +
  `{id}/ack` + `{id}/resolve`. Monitoring `Engine` gains an `AfterSweep`
  hook; the collector chains `alerting.Evaluate` after each sweep (dependency
  inversion — monitoring never imports alerting).
- ✅ SC4 — UI: **Alerts** page (active/recent alerts with ack/resolve + WO
  link indicator; rules table with create form, enable/disable, delete;
  evaluate-now). Nav + route. gofmt + build/vet/test + frontend green; closed.

### Verification (2026-06-03)
`go build/vet/test ./...` green incl. new `alerting` package. Frontend tsc +
build green. gofmt clean.

### Design notes
- **Atomic open + fire-once bridge:** the partial unique index + ON CONFLICT
  guarantees one open alert per (rule, check); RETURNING-only-on-insert means
  the work order spawns exactly once even under repeated sweeps.
- **Resolve-before-open ordering:** recovered alerts resolve first each pass,
  so a check that flapped down→up→down in one interval re-opens cleanly.
- **Dependency inversion:** monitoring exposes an `AfterSweep` callback rather
  than importing alerting — the engines stay decoupled.

### Carry-forward → 6C
SNMP-metric checks (sysUpTime / CPU / RAM) + the credential **encryption-at-
rest + decrypt** path they require (encrypt on credential create, decrypt on
use in the collector). This is platform infrastructure several engines will
share, so it gets its own phase.

---

## 6C — Credential crypto + SNMP-metric monitoring ✅ (closed 2026-06-03)

**Goal:** the encryption-at-rest infrastructure the platform needs to hold
credentials safely, and the SNMP-metric monitoring checks that depend on it
(the half of the original 6B that was split out).

### Sub-commits
- ✅ SC1 — `internal/secret`: AES-256-GCM `Cipher` keyed from a base64 32-byte
  env key. `Seal` returns (nonce‖ciphertext‖tag, KeyID); `Open` verifies the
  KeyID then authenticates+decrypts. KeyID = first-4-bytes-hex of SHA-256(key)
  — a rotation tag that reveals nothing. Tests: round-trip, fresh-nonce-per-
  call, tamper-detected, wrong-key (`ErrKeyMismatch`), bad-key.
- ✅ SC2 — credentials API: `POST /credentials` seals the secret before it
  touches the DB; `GET /credentials` and the create response return a
  **metadata-only DTO** (id/name/kind/weak/created_at) — the blob, key id, and
  plaintext never leave the server. `PUT /devices/{id}/credential` binds a
  credential to a device. Weak SNMP communities (public/private/community)
  auto-flagged. Cipher wired into the API + collector from `HIMS_ENCRYPTION_KEY`
  (absent ⇒ credential writes 503, everything else still serves).
- ✅ SC3 — SNMP-metric checks: poller gains `ProbeSNMP` (SNMP GET over an
  overridable client factory; records a numeric value). The monitoring engine
  dispatches by kind — snmp checks decrypt the device's bound community
  **in-memory** (never logged) and poll the check's OID (default sysUpTime).
  No cipher ⇒ snmp checks skipped (API-side engine stays reachability-only;
  the collector wires the cipher). Register endpoint now accepts `kind=snmp`
  + `oid`. Tests: ProbeSNMP success/timeout; engine snmp-with-cipher records
  value + up; snmp-skipped-without-cipher.
- ✅ SC4 — UI: **Credentials** page (create with masked secret input + list
  metadata with weak badge). Nav + route. gofmt + build/vet/test + frontend
  green; closed.

### Verification (2026-06-03)
`go build/vet/test ./...` green incl. new `secret` package + monitoring snmp
tests. Frontend tsc + build green. gofmt clean.

### Security invariants held
- Plaintext secrets are sealed before the DB and opened only in memory at
  point of use; never logged, returned, or rendered.
- Credentials are returned as metadata-only DTOs — blob + key id never leave
  the server. SNMP communities never appear in logs or sample error strings.
- Encryption key lives only in `HIMS_ENCRYPTION_KEY` (env), never in DB/git.

### Carry-forward
- Per-device SNMP-check registration + credential-bind UI (backend API is
  done; discovery already binds on success — UI is a small follow-up).
- Key rotation tooling (KeyID already tags each blob for it); SNMP v3.

---

## 3b/3c — Virtualization (ESXi) + BMC scaffolding ✅ (closed 2026-06-03)

**Goal:** classify + monitor virtualization hosts and lay the VM-inventory
foundation, with the heavy transports honestly deferred.

- ✅ `vmware_esxi` driver — fingerprints the VMware enterprise OID
  (`.1.3.6.1.4.1.6876`, authoritative 90) or an ESXi sysDescr (70); collects
  host CPU/RAM/datastore + interfaces via the shared swsnmp collectors
  (`virtual_host` template). Registered in the builtin set. Tests: OID match,
  descr heuristic, no-match, name/template.
- ✅ migration 000014 `virtual_machines` (host→VM, power/vcpu/mem/guest OS,
  upsert keyed on host+name) + `/devices/{id}/vms` API.
- ✅ UI: **Virtual Hosts** nav + `VirtualHostDetail` (host resources +
  datastores + VM section that explains VM enumeration awaits the API
  transport). Reachability + SNMP-metric monitoring already cover these hosts.
- ✅ build/vet/test + frontend green; gofmt clean.

### Carry-forward (deep transports — explicitly deferred, with triggers)
- **VM enumeration** (per-VM power/vCPU/guest OS, host→VM map) via the
  vSphere API (govmomi) — trigger: when a vCenter/ESXi credential is bound
  and operators need VM inventory. New external dep + transport.
- **Hyper-V** host/VM via WinRM — trigger: first Hyper-V host in inventory.
- **iLO/iDRAC out-of-band** via Redfish (HTTP/JSON) — modelled as server
  enrichment (bmc.* facts); trigger: when BMC credentials are bound. Pure-Go
  feasible, deferred only for scope.

---

## Phase 7 — CCTV (cameras + NVR/DVR) ✅ (closed 2026-06-03)

- ✅ `cctv` driver — fingerprints by HTTP banner (Hikvision/Dahua/Axis/…, 75),
  or open RTSP 554 (60); a recorder hint (nvr/dvr/recorder) flips the category
  to NVR. Registered in the builtin set. Tests: vendor camera, recorder→NVR,
  RTSP-only, no-match.
- ✅ migration 000015 `camera_info` (manufacturer/model/resolution/RTSP/ONVIF)
  + `nvr_channels` (channel→camera map, upsert keyed on nvr+channel) +
  `/devices/{id}/camera` and `/nvr-channels` APIs.
- ✅ UI: **Cameras** + **NVRs** nav (DeviceList) + shared `CctvDetail` (camera
  info + channel list; states deep fields await ONVIF, reachability monitored
  today).
- ✅ build/vet/test + frontend green; gofmt clean.

### Carry-forward (deferred, with trigger)
Deep collection — channel inventory, codec/resolution, recording state, RTSP
URLs — via ONVIF (SOAP) + vendor REST. Pure-Go feasible; trigger: when CCTV
credentials are bound and operators need channel inventory. Reachability
(RTSP/HTTP) is monitored now.

---

## Phase 8 — Wireless controllers + APs ✅ (closed 2026-06-03)

- ✅ `wlan_controller` driver — fingerprints UniFi/Omada/Ruckus/Aruba by HTTP
  banner + the vendor's mgmt port (78 with port, 60 banner-only). Registered.
  Tests: UniFi+port, banner-only, no-match.
- ✅ migration 000016 `wlan_controller_info` + `access_points` (AP inventory,
  upsert keyed on controller+name) + `/devices/{id}/wlan` + `/access-points`.
- ✅ UI: **Wireless** nav + `WirelessDetail` (controller summary + AP table;
  states AP detail awaits vendor REST, controller reachability monitored).
- ✅ build/vet/test + frontend green; gofmt clean.

### Carry-forward (deferred, with trigger)
Vendor REST collection (login → AP/SSID/client enumeration) for
UniFi/Omada/Ruckus. Pure-Go feasible; trigger: when a controller credential
is bound and operators need AP inventory.

---

## Phase 9 — Databases + AD/DNS/DHCP roles ✅ (closed 2026-06-03)

The multi-role CMDB cut. Role *inference* largely landed in 3a; this phase
broadens it and makes roles a first-class fleet view.

- ✅ broadened `InferRoles` (port→role): added web_server (80) + file_server
  (445/2049) alongside the existing DNS/DHCP/DC/SQL/Oracle/PostgreSQL. Bare
  443 deliberately excluded (too many appliances). Tests: file+web, no
  double-add of file_server.
- ✅ fleet role queries `RoleSummary` (count per role) + `ListDevicesByRole`;
  APIs `/roles/summary` + `/roles/{role}/devices`.
- ✅ UI: **Roles** page (role-count tiles → drill-down device list).
- ✅ build/vet/test + frontend green; gofmt clean.

### ⚠️ Cross-cutting finding → BACKLOG (high priority): discovery→persist apply worker
Reconnaissance during this phase confirmed `CreateDevice`/`AddDeviceRole`/the
inventory writers were **not called by any production path**. ✅ **RESOLVED**
in the next commit (`internal/apply` + collector `-discover`). See the
BACKLOG-PERSIST section below.

### Carry-forward
Deep role confirmation (LDAP bind, SQL handshake) — needs those transports;
deferred. Role auto-application happens inside the persist worker above.

---

## BACKLOG-PERSIST — discovery→persist apply worker ✅ (closed 2026-06-03)

The integrator that turns the engines + drivers into a live system: it takes
the `HostResult` a discovery run produces and writes it into the CMDB.

- ✅ `internal/apply` — `Applier.Apply(HostResult, locationID)`:
  - **reconcile** by (primary_ip, location): update a live device if found
    (`UpdateDiscoveredDevice`), else `CreateDevice`. Location-less scans
    always create (documented edge).
  - **bind-on-success**: persists the authenticating credential.
  - **roles**: applies `InferRoles(openPorts)` with source "port".
  - **facts + inventory**: upserts KV facts + interfaces/VLANs/MACs/neighbors/
    storage + firewall (status/VPN/HA/licenses), each stamped
    `last_seen = pollStart` under `collection_source = "snmp"`, then
    **prunes stale** rows (`last_seen < pollStart`) — a poll that no longer
    sees a row removes it.
  - Tested via a fake `Writer`: create-path persists everything (device +
    cred + dns-role + 2 ifaces + vlan + neighbor + fact + 3 stale-prunes,
    snmp source, poll-stamp), reconcile-updates-existing, dead-host-skips.
- ✅ migration query `UpdateDiscoveredDevice` (reconcile refresh).
- ✅ collector `-discover <ip> [-location <uuid>]` — connects DB, builds the
  Postgres scope-resolver fetcher + an in-memory cipher-decrypt closure
  (community never logged), runs the pipeline, applies. The end-to-end path
  that populates the live system.
- ✅ build/vet/test green; gofmt clean.

### Carry-forward
- Range/CIDR + AD-import discovery driving Apply over many IPs (the engine is
  per-IP ready); an API discover-and-apply endpoint (collector path done).
- Integration test against a real Postgres (gated `-tags=integration`) — the
  unit layer covers orchestration via the fake Writer.

---

## Phase 10 — MIB upload engine ✅ (closed 2026-06-03)

Self-contained, no new transport (reuses SNMP). Upload a MIB → parse into an
OID library → bind OIDs to metrics/templates.

- ✅ `internal/mibparse` — pragmatic SMIv2 reader: extracts OBJECT IDENTIFIER
  nodes + OBJECT-TYPE leaves and resolves each `{ parent N }` to a dotted
  numeric OID against a seeded base tree (iso/org/.../enterprises/system) +
  in-file definitions, with a cycle guard. Names that can't reduce to a
  numeric root are kept and flagged `Unresolved` (operator sees them, not
  dropped). Tests: enterprise-chain resolve (fortinet→…→fgSysVersion),
  OBJECT-TYPE kind+syntax, unresolved-parent-kept, empty-input error.
- ✅ migration 000017 `mib_files` + `mib_objects` + `oid_mappings`
  (OID→label/metric/vendor/template binding, upsert on (oid, metric_key)).
- ✅ API: POST `/mibs` (parse+store, returns parsed/unresolved counts), GET
  `/mibs`, `/mibs/{id}/objects`, `/oid-mappings` GET/POST/DELETE.
- ✅ UI: **MIBs** page (paste-and-parse upload, file list with unresolved
  badge, OID object table, OID-mapping bind form + list).
- ✅ build/vet/test + frontend green; gofmt clean.

### Carry-forward
Full ASN.1 grammar (IMPORTS resolution across MIBs, table INDEX, ranges) — the
pragmatic reader covers the common node/leaf assignments; a complete parser is
deferred. Test-GET an OID against a live device (reuses the SNMP poller) —
deferred to pair with the per-device monitoring UI follow-up.

---

## Phase 11 — Reporting + executive dashboards ✅ (closed 2026-06-03)

The cross-cutting rollup over every engine shipped so far.

- ✅ reporting queries: device count by category + by status, open work
  orders, open alerts, expiring systems (license/support ≤ 90d),
  devices-needing-attention (down/warning), total expenses.
- ✅ `GET /dashboard` — assembles inventory (category/status/role), monitoring
  health, expenses-by-category, and headline counts in one call;
  **best-effort** (a failed sub-query degrades to an empty section, so an
  empty DB still renders).
- ✅ UI: **Dashboard** page (headline tiles with warn colouring +
  proportional-bar breakdowns for category/status/monitoring/roles/expenses);
  added as the first nav item, polls every 30s.
- ✅ build/vet/test + frontend green; gofmt clean.

---

## Follow-ups — key rotation + per-device ops UI ✅ (closed 2026-06-03)

- ✅ **Credential key-rotation tooling**: `secret.ReKey(old, new, blob, keyID)`
  (open with old → seal with new; test asserts the re-keyed blob opens under
  the new KeyID and the old key no longer matches). `UpdateCredentialSecret`
  query + collector `-rekey` mode (env old+new keys; lists credentials,
  re-seals each, **idempotent** — rows already under the new KeyID are
  skipped; non-zero exit if any fail).
- ✅ **Per-device monitoring-check + credential-bind UI**: reusable
  `DeviceOps` component (register a tcp/snmp check via POST /monitoring/checks;
  bind a credential via PUT /devices/{id}/credential; lists the device's
  checks with live status). Added to Switch / Server / Firewall / Virtual-Host
  detail pages. Added `api.put`.
- ✅ build/vet/test + frontend green; gofmt clean.

### Deferred → SNMP v3 (its own phase, not "small")
Full SNMP v3 (USM) needs auth + priv protocol selection and keys carried in
the credential model, plus gosnmp v3 security-params wiring and the discovery
prober + monitoring poller honoring v3. The credential `kind=snmp_v3` already
exists; the transport is v2c-only today. Trigger: first device that mandates
v3 (no v2c community). Filed as BACKLOG-SNMPV3.

---

## Range/CIDR scan orchestrator ✅ (closed 2026-06-03)

Extends the per-IP persist path to whole-fleet onboarding.

- ✅ `discovery.ExpandCIDR(prefix, maxHosts)` — pure: enumerates hosts, skips
  IPv4 network/broadcast on /30-or-wider, yields all for /31-/32, and
  **refuses** an oversized scope (errors before allocating a /8) rather than
  silently truncating. Tests: /29 skip-ends, /31+/32, oversize refusal,
  unmasked-normalization.
- ✅ `internal/scan.Scope(ctx, ips, concurrency, fn)` — bounded worker pool
  over an injectable per-IP `discover→apply` fn; aggregates
  persisted/skipped/failed; honours context-cancel (stops dispatch). Tests:
  outcome aggregation, concurrency-limit (max-in-flight ≤ N), cancel-dispatches-
  nothing. No network (fn injected).
- ✅ collector `-scan <cidr> [-concurrency N] [-max-hosts M] [-location uuid]`
  — reuses the shared `buildDiscoverDeps` (Store fetcher + cipher decrypt +
  pipeline) fanned across the scope, signal-aware. Refactored `-discover` onto
  the same shared deps.
- ✅ build/vet/test green; gofmt clean.

### Carry-forward → ✅ DONE next commit (scan API + Discovery UI)
Persist a `discovery_jobs` + per-host `discovery_results` record per scan + an
API/UI to launch + watch scans — shipped below. AD-import scope source still
deferred.

---

## Scan API + Discovery UI ✅ (closed 2026-06-03)

Operator-facing subnet scanning (no longer CLI-only).

- ✅ API: `POST /discovery/scan {cidr, location_id, concurrency}` validates +
  expands the scope, creates a `discovery_jobs` row (status running), and
  launches a **background goroutine** (own 30-min context, not the request's)
  — returns 202 + the job immediately. `GET /discovery/jobs` +
  `/discovery/jobs/{id}` (job + per-host results).
- ✅ Background runner: `scan.Scope` over the hosts; per IP runs the pipeline
  + apply worker, records a `discovery_results` row for each **alive** host
  (outcome enrolled / classified / alive / failed, with driver+category+device
  link), and finalizes the job (completed/failed + found_count). Reuses the
  server cipher for in-memory credential decrypt (community never logged).
- ✅ `NewServer` extended with the driver registry + credential scope-resolver
  fetcher; nil-safe (scans return 503 if unconfigured). `cmd/hims-api` wires
  `drivers.Builtin()` + `postgres.New(pool)`.
- ✅ UI: **Discovery** page (CIDR + optional location scan form; jobs table
  polling every 5s for live status/counts; per-job results table with outcome
  badges). Nav item after Dashboard.
- ✅ build/vet/test + frontend green; gofmt clean.

### Carry-forward
Job cancellation endpoint; AD-import scope source; subnet-stored scopes
(subnet_id) instead of ad-hoc CIDR; results pagination for very large scans.

---

## Redfish — iLO/iDRAC out-of-band collector ✅ (closed 2026-06-03)

Dependency-free HTTP/JSON BMC collection for HPE iLO + Dell iDRAC — and the
**reusable HTTP-credential transport** future vendor-REST drivers build on.

- ✅ `internal/redfish` — `Client` (injectable `Doer`, HTTP Basic auth,
  self-signed-cert tolerant for mgmt LAN) + `Collect` walking service-root →
  Systems / Chassis (Thermal+Power) / Managers / Storage into normalized
  `BMCFacts` (vendor, iLO/iDRAC kind, model, serial, BIOS+BMC firmware, power,
  health, CPU/RAM, fan/PSU/temperature/storage sensors). HPE/Dell OEM detect.
  Optional sections best-effort. Tested against **sample HPE iLO + Dell iDRAC
  payloads** via a fake Doer (vendor detect, identity, sensor mix, a Critical
  fan status preserved, 404 errors).
- ✅ `internal/driver/redfish` — `redfish_bmc` driver: HTTPS-banner fingerprint
  (iLO/iDRAC/Redfish → server, conf 72, below switch-authoritative) + an
  HTTP-session `Collect` mapping `BMCFacts` → `driver.Facts` (BMC snap +
  sensors + KV). Registered in the builtin set. Tested (fingerprint + mapping
  + wrong-session).
- ✅ `Facts.BMC` + `Facts.BMCSensors`; apply worker persists them
  (`bmc_info` + `bmc_sensors`, stale-prune, source=redfish). Migration 000018
  + queries. Apply test asserts BMC + sensors persist.
- ✅ API: `GET /devices/{id}/bmc` + `/bmc-sensors`. UI: **BMC / hardware
  health** section on ServerDetail (controller summary + health badge + sensor
  table).
- ✅ collector `-redfish <ip> [-location]` — resolves scoped http_basic
  credentials (secret = `user:password`), verifies `/redfish/v1/`, collects +
  applies. Community/password used only in memory, never logged.
- ✅ gofmt + go build/vet/test + frontend green.

### ⚠️ Live-validation trigger (not yet validated against real hardware)
The Redfish field shapes follow the DMTF schema + published HPE/Dell examples
but have **not been validated against a real iLO 5 / iDRAC 9**. Trigger:
first BMC credential bound on the real fleet — run `-redfish` against one HPE
+ one Dell server, confirm the parsed model/serial/firmware/sensors, and
adjust field paths if a vendor diverges (esp. per-physical-drive health, which
v1 summarizes at the storage-controller level). Not marked
production-validated until then.

### Carry-forward
Per-physical-drive health (one GET per Drive ref); fact-based hardware-health
alert rules (the alerting engine currently matches monitoring-check status, not
arbitrary facts — adding a "bmc.health != OK" rule type is a follow-up); link a
BMC device to its OS-side device (BMC has its own mgmt IP). This HTTP client +
http_basic path is the reusable base for the vendor-REST drivers next.

---

## vSphere — ESXi host→VM map + datastores (govmomi) ✅ (closed 2026-06-03)

First external-dependency deep-collection phase. Adds `github.com/vmware/
govmomi` — chosen because it ships the **vcsim simulator**, so the collector
is fully tested against an in-memory vCenter with no real hardware.

- ✅ `internal/vsphere` — `Collect(ctx, *vim25.Client)` retrieves VMs (name,
  power, vCPU, memory, guest OS, IP) + datastores (capacity/free) via a
  ContainerView over the root folder; normalizes power state to our schema's
  vocabulary. **Tested against `simulator.Test` (vcsim)**.
- ✅ `internal/driver/vsphere` — `vmware_vsphere` driver: ESXi/vSphere HTTPS-
  banner fingerprint (→ virtual_host, conf 71) + govmomi-session `Collect`
  mapping inventory into `driver.Facts` (VMs + datastores-as-storage).
  Registered. Tested via vcsim + fingerprint + wrong-session.
- ✅ `Facts.VMs` + apply persists via `UpsertVM` (power-state clamp, IP parse)
  into the existing `virtual_machines` table — so the **VirtualHostDetail VM
  section now populates** (no UI change). Datastores reuse `Facts.Storage`.
- ✅ collector `-vsphere <ip> [-location]` — resolves scoped vendor_api/
  http_basic creds, connects via `govmomi.NewClient` to `https://<ip>/sdk`,
  collects + applies. Password used only in memory.
- ✅ go mod tidy; gofmt + go build/vet/test ./... green; frontend tsc green.

### ⚠️ Live-validation trigger
Validated against vcsim (the canonical govmomi test double), **not a real
ESXi 7/8 or vCenter**. Trigger: first vSphere credential bound. v1 targets a
single ESXi `/sdk`; carry-forward: vCenter multi-host walk, port groups/VLANs/
host NICs, VM↔managed-device linking, an API/UI collect trigger.

## Hyper-V — host→VM via WinRM/PowerShell ✅ (closed 2026-06-03)

Second deep-collection dep (`github.com/masterzen/winrm`). WinRM has no
simulator, so the design isolates the **testable core** — the Get-VM output
parser — from the un-simulatable transport.

- ✅ `internal/hyperv` — `Runner` interface (injectable) + `CollectVMs` running
  `Get-VM | ConvertTo-Json`. Parser handles ConvertTo-Json's single-object-vs-
  array quirk and the VMState enum as **number (2/3/9) or string** → our
  vocabulary. Tested: array, single-object, suspended+unknown, empty,
  runner-error.
- ✅ `internal/driver/hyperv` — **collection-only** driver: Fingerprint is
  NoMatch by design (WinRM+Windows isn't Hyper-V-specific; finding VMs confirms
  the role), Collect maps VMs → `Facts.VMs`. Tested.
- ✅ Reuses `Facts.VMs` + apply → `virtual_machines` (VirtualHostDetail VM
  section populates; no UI change). collector `-hyperv <ip>` resolves winrm
  creds, `RunPSWithContext`, applies.
- ✅ go mod tidy; gofmt + go build/vet/test ./... green.

### ⚠️ Live-validation trigger
Parser tested against sample Get-VM JSON; the **WinRM transport** can't be
simulated and is unvalidated. Trigger: first winrm credential bound on a real
Hyper-V host.

## ONVIF — camera device-info + media profiles ✅ (closed 2026-06-03)

Third deep-collection transport. Rather than depend on a heavyweight ONVIF
library, HIMS rolls a **thin SOAP client over an injectable Doer** (the proven
Redfish pattern), making the WS-Security digest + XML parsing unit-testable
with no camera.

- ✅ `internal/onvif` — SOAP client: WS-Security UsernameToken **PasswordDigest**
  (`Base64(SHA1(nonce+created+password))`) + SOAP POST + parse
  `GetDeviceInformation` + `GetProfiles` (best-effort). Tested with sample
  Hikvision-shaped SOAP + the **canonical OASIS WS-Security digest vector**.
- ✅ `internal/driver/onvif` — `onvif_camera` collection-only driver
  (Fingerprint NoMatch; cctv classifies). Collect → `Facts.Camera`. Tested.
- ✅ `Facts.Camera` + apply → `camera_info` (Phase 7 table), so **CctvDetail
  populates** (no UI change). collector `-onvif <ip>`.
- ✅ go mod tidy (own SOAP, no heavy lib retained); gofmt + build/vet/test green.

### ⚠️ Live-validation trigger
Digest validated against the OASIS vector; parsing against sample SOAP. Not
validated against a real camera (vendor namespace/field variance). Trigger:
first ONVIF credential bound; add GetStreamUri (RTSP URL) which v1 omits.

## UniFi — wireless controller REST (AP inventory) ✅ (closed 2026-06-03)

Fourth (final) deep-collection transport. Reuses the HTTP/JSON Doer pattern;
the device-list parser is the tested core.

- ✅ `internal/unifi` — `Client` over an injectable Doer: `Login` (POST
  /api/login, cookie-jar session) + `ListAPs` (GET /api/s/<site>/stat/device)
  **filtering to `type=uap`**, state→online/offline, num_sta→client count.
  Tested (uap filter, online/offline, login-fail, device-error).
- ✅ `internal/driver/unifi` — `unifi` collection-only driver (Fingerprint
  NoMatch; wlan_controller classifies). Collect → `Facts.WLAN` + `Facts.APs`.
- ✅ apply persists via `UpsertWLANControllerInfo` + `UpsertAccessPoint` → the
  Phase 8 tables, so **WirelessDetail populates** (no UI change). collector
  `-unifi <ip>` (cookie-jar HTTPS to :8443).
- ✅ gofmt + go build/vet/test ./... green.

### ⚠️ Live-validation trigger + deferrals
Parser tested against sample UniFi JSON; not validated against a real
controller (login varies: legacy `/api/login` vs UniFi-OS `/api/auth/login`).
**Omada + Ruckus deferred** — distinct APIs (Omada needs an Omada-ID + token;
Ruckus SmartZone is a different REST surface), each its own future phase.

## SNMP v3 (USM) — BACKLOG-SNMPV3 ✅ (closed 2026-06-03)

Extends the proven SNMP transport to v3 (RFC 3414 USM) rather than guessing
new vendor shapes — well-specified + solid gosnmp support, so the config
build is genuinely testable.

- ✅ `internal/snmp` — `Target.V3` (`V3Params`: security name + auth/priv
  protocol+key) + `NewClient` builds gosnmp Version3 + UserSecurityModel +
  `toV3` (MsgFlags from key presence: noAuth→authNoPriv→authPriv; **priv
  requires auth**; unknown protocol strings fall back to SHA/AES).
  `ParseV3JSON` decodes the credential blob. Tests: authPriv/authNoPriv/
  noAuthNoPriv, priv-without-auth guard, protocol-mapping defaults,
  SecurityLevel, v3-requires-params.
- ✅ Discovery pipeline auth loop builds a v3 `Target` for `snmp_v3` candidates
  (decrypt closure parses the JSON blob into `V3Params`); monitoring
  `probeSNMP` gained a v3 branch (`Poller.ProbeSNMPv3`) so SNMP-metric checks
  work against v3 devices too. Both collector + API decrypt closures handle
  the v3 blob.
- ✅ UI: Credentials create form shows v3 USM fields (security name, auth/priv
  protocol + key) when kind=`snmp_v3`, assembling the JSON the server seals.
- ✅ gofmt + go build/vet/test ./... green; frontend tsc + build green.

### Credential secret encodings (now documented)
- `snmp_v2c`: community string. `snmp_v3`: JSON `{security_name,auth_protocol,
  auth_key,priv_protocol,priv_key}`. `ssh`/`winrm`/`http_basic`/`onvif`/
  `vendor_api`: `username:password`.

### ⚠️ Live-validation trigger
v3 config-building + blob parsing are unit-tested; the USM handshake needs a
real v3 device. Trigger: first v3 credential bound on the fleet (Aruba/Cisco
switches commonly support v3).

## Peripherals: printers via SNMP + shared-SNMP-session fix ✅ (closed 2026-06-03)

Spec-named peripherals phase (printers), dependency-free + fully testable —
and it surfaced a pre-existing cross-cutting bug, fixed here.

- ✅ `internal/driver/printer` — `printer_snmp`: banner/sysDescr or port-9100
  fingerprint → printer; `CollectSupplies` walks Printer-MIB
  prtMarkerSuppliesTable (level+capacity → pct, honoring the -2 unknown / -3
  some-remaining sentinels) + prtMarkerLifeCount (lifetime pages). Tested with
  a fake SNMP client. Registered.
- ✅ migration 000019 `printer_supplies`; `Facts.PrinterSupplies` +
  `printer.page_count`; apply persists (upsert + stale-prune). API +
  **Printers** nav + PrinterDetail (level bars + page count).

### ⚠️ Cross-cutting bug found + FIXED: SNMP driver session type
Every SNMP driver defined its **own** `Session` struct and asserted to it, but
the pipeline only ever built an `aruba.Session` — so deep SNMP collection
**silently failed for cisco/huawei/host_snmp/fortigate/esxi** (and would for
printer). Never surfaced because the persist path didn't exist until this
session. **Fixed**: a shared `swsnmp.Session` the pipeline builds, with each
driver's `Session` now a type **alias** to it (zero test churn). All SNMP
drivers now collect through the pipeline.

### Live-validation trigger
Printer-MIB parsing is unit-tested; validate against a real printer once a
credential is bound. UPS-MIB + voice remain as future peripherals phases.

## Peripherals: UPS via SNMP (UPS-MIB) ✅ (closed 2026-06-03)

- ✅ `internal/driver/ups` — `ups_snmp`: sysDescr/banner keyword fingerprint
  (APC/Eaton/Liebert/Smart-UPS/… → ups, conf 68); Collect GETs UPS-MIB scalars
  (manufacturer, model, battery status 1-4→normal/low/depleted/unknown, charge
  %, est. runtime min) + walks upsOutputPercentLoad (max line). `Session`
  aliases `swsnmp.Session` (the shared type). Tested with a fake SNMP client
  (on-battery-low: identity, status mapping, charge/runtime, max-load).
  Registered.
- ✅ migration 000020 `ups_status` + queries; `Facts.UPS` + apply persists.
  API `/devices/{id}/ups`; UI **UPS** nav + UPSDetail (battery badge, charge,
  runtime, load).
- ✅ gofmt + go build/vet/test ./... green; frontend tsc + build green.

### Live-validation trigger
UPS-MIB parsing is unit-tested; validate battery/runtime/load against a real
UPS once a credential is bound. Voice (CUCM/IP-phones) remains the last
peripherals/voice sub-area.

## AD-import — LDAP computer-object discovery scope ✅ (closed 2026-06-03)

The AD-primary Windows discovery the spec calls for, as an **optional**
accelerator alongside IP-range scanning (HIMS stays AD-independent).

- ✅ `internal/adimport` — `Searcher` interface (go-ldap, injectable) +
  `SearchComputers(base DN)` + pure `ParseComputers` (sAMAccountName/cn,
  dNSHostName, operatingSystem, userAccountControl→enabled) + `classifyOS`
  (Server→server, else endpoint). Tested with hand-built `ldap.Entry`s:
  server-vs-endpoint, disabled-bit (UAC 0x2), cn-fallback + nameless-skip,
  baseDN passthrough, UAC logic.
- ✅ collector `-adimport <dc-host> -basedn <DN> [-location]` — resolves an
  `ldap` credential (`bindUser:password`) scoped to the DC, binds, searches the
  OU subtree, resolves each computer's dNSHostName → IPv4, and applies via the
  existing worker (reconcile by primary_ip+location). Computers without a
  resolvable IP are skipped + counted.
- ✅ apply now derives category from `Match.Category` even with no driver
  matched (so AD's OS-based category sticks); no schema/UI change — imported
  devices appear in the Servers list, Roles, dashboard, and search.
- ✅ go mod tidy; gofmt + go build/vet/test ./... green.

### ⚠️ Live-validation trigger
Entry-parsing + classification are unit-tested; the LDAP bind/search + DNS
resolution need a real domain. Trigger: first AD credential bound — validate
against the fleet DC (LDAPS/port 636 + paged results for >1000 objects are the
likely real-world extensions v1 omits).

## Status — full platform + onboarding + ALL 4 deep deps + SNMP v3 + peripherals + AD-import
The entire requested scope is shipped, green, committed. Drivers:
aruba/cisco/huawei (switch SNMP), fortigate (firewall), host_snmp +
vmware_esxi (servers/virt SNMP), cctv + wlan_controller (banner), redfish_bmc
(HTTP), vmware_vsphere (govmomi), hyperv (WinRM), onvif_camera (SOAP), unifi
(REST). Engines: discovery pipeline + persist worker + CIDR scan, credential
resolver + AES-256-GCM crypto, monitoring (TCP + SNMP) + alerting + work-order
bridge, topology, operations (work orders/parts/purchases/expenses/licenses),
MIB upload, reporting/dashboard. Remaining = explicitly deferred-with-trigger:
SNMP v3; Omada/Ruckus REST; scan job-record API/UI; AD-import; per-physical-
drive (Redfish) + GetStreamUri (ONVIF) + vCenter-multi-host (vSphere); and the
live-hardware validations for every credentialed collector.
