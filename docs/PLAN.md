# HIMS — Plan & Architecture

> The authoritative roadmap. Engines + modules, not CRUD. This file is the
> "what and why"; `PROGRESS.md` is the "what's done"; `HANDOVER.md` is the
> "how to run / continue".

## 0. Vision

A Hotel Group IT Infrastructure Management Platform = IT Asset Discovery +
Network Monitoring + Topology + CMDB + Work Orders + Licenses + Expenses +
Mini ITSM. Built for a multi-hotel fleet under one umbrella.

The system hierarchy:

```
Hotel Group
 └── Hotel
      └── Building
           └── Floor / Area / Room / Rack / Office
                └── Device / Asset
```

A device may be any of: switch, router, firewall, access point, wireless
controller, server, virtualization host, virtual machine, storage, NVR/DVR,
camera, printer, IP phone, PBX / call manager, voice gateway, database
server, domain controller, DHCP/DNS server, fingerprint device, user
endpoint, UPS, ISP/internet line, application system, license, service
contract.

## 1. Architectural principles

1. **Generic core, vendor-specific drivers.** Never `CiscoSwitch` /
   `HuaweiSwitch` tables. Core tables are `devices`, `interfaces`, `vlans`,
   `mac_addresses`, `neighbors`, `metrics`. Vendor detail lives in
   `device_facts` / `vendor_payloads` / `device_raw_snapshots`.
2. **Base device + extension templates.** Every device shares a base model;
   each category (switch / firewall / server / camera / …) has its own
   template describing the sections its detail page renders.
3. **Multi-role devices.** One device can be `Hyper-V Host` + `Domain
   Controller` + `DNS` + `DHCP` simultaneously (`device_roles`).
4. **Engines, not pages.** Discovery, classification, monitoring, topology,
   alerting, operations are engines with clear contracts.
5. **Credentials resolved, not picked.** Site → Subnet → Credential Groups.
   Bind on first success; never make the operator re-pick.
6. **Realistic scanning.** Staged: light discovery → classification →
   credential probe (matching creds only, no brute force) → deep collect →
   monitoring registration. Respect subnet credential scope.

## 2. The engines

1. **Discovery Engine** — pipeline (below).
2. **Credential Resolver Engine** — Site/Subnet/Group → ordered candidates,
   bind on success.
3. **Device Classification Engine** — fingerprint → category + roles.
4. **Device Template Engine** — category → detail-page sections.
5. **Monitoring Engine** — periodic polling, metrics, history.
6. **Topology Engine** — multi-source link + path computation.
7. **Alerting Engine** — rule-based; alert → optional work order.
8. **Work Order Engine** — asset-linked tickets.
9. **License & Contract Engine** — expiry tracking + alerts.
10. **Expense Engine** — contracts/internet/licenses/repairs/parts.
11. **Reporting Engine** — by hotel / system / vendor / category / asset.
12. **Plugin / Driver Engine** — per-vendor drivers (the heart).

### Discovery pipeline
```
Input Scope → Host Detection → Port/Protocol Fingerprint →
Credential Resolver → Authenticated Probe → Device Classification →
Template Matching → Deep Collection → Relationship Mapping →
Inventory Update → Monitoring Registration
```

### Discovery inputs
Single IP · IP range · CIDR · hotel-site subnets · AD import (OUs) · CSV ·
controller import (UniFi/Ruckus/Omada/ESXi) · manual add.

### Driver contract (every vendor implements)
```
Fingerprint(probe)      → match + confidence
Authenticate(creds)     → session
Collect(session)        → normalized facts (+ raw snapshot)
Template()              → which device template to apply
```
Planned drivers: cisco_ios, cisco_cucm, huawei_vrp, aruba_hpe, extreme,
mikrotik, fortigate, sophos, hikvision, dahua, qnap, synology, vmware,
hyperv, windows, linux, unifi, omada, ruckus, zkteco.

## 3. Protocols
ICMP · TCP/UDP scan · SNMP v1/v2c/v3 · SSH · WinRM/WMI · HTTP(S)
fingerprint · ONVIF · RTSP · LLDP/CDP · ARP · MAC/FDB · DNS/rDNS ·
mDNS · SSDP/UPnP · NetBIOS · SMB · LDAP/AD · VMware API · Hyper-V/WMI ·
vendor REST (UniFi/Omada/Ruckus/FortiGate/Sophos/Hikvision/Dahua).

## 4. Device templates (detail-page sections)
Switch (interfaces/VLANs/MAC/ARP/LLDP/port-role/PoE/STP/stack/config/topology),
router, firewall (HA/VPN/sessions/policies/UTM-license/WAN/routing),
server, virtualization host (+ VMs), iLO/iDRAC, storage (QNAP/Synology),
NVR/DVR (+ channels/cameras), camera (ONVIF/RTSP), printer (toner/counters),
PBX/voice (CUCM/Alcatel/gateway), wireless controller (+ APs/SSIDs),
database (SQL/Oracle/PostgreSQL), AD/DNS/DHCP. See spec for per-section detail.

## 5. Topology & search
Links from: LLDP/CDP + MAC table + ARP + VLANs + switch ports + WLC AP map
+ hypervisor VM map + NVR camera map.
Search by **IP / MAC / name** →
`ARP → MAC → switch+port+VLAN → LLDP uplink path → building/department`.
Headline: full physical path (PC → access-sw → core → firewall → ISP).

