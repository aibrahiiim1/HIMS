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

## Status — explicitly-listed roadmap complete
Every phase the operator queued (3b/3c, CCTV, Wireless, Databases/AD, MIB
engine, reporting/dashboards, the persist-worker integrator, and the bounded
follow-ups) is shipped, green, and committed. Remaining work is all
**deferred-with-trigger** deep-collection transports (vSphere/govmomi,
Hyper-V/WinRM, iLO/iDRAC Redfish, ONVIF, vendor REST), SNMP v3, peripherals/
voice drivers, and range/CIDR + AD-import discovery driving the apply worker.
