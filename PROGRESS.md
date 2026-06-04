# HIMS Enterprise Roadmap — Progress

Living tracker for the 31-item "high-end NMS" program. Updated as each step lands.
Branch: `feat/enterprise-sidebar-redesign`. All work uses **real data only** (no demo/fake), every commit builds + passes `go vet`/`eslint`, and features are verified live before commit.

Legend: ✅ done · 🟡 core done, richer phase remaining · 🟠 basic exists, needs "Pro" build · 🔴 not started

---

## Status by item

| # | Feature | Status | Notes |
|---|---------|--------|-------|
| 1 | NOC Wallboard | ✅ | TV view, 30/60s auto-refresh, fullscreen, dark — `c2ef971` |
| 2 | Device Path Finder | ✅ | L2 path + MAC/ARP/LLDP source + confidence — `d4a4694` |
| 3 | Switch Port Mapping Pro | ✅ | rack faceplate, filters, CSV export — `a5ed197` (live counters need #5b) |
| 4 | Topology Intelligence | ✅ | link build+resolve (mgmt-IP + sysName) + auto-rebuild + **stale-link pruning** + **link confidence** (LLDP/CDP/MAC/ARP × freshness) + **layer detection** (core/distribution/access/edge/wireless/host) + `/topology/graph` + layer-coloured map with neighborhood focus — `6284a2c`,`f67b7a1` + finalize |
| 5 | Real Monitoring Engine | ✅ | in-process continuous availability+latency time-series + SNMP scalar (value_num) — `86cf918`; **#5b** per-device Performance view (uptime %, latency avg/min/max + trend, status timeline, SNMP-value trend) over real samples. Tabular interface/host counters (util/errors/CPU/mem as series) are a documented collector-walk enhancement (needs reachable SNMP) — noted below. |
| 6 | Alert Engine | ✅ | rules+dedup+auto-resolve+WO bridge + **maintenance windows (suppression)** + **alert timeline** + **ack-by-actor + notes** + **escalation** + status/severity filters — `7b7a0ee` (backend) + frontend |
| 7 | Notifications | ✅ | Slack/Teams/Telegram/webhook/email channels (encrypted targets), severity + quiet-hours filtering, background dispatcher (dedup), test button, delivery log — `eb39a9e` (backend) + frontend |
| 8 | Device Templates Engine | 🟠 | CRUD exists; needs OID sets, default monitors/alerts, health rules |
| 9 | Vendor Fingerprint Library | 🟠 | CRUD exists; needs comprehensive multi-source library |
| 10 | Config Backup | 🔴 | SSH backup, versions, diff, drift detection |
| 11 | Config Drift / Change Tracking | 🔴 | depends on #10 |
| 12 | NetFlow / sFlow Analytics | 🔴 | top talkers, protocols, bandwidth |
| 13 | Wireless Intelligence | 🟡 | **data-gated** — no wireless controllers/APs collected in this fleet (access_points=0); detail UI ready, needs a controller collection run |
| 14 | Firewall Intelligence | ✅ | FortiGate Pro UI: HA/sessions/VPN-up-total/members KPIs, status, CPU/mem/disk meters, VPN tunnel table (up/down + traffic), cluster members, licenses, interfaces — verified live (3 FortiGates, 36 tunnels) |
| 15 | Server Intelligence | ✅ | virtualization-host Pro UI: hypervisor/CPU/memory/datastores KPIs, CPU/mem/disk meters, uptime, datastore usage table, VM inventory — real data (2 ESXi hosts, 13 datastores). Windows services/processes/event-logs/software/patch deferred to the Windows agent (per memory). |
| 16 | Camera / NVR Intelligence | 🟡 | **data-gated** — 116 cameras discovered but camera_info/nvr_channels=0 (no ONVIF deep-collect yet); detail UI ready, needs an ONVIF collection run |
| 17 | Voice / PBX Intelligence | 🟡 | **data-gated** — no PBX/CUCM in this fleet (pbx_phones=0); detail UI ready, needs a CUCM/AXL collection run |
| 18 | Asset Lifecycle | 🔴 | warranty/EOL/owner/cost/maintenance history |
| 19 | Work Orders Integration | 🟠 | WOs exist; needs device/alert linking, SLA, history |
| 20 | Maintenance Windows | ✅ | delivered as part of #6 — global/site/device scoped, time-bounded, suppress alert firing, auto-expire; CRUD UI on Alerts → Maintenance |
| 21 | Reports Pro | 🟠 | base reports exist; needs scheduled/PDF/Excel/email |
| 22 | Multi-Site / Hotel View | 🔴 | **blocked**: all 62 devices unassigned to a site |
| 23 | RBAC | 🟠 | users/roles/permissions exist; needs site-scope + matrix |
| 24 | Audit Trail | 🟡 | log + many events; needs deeper filters/coverage |
| 25 | Backup & Restore | 🔴 | DB backup, restore validation, DR checklist |
| 26 | Installer / Deployment | 🟡 | dev script + Windows service scripts + deploy docs exist |
| 27 | System Health / Self-Monitoring | ✅ | System Health page + `/system/runtime` + startup checklist — `9aaa583` |
| 28 | Data Quality Center | ✅ | duplicates/missing/stale/conflicts — `2c716ee` |
| 29 | Search Pro | ✅ | recent searches, quick actions, multi-type — `ced4165` |
| 30 | API Documentation | 🔴 | OpenAPI/Swagger + integration guide |
| 31 | Import / Export | 🟠 | CSV/Excel import + port export exist; needs full export set |

Supporting fixes this program: runtime key unlock (`3ef8293`), single-instance guard (`9aaa583`).

---

## Known real findings (surfaced by the new tooling)
- All 62 devices are **unassigned to a site** → blocks #22; flagged by Data Quality.
- **No recent discovery** (`last_discovery_at` stale fleet-wide).
- Monitoring flagged **default-port mismatches** (e.g. :443 probed on SSH-only switches) → false warnings + high latency; tuning win.

---

## Deferred (documented, not silently dropped)
- **#5b tabular counters**: per-interface utilization/errors/discards/CRC and host CPU/mem/disk as time-series need the collector to walk IF-MIB ifXTable / HOST-RESOURCES-MIB on an interval and store per-subject samples. The time-series store + scalar SNMP path + visualization are done; the tabular walk is a collector-side enhancement that requires reachable SNMP devices to build+verify. Switch Port Pro shows these columns as "not collected" until then.
- **#4 MAC/ARP-derived edges**: engine rates MAC/ARP links lower when present but does not synthesize brand-new edges purely from FDB heuristics (false-topology risk).

## Changelog
- Created progress tracker. Starting **#6 Alert Engine** (full finalization).
- **#6 Alert Engine backend** (`7b7a0ee`): migration 000029 (maintenance_windows + alert_events + alerts.acknowledged_by/escalated + alert_rules.escalate_after_minutes); engine suppression + timeline events + escalation; ack-by-actor; CRUD/timeline/note/maintenance endpoints; suppression unit test. Verified live (31 alerts opened, timeline, ack-by-alice, global window → 31 suppressed).
- **#6 Alert Engine frontend**: Alerts page rebuilt with Alerts/Rules/Maintenance tabs, escalated KPI+badge, status filter, alert timeline drawer + notes, maintenance-window scheduler/list, rule escalation field. **#6 and #20 complete.**
- **#15 Server Intelligence**: VirtualHostDetail rebuilt to the enterprise design over the real ESXi hosts — hypervisor/CPU/memory/datastore KPIs, CPU/mem/disk meters, uptime, datastore usage table (per-datastore used/total + meter), and VM inventory. Windows-specific depth (services/processes/event logs/installed software/patch) stays with the future Windows agent. **#15 complete (virtualization).**
- **#14 Firewall Intelligence**: FirewallDetail rebuilt to the enterprise design — KPI header (HA mode / active sessions / VPN up-total / cluster members), Firewall Status DefList, CPU/mem/disk meters, VPN tunnel table (up/down + in/out traffic), cluster members, licenses, interfaces. Verified live on a FortiGate (195k sessions, 10/12 tunnels up). **#14 complete.**
- **#13/#16/#17 status**: domain *collectors* exist (built phases 7/8/9/voice) but this fleet has no collected wireless/ONVIF/CUCM detail data (access_points/camera_info/nvr_channels/pbx_phones all 0; though 116 cameras are discovered). Their detail UIs render-on-data; building richer empty views would be unverifiable polish, so they're marked data-gated pending a collection run rather than shipped blind.
- **#5b Monitoring time-series viz**: device-detail Monitoring tab rebuilt into a Performance & Availability view — uptime % (windowed), latency avg/min/max + trend sparkline, a per-sample status timeline strip, and an SNMP-metric (value_num) trend — all over the real monitoring_samples series (2,510+ samples accumulated live). **#5 complete** (tabular counters deferred + documented above).
- **#4 Topology Intelligence finalized**: `/topology/graph` with layer detection (core/distribution/access by link degree + category → edge/gateway/wireless/host), link confidence (LLDP/CDP=high, MAC=medium, ARP=low, downgraded when stale), undirected edge dedup, stale-link pruning (>7d, on every rebuild), and a layer-coloured cytoscape map with click-to-focus neighborhood. Verified live: 44 nodes / 44 deduped edges, layers core 1 / dist 23 / access 20. Engine unit test covers layer + confidence + dedup. **#4 complete.**
- **#7 Notifications** (`eb39a9e` backend + frontend): internal/notify senders (Slack/Teams/Telegram/webhook/email) + pure decision logic (severity + quiet hours, unit-tested); channels with AES-GCM-encrypted targets (never returned), background dispatcher with one-delivery-per-(channel,alert) dedup, test endpoint, delivery log; Administration → Notifications page (per-type channel form, test button, log). Verified: encrypted-at-rest blob, metadata-only DTO, test path executes + logs. **#7 complete.**