## 6. Monitoring & alerting
Discovery (daily/weekly) is distinct from Monitoring (30s–1h per profile).
Alerts are rule-based; an alert may spawn a Work Order (e.g. camera offline
→ ticket assigned to IT).

## 7. Operations layer
Work Orders (asset-linked, with diagnosis/action/parts/cost/timeline),
Spare Parts (stock + min-qty + usage decrement), Purchase records,
Expenses (by hotel/system/vendor/category/asset), Systems & Licenses
(expiry + support-contract + cost + status, with 90/60/30/7-day alerts).

## 8. MIB upload engine
Upload MIB → parse OIDs → OID library → map OID → template metric →
bind to vendor/template → test SNMP OID. (Upload alone ≠ auto-understanding;
the value is the mapping + binding.)

## 9. Data model (high level)
hotels/buildings/floors/locations (one typed location tree), subnets,
credentials, credential_groups, credential_bindings, discovery_jobs,
discovery_results, devices, device_roles, device_facts, device_templates,
interfaces, vlans, mac_addresses, arp_entries, lldp_neighbors,
topology_links, servers, virtual_hosts, virtual_machines, storage_volumes,
cameras, nvr_channels, printers, pbx_extensions, wireless_controllers,
access_points, databases, licenses, systems, work_orders, spare_parts,
purchase_records, expenses, alerts, alert_rules, monitoring_checks,
monitoring_samples (TimescaleDB hypertable).

## 10. Phased roadmap

- **Phase 0 — Foundation.** Repo + CI; CMDB schema (location tree, subnets,
  devices + roles + facts); driver-engine contract + registry; credential
  resolver model + logic; core domain interfaces + Postgres repos; docs.
- **Phase 1 — Switches + Topology + Credential Resolver.** First driver =
  **Aruba/HPE** (22 of 26 switches in the real fleet). Switch template,
  discovery pipeline wired to the resolver, topology engine, IP/MAC/name →
  path search, topology graph UI. End-to-end proof of the architecture.
- **Phase 2 — More switch drivers** (Cisco IOS, Huawei VRP) + topology
  hardening. ✅ DONE — shared `swsnmp` collectors, Cisco (CDP) + Huawei
  drivers, LLDP/CDP neighbor merge. No schema/UI change (driver-engine win).
- **Phase 3 — Compute:** servers (SNMP/WinRM/SSH), then virtualization
  (ESXi/Hyper-V + VM mapping), iLO/iDRAC.
  - **3a ✅ DONE** — servers via SNMP HOST-RESOURCES (CPU/RAM/disk +
    interfaces), `host_snmp` driver, port-based role inference, server
    template UI.
  - **3b/3c (open)** — virtualization (vSphere/WinRM) + VM→host mapping;
    iLO/iDRAC via Redfish. Both need new transports.
- **Phase 4 — Firewall** (FortiGate driver — port the proven Fortinet OID
  work: HA/VPN/sessions/CPU-RAM-disk/license). ✅ DONE — fortigate driver
  with every validated MIB lesson baked in (disk-MB, VPN composite index,
  Counter64 octets, fgHaGroupName OID, HA-count-from-rows) + firewall
  template UI. SNMP transport; no new infra.
- **Monitoring Engine** (built out of order — the platform's live-state
  spine). ✅ **6 core DONE** — `monitoring_checks` + `monitoring_samples`
  (TimescaleDB best-effort), pure hysteresis evaluator (up→warning→down +
  device rollup), TCP-reachability poller, scheduled collector loop
  (`-monitor`/`-seed`), API + Monitoring UI. **6B (open)** — SNMP-metric
  checks (need credential-decrypt in the collector) + alert rules over
  samples → alert→work-order bridge.
- **Phase 5 — CCTV:** NVR/DVR + cameras (ONVIF/RTSP/vendor API).
- **Phase 6 — Wireless controllers + APs** (UniFi/Omada/Ruckus REST).
- **Phase 7 — Databases** (SQL/Oracle/PostgreSQL) + AD/DNS/DHCP.
- **Phase 8 — Peripherals** (printers/UPS/fingerprint/IP phones) + voice.
- **Phase 9 — Operations layer:** work orders → spare parts → purchases →
  expenses → licenses/contracts; alert → work-order bridge.
  - **A ✅ DONE** (built early — pure CRUD, high value): work orders
    (asset-linked, lifecycle, timeline, cost) + Systems & Licenses register
    (live expiry status).
  - **B ✅ DONE** — spare parts (stock + reorder threshold + atomic
    work-order consumption decrement), purchase ledger, expense rollups
    (by category / location, derived from the ledger). The
    **alert→work-order bridge** is the one remaining piece and moves to
    Monitoring 6B (it needs an alert source).
- **Phase 10 — MIB upload engine, reporting, executive dashboards.**

Ordering rationale (operator): switches + topology + credential resolver
are the heart — build them first, then breadth.

## 11. Working discipline
Each phase: plan → build in small commits → `go build/vet/test` (+ frontend
tsc/lint/build) green → close in PROGRESS.md. Integration tests gated behind
`-tags=integration` so the default suite stays DB-free. Credentials are
encrypted at rest and never logged or surfaced.
