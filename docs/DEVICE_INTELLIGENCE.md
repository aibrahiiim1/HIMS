# Device Intelligence & Universal Discovery Compatibility

Phase initiative: make discovery of a *new* subnet classify + manage as much as
possible automatically, without per-OID/per-vendor hand-patching, and make every
classification **explainable**. This doc is the living artifact for the phase.

Branch: `feat/discovery-acceptance-credkind` — **local only, not pushed.**

---

## PHASE 1 — AUDIT (current compatibility matrix)

### 1.1 What already exists (the good news)

HIMS already implements the layered pipeline this phase asks for. Audited
2026-06-16 against the live code.

**Pipeline layers** (`internal/discovery/pipeline.go`, `protocolplan.go`):
1. **Reachability** — TCP port scan (+ Hikvision 8000+octet derivation), open-port set.
2. **Unauthenticated banners** — HTTP Server header + page title + small body
   snippet (up to 3 web ports); SSH ident banner.
3. **Protocol plan** — pre-decides the device *candidate* (windows / linux /
   appliance / camera / vmware / network / printer / unknown) and the **expected
   vs opportunistic vs skipped** protocols from cheap evidence, so the scan does
   not spray every protocol at every host.
4. **SNMP probe** — v2c (resolved communities → default public/private), **v1
   fallback** (same community), and **v3** (USM SHA/MD5 + AES/DES). Captures
   sysDescr / sysObjectID / sysName / sysContact / sysLocation.
5. **Authenticated probe** — expected-protocol credentials tried before
   opportunistic; bind-on-success; subnet-scoped credential exclusivity (anti-spray).
6. **Classification** — `driver.Registry.Best()` (confidence-ranked) + evidence
   classifier (`internal/classify`) + fingerprint catalog (`internal/fingerprint`).
7. **Collection** — winning driver's `Collect()` over the authenticated session.
8. **Explanation** — `discovery_results.probe_data` (scanDetail): open_ports,
   classification, confidence, evidence[], candidate, expected/opportunistic/
   skipped protocols, cred_attempts[], bound_cred, collected_via, next_action.

**SNMP versions:** v1 + v2c + v3 all attempted. ✅
**Credential model:** bound-first → non-weak → narrowest-scope → group priority;
subnet-scoped sets are exclusive (anti-spray); expected-before-opportunistic. ✅

### 1.2 Classification mechanisms today (THREE — this is the core finding)

Classification confidence is produced by **three independent subsystems**, which
is the root reason past misclassifications had to be fixed by hand in code:

