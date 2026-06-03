# HIMS — Hotel Infrastructure Management System

A platform for the Coral Sea Resorts hotel group to **discover, inventory,
monitor, map, and operate** all IT infrastructure across every hotel:
network gear, servers, virtualization, storage, CCTV, voice, databases,
directory services, peripherals — plus the operations layer on top
(work orders, spare parts, purchases, expenses, licenses & contracts).

It is built as **engines and modules**, not CRUD pages:

- **CMDB core** — a generic device model + multi-role devices + per-driver
  facts, hung off a real location tree (Hotel Group → Hotel → Building →
  Floor → Area → Room → Rack).
- **Driver/Plugin engine** — vendor specifics live in drivers
  (`aruba_hpe`, `cisco_ios`, `fortigate`, `hikvision`, `vmware`, …); the
  core never references a vendor.
- **Discovery engine** — a pipeline: detect → fingerprint → resolve
  credentials → authenticated probe → classify → template-match → deep
  collect → relationship-map → inventory → register monitoring.
- **Credential Resolver** — Site → Subnet → Credential Groups; bind on
  first success, never re-prompt.
- **Monitoring engine** — separate cadence, TimescaleDB-backed metrics.
- **Topology engine** — multi-source (LLDP/CDP + MAC + ARP + VLAN +
  controller/hypervisor/NVR maps) with IP/MAC/name → port → path search.
- **Operations layer** — work orders, spare parts, purchases, expenses,
  licenses/contracts; alerts can spawn work orders.

## Stack
Go (API / collector / drivers) · PostgreSQL + TimescaleDB · NATS ·
React + WebSocket · Cytoscape.js (topology) · ECharts (dashboards).

## Status
Greenfield, under active construction. See:
- [`docs/PLAN.md`](docs/PLAN.md) — full architecture + phased roadmap.
- [`docs/PROGRESS.md`](docs/PROGRESS.md) — what's done, per phase.
- [`docs/HANDOVER.md`](docs/HANDOVER.md) — how to build/run + how to continue.
- [`docs/RUNBOOK.md`](docs/RUNBOOK.md) — per-collector operational runbook + live-validation criteria.

## Build
```
go build ./...
go vet ./...
go test ./...
```
Integration tests (real Postgres) are gated behind `-tags=integration`.
