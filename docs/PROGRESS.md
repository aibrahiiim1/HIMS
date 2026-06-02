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

## Later phases ⬜
See `PLAN.md` §10 (compute, firewall, CCTV, wireless, databases/AD,
peripherals/voice, operations layer, MIB engine + reporting).