| # | Mechanism | Where | Editable? | Exclusions? |
|---|---|---|---|---|
| 1 | **Driver `Fingerprint()`** | `internal/driver/*/` (per driver) | code only | only hardcoded (e.g. aruba's HP-JetDirect bail) |
| 2 | **Evidence classifier** | `internal/classify/rules.go` | code only | no |
| 3 | **Fingerprint catalog** | `internal/fingerprint` (builtin) + `vendor_fingerprints` DB table | **DB rows operator-editable** (CRUD/import/export, priority, source, disable-to-suppress-builtin) | **NO — positive matches only** |

The catalog (mechanism 3) is the right home for "universal compatibility," and it
already has **~95 built-in rules** + operator extensibility. The gap is that it
has **no exclusion/negation support**, so a broad prefix (HP `.11`) that should
be a printer under `.11.2.3.9` had to be excluded in driver code (mechanism 1)
instead of declaratively in the catalog.

### 1.3 Compatibility matrix — current coverage

Legend: ✅ dedicated driver/collect · 🟡 catalog-classified (identify only, generic/no deep collect) · ❌ none

#### Switches / routers
| Vendor | Classify | Collect | Evidence |
|---|---|---|---|
| Cisco IOS/Catalyst/Nexus | ✅ OID `.9` @90 / descr @70 | ✅ cisco_ios (intf/VLAN/FDB/ARP/CDP) | strong |
| Aruba/HPE/ProCurve | ✅ OID `.11`/`.14823`/`.47196` @90 | ✅ aruba_hpe | strong (+ HP-printer exclusion) |
| Huawei VRP | ✅ OID `.2011` @90 | ✅ huawei_vrp | strong |
| Extreme ERS/EXOS | ✅ OID `.45`/`.1916` @90 | ✅ extreme_switch | strong |
| MikroTik RouterOS | 🟡 catalog OID `.14988` @80 → router | ❌ no driver | identify only |
| Ubiquiti | 🟡 catalog OID `.41112` @78 | ❌ | identify only |
| Juniper | 🟡 catalog OID `.2636` @80 | ❌ | identify only |
| TP-Link | 🟡 catalog OID `.11863` @70 | ❌ | identify only |
| Arista / Netgear / Brocade / Alcatel | 🟡 catalog | ❌ | identify only |
| D-Link / Ruijie | ❌ (Ruijie web-title→switch only) | ❌ | weak |

#### Wireless controllers / APs
| Vendor | Classify | Collect | Notes |
|---|---|---|---|
| Aruba / UniFi / Ruckus / Omada / Extreme | ✅ banner+port @78 (`wlan_controller`) | ✅ omada/ruckus/extreme; ⚠️ **unifi driver NOT registered**; Aruba no REST collector | see gap G2 |
| Cisco WLC / MikroTik CAPsMAN | ❌ | ❌ | none |

#### Firewalls
| Vendor | Classify | Collect |
|---|---|---|
| Fortinet | ✅ OID `.12356` @90 | ✅ fortigate (HA/VPN/license/sessions) |
| Palo Alto | 🟡 catalog OID `.25461` @85 | ❌ |
| Cisco ASA | 🟡 descr "adaptive security appliance" | ❌ |
| Sophos / pfSense / OPNsense / MikroTik | ❌ | ❌ |

#### Printers / MFP — ✅ broad (13 vendors)
HP/JetDirect/LaserJet/OfficeJet, Canon iR-ADV/LBP, Kyocera, UTAX/TA, Ricoh,
Xerox, Brother, Epson, Samsung, Sharp, Konica, Toshiba, Lexmark → `printer_snmp`
(SNMP v1/v2c/v3, Printer-MIB: vendor/model/serial/page-count/supplies). Catalog
also has vendor OIDs (HP `.11.2.3.9`, Canon `.1602`, Ricoh `.367`, Xerox `.253`,
Epson `.1248`, Zebra `.10642`).

#### Servers / BMC
| Type | Classify | Collect |
|---|---|---|
| Linux/Windows (SNMP) | ✅ host_snmp OID `.8072`/`.311` @80 | ✅ CPU/RAM/disk/intf |
| Windows (deep) | ✅ ports/OS-caption → endpoint/server | ✅ WinRM + Relay-Agent WMI |
| ESXi | ✅ SNMP OID `.6876` @90 + vSphere banner @71 | ✅ esxi (SNMP) + vsphere (govmomi VM map) |
| Hyper-V | ✅ (Windows) | ✅ hyperv (WinRM Get-VM) |
| iLO / iDRAC / Redfish BMC | ✅ banner @72 (`redfish_bmc`) | ✅ Redfish JSON (model/serial/BIOS/health) |
| Lenovo XCC / generic Redfish | 🟡 banner if "redfish"/"ibmc" | ✅ if Redfish-compliant |
| IPMI-only (no Redfish) | ❌ | ❌ |

#### Storage / NAS
Synology (`.6574` @78), QNAP (`.24681` @78) catalog-classified 🟡; WD / EMC /
NetApp / Pure ❌. No deep collect.

#### CCTV
Hikvision/Dahua/Axis/Uniview ✅ (banner @75 + ISAPI deviceType → NVR/DVR/camera
@88-90; ONVIF deep). NVR channels modeled as child entities. ✅

#### UPS
APC/Eaton/Liebert/Vertiv/CyberPower/Riello/Tripp Lite + generic UPS-MIB → ✅ `ups_snmp`.

#### Voice / other
CUCM PBX ✅ (AXL); Grandstream/Polycom/Yealink 🟡 catalog; ip_phone/voice_gateway
categories exist but have no fingerprints. ISC Kea/Stork DHCP 🟡 (`webapp`).

### 1.4 Gaps & risks (ranked)

- **G1 — No exclusion rules in the catalog.** The single most important gap. A
  shared/broad enterprise prefix can only be corrected by hardcoding a driver bail
  (as done for HP JetDirect). Needed: declarative exclusions (OID-subtree / descr
  marker / port) so "HP `.11` = switch EXCEPT `.11.2.3.9` = printer" lives in data.
- **G2 — UniFi collector not registered** (`internal/driver/unifi` exists, missing
  from `internal/drivers/builtin.go`). Classify-then-fail-collect. Quick fix.
- **G3 — No explanation surfaced.** `probe_data` records evidence + winning
  classification + next_action, but **rejected candidates** and a per-device
  "why / confidence breakdown" panel are not surfaced in the UI.
- **G4 — Three disjoint classification layers** (driver / classify / catalog) with
  overlapping vendor knowledge and no single confidence+evidence explanation.
- **G5 — Missing evidence inputs:** TLS certificate CN and MAC/OUI are not
  captured during the probe (listed in the phase requirements).
- **G6 — Broad-prefix false-positive risk:** Dell `.674`→server, Microsoft
  `.311`→server, HP `.11`→switch classify on the enterprise prefix alone; only HP
  has an exclusion today.
- **G7 — Categories without fingerprints/templates:** access_point, ip_phone,
  voice_gateway, database, directory, dns, dhcp, storage (partial). Templates
  exist for switch/server/firewall/virtual_host/wireless_controller/pbx only.
- **G8 — No vendor-neutral deep-collect** for catalog-only identified gear
  (MikroTik/Ubiquiti/Juniper/TP-Link): they identify but collect nothing beyond
  the generic SNMP system group.

### 1.5 Recommended phase sequence (lowest-risk-first; extends, does not rewrite)

The catalog (mechanism 3) becomes the single source of device intelligence; the
evidence classifier + drivers consume it. We extend, not rewrite — the stabilized
discovery core (Windows/printer/site closures) stays intact.

- **Phase 2 — Device Intelligence Catalog.** Add **exclusion rules** + structured
  catalog entries (enterprise prefix, sysObjectID pattern, sysDescr pattern,
  family, confidence, exclusions, required protocols, driver+template mapping) to
  `vendor_fingerprints` (migration + domain + queries). Migrate the hardcoded HP
  JetDirect exclusion into data. Register UniFi (G2). Capture TLS-cert-CN + MAC/OUI
  (G5).
- **Phase 3 — Classification confidence + rejected candidates.** Produce an
  evidence-scored result with the chosen classification, confidence, the evidence
  list, and the **rejected candidates + why** — persisted in `probe_data`.
- **Phase 4 — Vendor/device packs.** Seed catalog packs (printers, network,
  wireless, servers/BMC/Redfish, firewalls, UPS, CCTV) with exclusions + confidence
  + template mapping. Create any missing category + its template.
- **Phase 5 — UI evidence panel.** Per-device "Classification Evidence": final
  class, confidence, driver/template, evidence used, rejected candidates,
  protocols tried, credential used, next action. Improve the unknown-device view
  (banners/sysDescr/sysObjectID/TLS-CN/MAC-OUI + likely category + next protocol).
- **Phase 6 — Compatibility test suite.** Table-driven regression tests with real
  sysObjectID/sysDescr/banner samples for every family + the false-positive cases
  (HP JetDirect≠switch, ProCurve=switch, Canon/Kyocera/UTAX=printer, Hikvision
  NVR vs camera, MikroTik, Ubiquiti, controllers, iLO/iDRAC/Redfish, ESXi, Windows,
  Linux, UPS-MIB).
- **Phase 7 — Real subnet validation.** Controlled re-scans of 172.21.60.0/24,
  172.21.96.0/24, 172.21.210.0/23; report accuracy / managed / unknown / false
  positives / driver-template mismatches / missing protocol support.

### 1.6 Acceptance (whole phase)
- New-subnet discovery needs no OID-by-OID hand-patching for common devices.
- Reusable, data-driven intelligence catalog **with exclusions**.
- Unknown devices show evidence + likely category + next protocol (never a bare
  "unknown / insufficient evidence").
- Baseline support for common printers/switches/servers/firewalls/controllers/
  UPS/CCTV/iLO-iDRAC-Redfish.
- False positives reduced by confidence + exclusion rules.
- Every classification explainable; compatibility tests guard against regressions.

### 1.7 Phase 1 status
Audit complete — matrix + gaps + sequence above. No code changed in Phase 1
(read-only). Build/vet/tests green, working tree clean, branch local only.

### 1.8 Phase 2 + 3 status — DONE (built together, per operator direction)

**Phase 2 — exclusion engine + operator-editable exclusions.**
- `fingerprint.Exclusion{Kind,Pattern}` + `Print.Exclusions`. A rule whose
  positive pattern matches is **suppressed** when the evidence also matches any
  exclusion, using the same per-channel semantics as a positive match
  (`matchKind`). This closes **G1** — broad/shared prefixes are corrected in
  *data*, not by a hardcoded driver bail.
- The hardcoded HP-JetDirect-≠-switch carve-out is now **data**: the built-in
  Aruba/HPE `.11` switch rule carries exclusions for the JetDirect OID subtree
  (`.11.2.3.9`) + the `jetdirect`/`laserjet`/`ethernet multi-environment` service
  markers.
- **Operator-editable**: `vendor_fingerprints.exclusions` JSONB column (migration
  `000078`, `NOT NULL DEFAULT '[]'`). Create/Update/Upsert + import/export (JSON +
  CSV `exclusions` column) round-trip exclusions; the seed path persists the
  built-in exclusions. Malformed/empty blobs degrade safely to "no exclusions".
- **Built-in catalog refresh (seed follow-up).** The live classifier reads
  fingerprints from the DB, and a DB row *shadows* the same-(kind,pattern) built-in
  catalog entry. So a built-in row first seeded BEFORE exclusions existed would keep
  `[]` and the shipped exclusion would never reach the classifier. `seed` now
  RE-SYNCS existing **built-in** rows in place: `planBuiltinSeed` classifies each
  catalog entry as create / refresh / preserve / up-to-date, and
  `RefreshBuiltinVendorFingerprint` (guarded `WHERE source='builtin'`) rewrites
  vendor/device_type/confidence/model/**exclusions** while preserving the row id +
  operator knobs (enabled, priority). Operator (`source='user'`) rows are **never**
  overwritten — even one sharing a built-in (kind,pattern). Re-seed is idempotent
  (a synced row is "up_to_date", no write). seed response now reports
  `{created, refreshed, preserved, up_to_date, library_size}`.
  - **Deploy step:** there is no startup auto-seed — after deploying this binary the
    operator must `POST /api/v1/vendor-fingerprints/seed` once (or via the UI) to
    propagate refreshed metadata + exclusions into the live catalog. Idempotent.

**Phase 3 — confidence + rejected candidates, persisted.**
- `fingerprint.MatchWithRejected()` returns winners **plus** rejected candidates
  with a reason: `excluded by <kind> marker "<pattern>"` (exclusion-suppressed) or
  `lower confidence (N) than chosen <type> (M)` (out-ranked runner-up of a
  different device type).
- `discovery.ClassificationDetail{Evidence, FinalSource, Winners, Rejected,
  LikelyType}` is attached to every classified host and **persisted into
  `discovery_results.probe_data`** as `classification_detail`. Evidence is recorded
  even when nothing matches, and an unknown's `likely_type` is the top rejected
  candidate's device type — so the eventual UI panel (Phase 5) can explain *why*,
  and what an unknown most likely is, with no further backend work.

Not in this pass (deferred, as directed): Phase 4 vendor packs, Phase 5 UI
evidence panel, G5 (TLS-cert-CN / MAC-OUI evidence capture), UniFi catalog
registration (G2 — confirmed a false alarm in audit: UniFi/Omada/Ruckus are
collection-only, not in the classification registry).

Gates: `go build/vet/test ./...` green (new tests:
`TestClassificationDetail_*`, `TestFpExclusions*`, plus the engine-level
`TestExclusion_*`). No frontend changed this pass. Working tree clean of tracked
files; branch local only — **not pushed**. Migration `000078` is applied by the
operator-run deploy (additive, default-valued, safe with the old binary).

### 1.9 Phase 4 status — DONE (vendor/device packs, four gated sub-commits)

Phase 4 expands `fingerprint.Library()` to cover the compatibility-matrix gaps,
across four sub-commits (SC1 network/firewall/LB, SC2 compute, SC3 edge, SC4
audit/docs). Pure catalog data + a migration for two new categories + data-driven
exclusions; the engine (Phase 2/3) is unchanged.

**Categories added (migration `000079`).** `load_balancer` (F5/Citrix/A10/Kemp) and
`pdu` (rack power distribution, distinct from `ups`). The `devices.category` CHECK
is extended; `domain.CatLoadBalancer`/`CatPDU`; `load_balancer` is sticky-infra
(must not downgrade to `server` on a weak re-scan); the Discovery category
dropdown lists both. All other Phase-4 device types reuse existing categories.

**Category validity is enforced by a test.** `TestLibraryCategoriesAreValid` walks
the whole catalog and fails if any print's `device_type` (after `CanonicalCategory`)
is not in the `devices.category` CHECK set — so a typo'd/unmapped category can
never reach scan-apply. (Audit confirmed: every Phase-4 device_type maps to a valid
category; no invalid/unexpected category string in catalog or tests.)

**Vendor packs added.**
- SC1 — switches/routers (D-Link, H3C, Ruijie, Allied Telesis, Dell PowerConnect,
  Meraki, Linksys; NX-OS/IOS-XE/XR, Comware, VyOS, EdgeOS), firewalls (SonicWall,
  WatchGuard, Check Point, Sophos, Barracuda, PAN-OS, Firepower, pfSense, OPNsense,
  Juniper SRX, UniFi USG), load balancers (F5, Citrix ADC, A10, Kemp).
- SC2 — server/BMC (HPE `.232` ProLiant+iLO, Dell iDRAC `.674.10892.2`, Lenovo
  XCC, Supermicro, AMI MegaRAC, Redfish; **BMCs classify as `server` with a
  vendor/model management-controller identity — no dedicated `bmc` category this
  phase**, per operator decision), virtualization (Proxmox, vCenter, Nutanix →
  `virtual_host`), storage (NetApp, EMC, TrueNAS, ONTAP, PowerStore, Isilon; +
  Synology/QNAP **reclassified `server`→`storage`**).
- SC3 — printers (Kyocera, Brother, Lexmark, Sharp, Konica, Toshiba + Canon/Kyocera
  markers), UPS (Vertiv/Liebert, CyberPower, Tripp Lite, Riello), PDU (APC rPDU,
  ServerTech, Raritan, Geist, Eaton ePDU), CCTV (vendor-neutral NVR/DVR markers,
  Uniview), wireless APs (Aruba Instant, UniFi, Ruckus ZoneFlex, Omada EAP,
  Aerohive → `access_point`), IP phones (Cisco/Yealink/Grandstream/Fanvil/Snom/
  Polycom → `ip_phone`; Grandstream/Yealink/Polycom moved off the `voip`→`pbx`
  default).

**Broad-prefix exclusion strategy (G6).** A broad/shared enterprise prefix keeps
its common classification but **excludes** the subtrees/markers that belong to a
different device family, in data (mirrors the original HP/JetDirect carve-out):

| Broad rule | Default | Excluded subtree/marker | Goes to |
|---|---|---|---|
| HP/Aruba `.11` | switch | `.11.2.3.9` OID + `jetdirect`/`laserjet`/`ethernet multi-environment` | printer |
| Dell `.674` | server | `.674.10895` (PowerConnect/Force10) | switch |
| APC `.318` | ups | `.318.1.1.4` / `.12` / `.26` (rPDU) | pdu |
| Eaton `Eaton` (sysDescr) | ups | `ePDU` marker | pdu |
| TP-Link `.11863` | switch | `EAP` marker (Omada AP) | access_point |

Other broad prefixes (Microsoft `.311`, Net-SNMP `.8072`) stay low-confidence
server signals with no competing specific family, so they need no exclusion;
specific product markers (e.g. SRX 85, Firepower 84, iDRAC 88) simply outrank the
generic vendor PEN they sit under.

**Template/detail behavior.** The new categories (`load_balancer`, `pdu`) — and
`access_point`/`ip_phone`/`storage`/`dvr` — are **not** in the frontend
`detailBase` route map, so they fall through to the generic device-detail view.
Notably `pdu` does **not** render the UPS template and `load_balancer` does **not**
render the firewall template (neither is mapped to those routes). Dedicated
detail/dashboard views for the new classes are deferred to Phase 5.

**Deploy / seed sequence (REQUIRED after deploying Phase 2–4).** The live classifier
reads DB rows, which shadow the built-in catalog, so the new metadata/exclusions/
categories reach production only after:
1. `hims-migrate up` applies **`000078`** (exclusions column) and **`000079`**
   (load_balancer + pdu categories).
2. `POST /api/v1/vendor-fingerprints/seed` is run **once** — `planBuiltinSeed`
   then creates the new built-in rows and refreshes drifted ones (vendor/type/
   confidence/model/exclusions), so e.g. the HP exclusion, the Synology/QNAP
   `storage` reclassification, and the new PDU/AP/LB prints become live.
   Operator-edited (`source='user'`) rows are never overwritten.

**Test coverage (Phase 2 → SC4).**
- Phase 2: `TestExclusion_HPJetDirectNotSwitch`, `TestExclusion_RealProCurveSwitchStillMatches`,
  `TestFpExclusions{RoundTrip,EmptyNormalizesToBracket,FromJSONMalformedIsSafe}`,
  `TestSeedPlan_{RefreshesDriftedBuiltinExclusions,IdempotentWhenInSync,PreservesOperatorRow,CreatesMissingNoDuplicates}`.
- Phase 3: `TestClassificationDetail_{WinnerRecorded,RejectedByExclusion,RejectedRunnerUp}` + evidence-on-no-match.
- SC1: `TestPack_{DellPowerConnectNotServer,DellServerStillServer,LoadBalancers,Firewalls,NewSwitchVendors}`.
- SC2: `TestPack_{BMCsAreServerWithIdentity,PowerEdgeNotIDRAC,HPEProLiantNotSwitch,Virtualization,StorageNAS,GenericLinuxStaysServer,GenericHTTPNotBMC}`.
- SC3: `TestPack_{PrintersEdge,PrinterNotSwitch,UPSvsPDU,CCTV_NVRDVRvsCamera,WirelessAPs,IPPhones}`.
- SC4: `TestLibraryCategoriesAreValid` (catalog→category invariant).

**Deferred (unchanged):** Phase 5 UI evidence panel; G5 (TLS-cert-CN / MAC-OUI
evidence capture); a dedicated `bmc`/`management_controller` category (revisit if
BMC-specific dashboards/reports are wanted); the create/update/list API encoding
`exclusions` as base64 (normalize before the Phase 5 UI consumes it).

Gates: `go build/vet/test ./...` green; frontend `lint`+`build` green (SC1 touched
the category dropdown; SC2–4 frontend-untouched); migration `000079` validated
against the real schema in a rolled-back transaction; production **not** mutated;
branch local only — **not pushed**.
