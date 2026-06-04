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
| 5 | Real Monitoring Engine | 🟡 | in-process continuous availability+latency time-series + SNMP scalar — `86cf918`; remaining #5b: CPU/mem/disk/temp + interface counters schema+collection |
| 6 | Alert Engine | ✅ | rules+dedup+auto-resolve+WO bridge + **maintenance windows (suppression)** + **alert timeline** + **ack-by-actor + notes** + **escalation** + status/severity filters — `7b7a0ee` (backend) + frontend |
| 7 | Notifications | ✅ | Slack/Teams/Telegram/webhook/email channels (encrypted targets), severity + quiet-hours filtering, background dispatcher (dedup), test button, delivery log — `eb39a9e` (backend) + frontend |
| 8 | Device Templates Engine | 🟠 | CRUD exists; needs OID sets, default monitors/alerts, health rules |
| 9 | Vendor Fingerprint Library | 🟠 | CRUD exists; needs comprehensive multi-source library |
| 10 | Config Backup | 🔴 | SSH backup, versions, diff, drift detection |
| 11 | Config Drift / Change Tracking | 🔴 | depends on #10 |
| 12 | NetFlow / sFlow Analytics | 🔴 | top talkers, protocols, bandwidth |
| 13 | Wireless Intelligence | 🟡 | controllers/APs collected; needs rich UI |
| 14 | Firewall Intelligence | 🟡 | status/VPN/HA/licenses collected; needs rich UI |
| 15 | Server Intelligence | 🟡 | CPU/RAM/disk/storage via SNMP; needs services/processes/events |
| 16 | Camera / NVR Intelligence | 🟡 | ONVIF collected; needs channels/recording/lost-video |
| 17 | Voice / PBX Intelligence | 🟡 | CUCM phones collected; needs registration/gateways/trunks |
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

## Changelog
- Created progress tracker. Starting **#6 Alert Engine** (full finalization).
- **#6 Alert Engine backend** (`7b7a0ee`): migration 000029 (maintenance_windows + alert_events + alerts.acknowledged_by/escalated + alert_rules.escalate_after_minutes); engine suppression + timeline events + escalation; ack-by-actor; CRUD/timeline/note/maintenance endpoints; suppression unit test. Verified live (31 alerts opened, timeline, ack-by-alice, global window → 31 suppressed).
- **#6 Alert Engine frontend**: Alerts page rebuilt with Alerts/Rules/Maintenance tabs, escalated KPI+badge, status filter, alert timeline drawer + notes, maintenance-window scheduler/list, rule escalation field. **#6 and #20 complete.**
- **#4 Topology Intelligence finalized**: `/topology/graph` with layer detection (core/distribution/access by link degree + category → edge/gateway/wireless/host), link confidence (LLDP/CDP=high, MAC=medium, ARP=low, downgraded when stale), undirected edge dedup, stale-link pruning (>7d, on every rebuild), and a layer-coloured cytoscape map with click-to-focus neighborhood. Verified live: 44 nodes / 44 deduped edges, layers core 1 / dist 23 / access 20. Engine unit test covers layer + confidence + dedup. **#4 complete.**
- **#7 Notifications** (`eb39a9e` backend + frontend): internal/notify senders (Slack/Teams/Telegram/webhook/email) + pure decision logic (severity + quiet hours, unit-tested); channels with AES-GCM-encrypted targets (never returned), background dispatcher with one-delivery-per-(channel,alert) dedup, test endpoint, delivery log; Administration → Notifications page (per-type channel form, test button, log). Verified: encrypted-at-rest blob, metadata-only DTO, test path executes + logs. **#7 complete.**
