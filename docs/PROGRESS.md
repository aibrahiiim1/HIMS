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

## Phase 1 — Switches + Topology + Credential Resolver 🚧 (next)
_Aruba/HPE driver `Collect` over an SNMP transport (interfaces/VLANs/MAC/
LLDP/port-roles), discovery pipeline wired to the resolver bridge, topology
engine (LLDP/CDP + MAC + ARP), IP/MAC/name → port → path search, topology
graph UI. Builds directly on the Phase 0 driver engine + resolver._

---

## Later phases ⬜
See `PLAN.md` §10 (compute, firewall, CCTV, wireless, databases/AD,
peripherals/voice, operations layer, MIB engine + reporting).
