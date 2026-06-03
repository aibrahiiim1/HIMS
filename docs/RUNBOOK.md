# HIMS — Collector Runbook & Live-Validation Guide

> Operational runbook for every HIMS collector: how to run it, what it needs,
> what it should populate, where to look when it fails, and the exact criteria
> for marking a collector **production-validated**.
>
> Companion docs: `PLAN.md` (architecture), `PROGRESS.md` (what's built + each
> collector's ⚠️ live-validation trigger), `HANDOVER.md` (build/run basics).

---

## 0. Conventions used in this runbook

- **Collector** = a `hims-collector` run mode (one CLI flag).
- All collectors **persist** to the CMDB via the shared `internal/apply`
  reconciler: a device is matched/created by `(primary_ip, location)`, the
  credential that authenticated is bound to it (`SetDeviceCredential`), roles
  are inferred, and inventory rows are upserted with `last_seen_at = pollStart`
  then stale rows are pruned. Re-running a collector is **idempotent**.
- **Probes are read-only.** No collector writes device configuration. The only
  DB writes are to the HIMS CMDB.
- **Credentials are encrypted at rest** (AES-256-GCM, key from
  `HIMS_ENCRYPTION_KEY`); they are never logged, never returned by the API,
  never rendered in the UI.
- **No per-scan credential pickers.** Collectors resolve credentials through
  the Site → Subnet → Credential-Group resolver and bind the first that
  authenticates. You provision credential *groups*; you do not hand a
  collector a specific username/password on the command line.

### Credential kinds & secret encodings

| Kind (`credentials.kind`) | Secret encoding (plaintext before sealing) | Used by |
|---|---|---|
| `snmp_v2c`   | community string                               | discovery, printers, UPS |
| `snmp_v3`    | JSON `{securityName,authProtocol,authKey,privProtocol,privKey}` | discovery (v3) |
| `ssh`        | `username:password`                            | manual CLI probe |
| `winrm`      | `username:password`                            | Hyper-V |
| `http_basic` | `username:password`                            | Redfish, UniFi, Omada, Ruckus, Extreme, CUCM |
| `onvif`      | `username:password`                            | ONVIF cameras/NVRs |
| `vendor_api` | `username:password`                            | UniFi/Omada/Ruckus/Extreme/CUCM (alt to http_basic) |
| `ldap`       | `username:password` (bind DN or UPN)           | AD import |

### Global prerequisites

```
HIMS_DATABASE_URL   postgres://hims:hims@host:5432/hims?sslmode=disable
HIMS_ENCRYPTION_KEY 32-byte key (base64/hex per secret.NewCipher) — REQUIRED to
                    decrypt bound credentials; without it collectors that need a
                    credential cannot run and the API disables credential writes.
```

Credential groups must already exist and be scoped to the site/subnet of the
target. Bind a `location` UUID with `-location <uuid>` so devices land in the
right site scope.

### Generic failure reasons (apply to every collector)

| Symptom | Cause | Fix |
|---|---|---|
| `no credential resolved` | no enabled credential of the required kind is scoped to the target IP/site | add/scope a credential group; check the group's subnet CIDR |
| `database unavailable` / apply fails | `HIMS_DATABASE_URL` wrong or DB down | verify the pool URL; `hims-api` logs `database unavailable at startup` |
| credential decrypt fails | `HIMS_ENCRYPTION_KEY` missing or rotated without re-key | set the key; if rotated, run `hims-collector -rekey` |
| device created but no inventory | auth succeeded but the deep-collect API/endpoint/version mismatched | see the per-collector "Common failure reasons" |

### Global log locations

- **Collector**: stdout, `slog` text handler. Key lines are
  `slog.Error("<collector>: <stage> failed", ...)` and the final
  `fmt.Printf("<Collector> <ip> persisted as device <uuid> (...)")` success line.
- **API**: stdout, `slog` **JSON** handler. Startup logs
  `drivers registered`, `credential encryption enabled key_id=...`, and
  `hims-api starting addr=...`.
- Secrets/communities never appear in any log line by design.

---

## 1. Discovery — switches / servers / firewalls (SNMP)

| Field | Value |
|---|---|
| **Credential type** | `snmp_v2c` (community) or `snmp_v3` (USM JSON) |
| **Permissions / role** | SNMP read-only community / USM user with read view |
| **Ports / protocols** | UDP **161** (SNMP); TCP probe set 22, 23, 53, 80, 88, 161, 389, 443, 1433, 1521, 3389, 5432, 8080 |
| **Example command** | `hims-collector -discover 10.20.0.10 -location <site-uuid>` |
| | `hims-collector -scan 10.20.0.0/24 -location <site-uuid> -concurrency 16` |
| **Expected output** | `IP 10.20.0.10 — alive=true ports=[22 161] / classified: driver=cisco_ios category=switch confidence=90` then a persist line |
| **Tables populated** | `devices`, `device_credentials`, `device_roles`, `device_facts`, `interfaces`, `vlans`, `mac_entries`, `neighbors`, `server_storage` (per driver category) |
| **UI page** | Switches / Servers / Firewalls list → device detail (Interfaces, VLANs, Topology, collection-health panel) |
| **Common failures** | community mismatch → device created `unknown`/no facts; firewall blocks UDP 161; SNMP v3 auth/priv protocol mismatch (`USM` errors); early-bail fixed — all communities are tried |
| **Logs to check** | `discover:` / `scan:` error lines; classification line shows `confidence=0`/`unknown` when no driver matched |
| **Safe test scope** | a single known switch/server first (`-discover`), then a `/29`–`/27` lab subnet before a production `/24`; `-max-hosts` caps scope (default 4096) |
| **Rollback / cleanup** | delete the test device(s) via DB or API; no device-side changes were made (read-only SNMP) |

---

## 2. Redfish — server BMC (iLO / iDRAC)

| Field | Value |
|---|---|
| **Credential type** | `http_basic` (BMC local or directory account) |
| **Permissions / role** | Redfish **read-only / Operator** account (no config changes needed) |
| **Ports / protocols** | TCP **443** HTTPS (Redfish `/redfish/v1`); self-signed certs accepted (mgmt-LAN) |
| **Example command** | `hims-collector -redfish 10.30.0.21 -location <site-uuid>` |
| **Expected output** | `Redfish 10.30.0.21 persisted as device <uuid> (BMC + N sensors)` |
| **Tables populated** | `devices`, `device_credentials`, `bmc_info`, `bmc_sensors`, `device_facts` |
| **UI page** | Servers → server detail → **BMC / out-of-band** panel (controller, firmware, power, fans/PSU/temp sensors) |
| **Common failures** | 401 (wrong account / account locked); Redfish disabled on the BMC; old iLO4/iDRAC7 firmware with partial Redfish schema; per-physical-drive storage is **deferred** (only summary) |
| **Logs to check** | `redfish:` error lines; HTTP status surfaced in the error |
| **Safe test scope** | one non-critical host's BMC; BMC access is out-of-band and read-only |
| **Rollback / cleanup** | none on device side; remove the test device row if desired |

---

## 3. vSphere / ESXi — host → VM map + datastores (govmomi)

| Field | Value |
|---|---|
| **Credential type** | `http_basic` / `vendor_api` (vSphere SSO or ESXi local) |
| **Permissions / role** | **Read-only** vCenter/ESXi role (`System.View`, `VirtualMachine` read) |
| **Ports / protocols** | TCP **443** HTTPS, SOAP SDK at `/sdk` (govmomi); insecure TLS for self-signed |
| **Example command** | `hims-collector -vsphere 10.30.0.40 -location <site-uuid>` |
| **Expected output** | `vSphere 10.30.0.40 persisted as device <uuid> (N VMs)` |
| **Tables populated** | `devices`, `device_credentials`, `virtual_machines`, `device_facts` |
| **UI page** | Virtual Hosts → host detail → **VMs** table (power state, vCPU, memory, guest OS, IP) |
| **Common failures** | 401/permission denied; pointing at vCenter expecting a single host (multi-host enumeration is **deferred** — connect per-ESXi for now); lockdown mode on ESXi |
| **Logs to check** | `vsphere:` error lines; govmomi login/permission errors |
| **Safe test scope** | a single standalone ESXi host first, then a small cluster host |
| **Rollback / cleanup** | none on device side; read-only API session is logged out after collect |

---

## 4. Hyper-V — host → VM map (WinRM / PowerShell)

| Field | Value |
|---|---|
| **Credential type** | `winrm` (`username:password`, domain or local admin able to read Hyper-V) |
| **Permissions / role** | member of **Hyper-V Administrators** or read equivalent; WinRM enabled |
| **Ports / protocols** | TCP **5985** WinRM HTTP (negotiate auth) |
| **Example command** | `hims-collector -hyperv 10.30.0.50 -location <site-uuid>` |
| **Expected output** | `Hyper-V 10.30.0.50 persisted as device <uuid> (N VMs)` |
| **Tables populated** | `devices`, `device_credentials`, `virtual_machines`, `device_facts` |
| **UI page** | Virtual Hosts → host detail → **VMs** table |
| **Common failures** | WinRM not enabled (`Enable-PSRemoting`); 5985 firewalled; HTTPS-only (5986) environments; the PowerShell `Get-VM` cmdlet unavailable (host isn't a Hyper-V host) |
| **Logs to check** | `hyperv:` error lines incl. `winrm exit <code>: <stderr>` |
| **Safe test scope** | one lab Hyper-V host; the script only **reads** (`Get-VM`) — no VM state changes |
| **Rollback / cleanup** | none on device side |

---

## 5. ONVIF — IP cameras / NVR (SOAP)

| Field | Value |
|---|---|
| **Credential type** | `onvif` or `http_basic` (`username:password`) |
| **Permissions / role** | ONVIF **User/Operator** with device-info read |
| **Ports / protocols** | TCP **80** HTTP ONVIF service (`http://<ip>`); WS-Security `PasswordDigest` |
| **Example command** | `hims-collector -onvif 10.40.0.11 -location <site-uuid>` |
| **Expected output** | `ONVIF 10.40.0.11 persisted as device <uuid> (camera info + profiles)` |
| **Tables populated** | `devices`, `device_credentials`, `camera_info`, `device_facts` (NVR channels where exposed) |
| **UI page** | Cameras / NVRs list → **CCTV detail** (manufacturer, model, resolution, RTSP/ONVIF URLs) |
| **Common failures** | digest auth rejected (clock skew → WS-Security `Created` invalid); ONVIF disabled on camera; non-standard ONVIF port; `GetStreamUri` (live RTSP per-profile) is **deferred** |
| **Logs to check** | `onvif:` error lines; SOAP fault strings |
| **Safe test scope** | one camera; read-only device-info/profile calls |
| **Rollback / cleanup** | none on device side |

---

## 6. Wireless — UniFi controller (REST)

| Field | Value |
|---|---|
| **Credential type** | `http_basic` / `vendor_api` |
| **Permissions / role** | UniFi **read-only admin** |
| **Ports / protocols** | TCP **8443** HTTPS; cookie-session login |
| **Example command** | `hims-collector -unifi 10.50.0.5 -location <site-uuid>` |
| **Expected output** | `UniFi 10.50.0.5 persisted as device <uuid> (N APs)` |
| **Tables populated** | `devices`, `device_credentials`, `wlan_controller_info`, `access_points`, `device_facts` |
| **UI page** | Wireless → **WirelessDetail** (controller summary + AP table: name/MAC/model/IP/status/clients) |
| **Common failures** | login 401; the `default` site name differs; UDM vs self-hosted controller path differences; controller version API drift |
| **Logs to check** | `unifi:` error lines |
| **Safe test scope** | one controller; read-only `stat/device` calls |
| **Rollback / cleanup** | none on device side |

---

## 7. Wireless — TP-Link Omada controller (REST)

| Field | Value |
|---|---|
| **Credential type** | `http_basic` / `vendor_api` |
| **Permissions / role** | Omada **Viewer** role |
| **Ports / protocols** | TCP **8043** HTTPS; token login (`Csrf-Token`), controller-id in path |
| **Example command** | `hims-collector -omada 10.50.0.6 -omada-cid <controller-id> -location <site-uuid>` |
| **Expected output** | `Omada 10.50.0.6 persisted as device <uuid> (N APs)` |
| **Tables populated** | `devices`, `device_credentials`, `wlan_controller_info`, `access_points`, `device_facts` |
| **UI page** | Wireless → WirelessDetail |
| **Common failures** | wrong/empty `-omada-cid` (get from `/api/info`); site name ≠ `Default`; token expiry; cloud-based Omada (vs on-prem) differs |
| **Logs to check** | `omada:` error lines |
| **Safe test scope** | one controller; read-only `/sites/{site}/devices` |
| **Rollback / cleanup** | none on device side |

---

## 8. Wireless — Ruckus SmartZone (REST)

| Field | Value |
|---|---|
| **Credential type** | `http_basic` / `vendor_api` |
| **Permissions / role** | SmartZone **read-only** admin |
| **Ports / protocols** | TCP **8443** HTTPS; session login; API version path `/wsg/api/public/v9_1` |
| **Example command** | `hims-collector -ruckus 10.50.0.7 -location <site-uuid>` |
| **Expected output** | `Ruckus 10.50.0.7 persisted as device <uuid> (N APs)` |
| **Tables populated** | `devices`, `device_credentials`, `wlan_controller_info`, `access_points`, `device_facts` |
| **UI page** | Wireless → WirelessDetail |
| **Common failures** | API-version path mismatch by firmware (`v9_1` vs other); session endpoint differences; 401 |
| **Logs to check** | `ruckus:` error lines (`session → <code>`, `aps → <code>`) |
| **Safe test scope** | one controller; read-only AP list |
| **Rollback / cleanup** | none on device side |

---

## 9. Wireless — Extreme (ExtremeCloud IQ / XIQ, REST)

| Field | Value |
|---|---|
| **Credential type** | `http_basic` / `vendor_api` (XIQ tenant account) |
| **Permissions / role** | XIQ **Monitor / read-only** |
| **Ports / protocols** | TCP **443** HTTPS to `api.extremecloudiq.com` (cloud); bearer token via `/login` |
| **Example command** | `hims-collector -extreme 10.50.0.8 -extreme-base https://api.extremecloudiq.com -location <site-uuid>` |
| **Expected output** | `Extreme (XIQ) 10.50.0.8 persisted as device <uuid> (N APs)` |
| **Tables populated** | `devices`, `device_credentials`, `wlan_controller_info`, `access_points`, `device_facts` |
| **UI page** | Wireless → WirelessDetail |
| **Common failures** | XIQ is cloud-hosted (the `-extreme` IP is just the **anchor** for the controller record); token lifetime/field-name drift by API revision; **paging** — v1 fetches the first 100 devices only; on-prem XCC has a different surface (**deferred**) |
| **Logs to check** | `extreme:` error lines (`login → <code>`, `devices → <code>`) |
| **Safe test scope** | one tenant; read-only `/devices` |
| **Rollback / cleanup** | none on device side |

---

## 10. Voice — Cisco CUCM phone registry (AXL / SOAP)

| Field | Value |
|---|---|
| **Credential type** | `http_basic` / `vendor_api` (AXL **Application User**) |
| **Permissions / role** | CUCM app user in **Standard AXL API Access** + **Standard CCM Admin Users** |
| **Ports / protocols** | TCP **8443** HTTPS; SOAP `POST /axl/`, `SOAPAction: CUCM:DB ver=<v> listPhone` |
| **Example command** | `hims-collector -cucm 10.60.0.10 -cucm-version 12.5 -location <site-uuid>` |
| **Expected output** | `CUCM 10.60.0.10 persisted as device <uuid> (N phones)` |
| **Tables populated** | `devices`, `device_credentials`, `pbx_phones`, `device_facts` (`phone_count`) |
| **UI page** | **Voice** → **PbxDetail** (phone registry: name/model/description/device pool) |
| **Common failures** | AXL not enabled (Cisco AXL Web Service stopped); **schema version mismatch** between `-cucm-version` and the CUCM release (8.x–15.x); SOAP fault on `listPhone`; large clusters need **paging** (v1 fetches the first page) |
| **Logs to check** | `cucm:` error lines incl. `AXL auth failed (401)`, `AXL fault: <msg>` |
| **Safe test scope** | a publisher in a lab/UAT CUCM; `listPhone` is read-only |
| **Rollback / cleanup** | none on device side |

---

## 11. AD import — computer-object discovery (LDAP)

| Field | Value |
|---|---|
| **Credential type** | `ldap` (`bindDN-or-UPN:password`) |
| **Permissions / role** | any **authenticated domain read** account (read computer objects) |
| **Ports / protocols** | TCP **389** LDAP (`ldap://<dc>:389`) |
| **Example command** | `hims-collector -adimport dc01.corp.local -basedn "OU=HotelA,DC=corp,DC=local" -location <site-uuid>` |
| **Expected output** | `AD import from dc01.corp.local persisted N computers` |
| **Tables populated** | `devices` (category from OS classification), `device_facts` (AD attributes); no credential bound (LDAP is the import path, not a device credential) |
| **UI page** | Device lists by category (Servers / Windows clients), filtered by the imported site scope |
| **Common failures** | bind failure (wrong DN/UPN/password); base DN typo / empty subtree; LDAPS-only environments (389 blocked); disabled computer objects imported (UAC `0x2` bit honored → `enabled=false`) |
| **Logs to check** | `adimport:` error lines; bind/search errors |
| **Safe test scope** | a single OU subtree (`-basedn`) before a domain-wide base; read-only LDAP search |
| **Rollback / cleanup** | delete imported device rows scoped to the test OU; no AD changes made |

---

## 12. Monitoring loop (reachability + SNMP metrics)

| Field | Value |
|---|---|
| **Credential type** | `snmp_v2c` / `snmp_v3` for SNMP-metric checks; none for TCP checks |
| **Permissions / role** | SNMP read-only (metric checks) |
| **Ports / protocols** | per-check: TCP connect, or UDP 161 SNMP |
| **Example command** | `hims-collector -seed` (seed default checks, then exit) · `hims-collector -monitor -tick 30s` (run the sweep loop) |
| **Expected output** | periodic sweep logs; samples written; alerts opened/auto-resolved with the work-order bridge |
| **Tables populated** | `monitoring_checks`, `monitoring_samples`, `alert_rules`, `alerts`, and bridged `work_orders` |
| **UI page** | **Monitoring** (overview + per-device checks/samples) and **Alerts** |
| **Common failures** | check target unreachable (expected → opens alert); SNMP metric OID not supported by device; hysteresis flapping (tune thresholds) |
| **Logs to check** | monitoring sweep log lines; alert open/resolve lines |
| **Safe test scope** | seed defaults against a couple of known-good devices first |
| **Rollback / cleanup** | delete test checks via `DELETE /monitoring/checks/{id}`; resolve test alerts |

---

## 13. Live-validation checklist (per collector)

Run this for **each** collector against one real target before trusting it
fleet-wide. Tick every box.

```
[ ] Credential group of the required kind exists and is scoped to the target's site/subnet
[ ] HIMS_ENCRYPTION_KEY set; API logs "credential encryption enabled"
[ ] Collector run prints the success line (device UUID + a non-zero inventory count)
[ ] Device appears in the correct UI list page under the expected category
[ ] Device detail page renders the expected panel WITH data (not the empty-state hint)
[ ] Expected tables hold rows for this device_id (spot-check via API endpoint)
[ ] Re-run the collector → counts stable, no duplicate rows (idempotency / stale-prune works)
[ ] last_seen_at advanced on re-run; stale rows from a removed item get pruned
[ ] No secret/community string appears anywhere in collector or API logs
[ ] The bound credential is NOT returned by GET /credentials or any device endpoint
[ ] Vendor/version edge notes in PROGRESS.md ⚠️ trigger were checked against this target
```

### Criteria for marking a collector **production-validated**

A collector moves from *"built, live-validation pending"* to
**production-validated** only when ALL of the following hold and are recorded
in `PROGRESS.md` (replace the ⚠️ trigger block with a ✅ validated note giving
the date, target model/firmware, and operator):

1. **Real target, real credential.** Validated against actual hardware/service
   of the intended vendor — not a simulator/sample payload.
2. **End-to-end populate.** Device created, credential bound, every table the
   collector owns populated with correct values (cross-checked against the
   device's own UI/CLI).
3. **UI parity.** The intended UI page renders the data correctly.
4. **Idempotent + prune.** A second run produces stable counts; removing an
   item on the device prunes its row on the next run.
5. **Security clean.** No secret leaks in logs; credential never surfaced via
   API/UI; read-only confirmed (no device-side change).
6. **Edge cases from the ⚠️ trigger resolved or explicitly accepted** (e.g.
   CUCM AXL paging, XIQ device paging, vCenter multi-host, Redfish per-drive,
   ONVIF GetStreamUri) — either implemented, or documented as an accepted
   limitation with a BACKLOG entry + activation trigger.
7. **At least two device models/firmware** of that class validated where the
   fleet runs more than one (catches version/API drift).

Until a collector meets all seven, it stays labelled live-validation-pending
in `PROGRESS.md` and must not be presented to operators as production-trusted.

---

## 14. Bring-up (run the stack)

```bash
# 1. Postgres (throwaway/dev)
docker run -d --name hims-pg -e POSTGRES_USER=hims -e POSTGRES_PASSWORD=hims \
  -e POSTGRES_DB=hims -p 5432:5432 postgres:16

# 2. Apply migrations (golang-migrate format, migrations/NNNNNN_*.up.sql)
export HIMS_DATABASE_URL='postgres://hims:hims@localhost:5432/hims?sslmode=disable'
migrate -path migrations -database "$HIMS_DATABASE_URL" up

# 3. API
export HIMS_ENCRYPTION_KEY='<32-byte key>'
export HIMS_ADDR=':8090'
go run ./cmd/hims-api          # logs: drivers registered / encryption enabled / starting

# 4. Frontend
cd web && npm run dev          # or `npm run build` + serve dist/

# 5. Smoke test
curl -s localhost:8090/healthz                       # {"status":"ok"}
curl -s localhost:8090/api/v1/devices?category=pbx   # device list (category-scoped)
curl -s localhost:8090/api/v1/dashboard              # executive rollups

# 6. A collector (after a credential group is provisioned for the target's site)
go run ./cmd/hims-collector -discover 10.20.0.10 -location <site-uuid>
```

Health endpoint: `GET /healthz`. **All data endpoints are under `/api/v1/...`.**
The device list endpoint is **category-scoped**: `GET /api/v1/devices?category=<cat>`
(e.g. `switch`, `server`, `pbx`, `wireless_controller`). Per-device detail
endpoints follow `GET /api/v1/devices/{id}/<facet>` (`interfaces`, `vms`,
`access-points`, `phones`, `bmc`, `printer-supplies`, `ups`, …).

Without `HIMS_DATABASE_URL` the API starts in **no-db mode** on `:8090` and
returns `{"status":"ok","db":"unavailable"}` — useful for a binary smoke test
but no data is served.

### Verified local bring-up (2026-06-03)

This stack was stood up end-to-end and smoke-tested:
- Postgres 16 (container) ← all 21 migrations applied clean → 46 tables incl.
  `pbx_phones`, `access_points`, `bmc_info`.
- `hims-api` started: logged all **14** registered drivers (incl. `cucm`,
  `extreme`), `credential encryption enabled`, listening `:8090`.
- `POST /api/v1/credentials` (snmp_v2c) → DTO returned **metadata only**; the
  stored `encrypted_blob` is ciphertext (plaintext-substring check = false,
  37-byte AES-GCM blob); secret string appears in **zero** API responses.
- Voice read path proven: a `pbx` device + two `pbx_phones` rows →
  `GET /api/v1/devices/{id}/phones` returned both phones with
  `collection_source:"axl"`.
- `POST /api/v1/monitoring/seed` → `{"seeded":1}`; `monitoring/overview`
  reflected the new check.
- Frontend `npm run build` + `vite preview` served `index.html` + bundle.
