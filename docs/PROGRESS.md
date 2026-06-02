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

## Later phases ⬜
See `PLAN.md` §10. Remaining: **3b/3c** (virtualization + iLO/iDRAC — new
transports), CCTV, wireless, databases/AD, peripherals/voice, operations
layer (work orders → spare parts → purchases → expenses → licenses), MIB
upload engine + reporting/dashboards.
