# Wireless Controller Driver Catalog — Handover

Canonical reference for HIMS multi-vendor wireless onboarding + collection.
Last updated alongside commit `5ae1a26` (local only, not pushed).

## Architecture (single source of truth)

The "Add controller" flow is **model-driven** from one catalog, not a hardcoded
list. Everything reads the same definition:

| Concern | Where |
| --- | --- |
| Driver catalog (the source of truth) | `internal/api/wireless_registry.go` |
| Catalog API for the form | `GET /api/v1/wireless/controller-vendors` (`listWirelessVendors`) |
| Pre-persist Test Connection | `POST /api/v1/wireless/controllers/test` (`testWirelessController` → `probeWirelessController`) |
| Persist + collect (add) | `POST /api/v1/wireless/controllers` (`addWirelessController`) |
| Collection authority (routing) | `collectWirelessForDevice` in `internal/api/wireless_controllers.go` |
| Normalized gather + persist | `gatherWirelessRosters` / `persistWirelessResult` in `internal/api/wireless_vendor_collect.go` + `vendor_profiles.go` |
| Per-capability health recorder | `recordWirelessHealth` in `internal/api/wireless_controllers.go` |
| Add form (generated from catalog) | `web/src/components/AddWirelessController.tsx` |
| Controller detail UI | `web/src/pages/WirelessDetail.tsx` |

Onboarding a new platform = add one catalog row. A capability is only ever shown
as collectable when its collector actually runs — no fake "full support".

### Data model

Common normalized tables (UI always reads these): `wlan_controller_info`,
`access_points`, `wireless_ssids`, `wireless_clients`, `wireless_radio_status`
(+ `channel_width`, migration `000091`), `wireless_events`, and the per-capability
`wireless_collection_health` (migration `000090`).

### Vendor client packages (parsers are the testable core)

| Driver key | Profile vendor_type | Client package | Transport |
| --- | --- | --- | --- |
| `ruckus_zd` | `ruckus_zd` | `internal/ruckuszd` | Web-XML (AJAX) |
| `extreme_xcc` | `extreme_xcc` | `internal/extremexcc` | On-prem REST |
| `ruckus_sz` | `wireless_ruckus` | `internal/ruckus` | SmartZone public REST |
| `unifi` | `wireless_unifi` | `internal/unifi` | UniFi Network REST |
| `omada` | `wireless_omada` | `internal/omada` | Omada Open API v2 |
| `aruba_os8` | `wireless_aruba` | `internal/aruba` | ArubaOS 8 showcommand REST |
| `aruba_os10` | `wireless_aruba_os10` | `internal/aruba` (shared) | ArubaOS 10 on-prem showcommand REST |
| `aruba_central` | `wireless_aruba_central` | `internal/arubacentral` | Aruba Central cloud REST (OAuth2 bearer) |

## Capability status model

| Status | Meaning |
| --- | --- |
| `collected` | Real rows persisted this run (runtime proof). |
| `supported` | Implemented + fixture-tested; expected to collect. |
| `implemented_live_validation_pending` | **Full path exists** — collector code, endpoint/command, parser, fixture test, runtime route — but the exact response shape is **not yet confirmed against real hardware/tenant**. NOT "developer work remaining". |
| `external_dependency_required` | Implemented; blocked on an external input (token/tenant). |
| `unsupported_by_device` | Proven API/firmware gap (reason attached). |
| `endpoint_not_exposed` | Authenticated, but this firmware/API returned nothing (runtime). |
| `auth_failed` / `needs_configuration` | Runtime: login failed / a required field is missing. |
| `not_implemented` | Genuinely no collector code. **Used by nothing in this catalog.** |

`recordWirelessHealth` records the declared proven status as-is (with its reason)
when a run yields no rows, and flips it to `collected` automatically once real
rows arrive — so a live run upgrades the status without any code change.

## Final capability matrix

Legend: **live** = validated against real hardware · **fx** = implemented +
fixture-tested · **LVP** = `implemented_live_validation_pending` · **unsup** =
`unsupported_by_device` (proven).

| Driver | APs | SSIDs | Clients | Radios | Firmware | Health | Events |
| --- | --- | --- | --- | --- | --- | --- | --- |
| ruckus_zd | live | live | live | **live (466)** | live | unsup | unsup |
| extreme_xcc | fx | fx | fx | LVP | fx | fx | fx |
| ruckus_sz | fx | fx | fx | fx | fx | fx | fx |
| unifi | fx | fx | fx | fx | fx | fx | fx |
| omada | fx | fx | fx | LVP | fx | fx | fx |
| aruba_os8 | fx | fx | fx | LVP | fx | fx | unsup |
| aruba_os10 | fx | fx | fx | LVP | fx | fx | unsup |
| aruba_central | fx | fx | fx | LVP | fx | fx | fx |

ZoneDirector live regression (production controller): **234 APs · 9 SSIDs ·
1051 clients · 466 radios**; per-capability health all correct.

### Proven `unsupported_by_device` (not a gap — the API has no pollable endpoint)

- **ZoneDirector health & events** — the AJAX admin interface exposes no
  controller/subsystem health endpoint and returns zero event rows; events are
  delivered via SNMP traps. Use reachability + firmware (backfilled from the AP
  fleet) for health.
- **ArubaOS 8/10 events** — controllers stream events to syslog/SNMP; no
  structured showcommand returns a pollable event/alarm table.

## External validation dependencies (the ONLY remaining work)

These are `implemented_live_validation_pending` — the code, parser, fixtures, and
runtime route all exist; only live hardware/tenant is needed to confirm the exact
response shape. None is an implementation gap.

| Driver | Capability | External dependency |
| --- | --- | --- |
| extreme_xcc | radios | A live-bound XCC (VE6120) wireless profile + credential. |
| omada | radios | A live Omada controller to confirm the `/eaps/{mac}` `radioList` shape (differs v4 vs v5/OC200). |
| aruba_os8 | radios | A live ArubaOS 8 controller to confirm `show ap bss-table` column names. |
| aruba_os10 | radios | A live ArubaOS 10 (on-prem Conductor) controller, same as os8. |
| aruba_central | radios | A live Central tenant + API token to confirm the `/monitoring/v2/aps` `radios[]` field shape. |

> Note: all `fx` (fixture-tested) capabilities for the no-hardware vendors
> (UniFi, Omada, SmartZone, all Aruba families) are likewise pending a first live
> run; radios are called out separately only because their response shapes are the
> most version-sensitive. A first live collection promotes every `fx` capability
> to `collected` (or surfaces `endpoint_not_exposed` with the exact reason).

## How to validate a pending-live driver when hardware becomes available

General procedure (same for any driver):

1. **Onboard**: Inventory → Wireless → Add controller → pick the driver. The form
   shows only that driver's required fields (e.g. Omada needs Controller ID;
   Aruba Central takes the API gateway URL + token-as-password).
2. **Test Connection** (no DB writes): returns structured checks —
   `Required fields → Reachable → Authentication → API version/path → AP query` —
   each with the exact reason on failure. Fix any field/path the checks flag.
3. **Run Collection**: persists every supported roster.
4. **Inspect** the controller detail page → "Driver & capabilities": each
   capability shows `collected (N)` on success, or an exact runtime status
   (`endpoint_not_exposed` / `auth_failed`) naming the capability.
5. **If a parser field differs** from the fixture (the radios LVP case): adjust
   the field tag/key in the vendor client's parser and update its fixture test —
   typically a one-line change. The fixtures lock the corrected shape in.

Driver-specific endpoints/commands to confirm (and the parser to adjust):

| Driver | Capability | Endpoint / command | Parser to adjust |
| --- | --- | --- | --- |
| extreme_xcc | radios | AP payload `radios[]` from `/aps/query` | `apRadios` in `internal/extremexcc/collect.go` |
| omada | radios | `GET /{cid}/api/v2/sites/{site}/eaps/{mac}` → `result.radioList[]` | `parseRadioDetail` in `internal/omada/client.go` |
| aruba_os8/os10 | radios | `show ap bss-table` (`ch`, `phy` columns) | `parseBSSTable` in `internal/aruba/client.go` |
| aruba_central | radios | `GET /monitoring/v2/aps` → AP `radios[]` | `parseRadios` in `internal/arubacentral/client.go` |
| ruckus_sz | api version | `GET /wsg/api/public/apiInfo` (auto-detected) | `DetectAPIBase` / `newestAPIBase` in `internal/ruckus/client.go` |

### Authenticated local API verification (no UI)

To exercise the endpoints directly against the dev DB (no real controllers
required to see the catalog/test plumbing):

```
# mint a dev admin session in the dev DB (sessions.token_hash = sha256(token))
TOKEN=$(openssl rand -hex 32)
HASH=$(printf '%s' "$TOKEN" | sha256sum | cut -d' ' -f1)
docker exec hims-pg psql -U hims -d hims -c \
  "INSERT INTO sessions(token_hash,user_id,expires_at) VALUES('$HASH', <admin-user-id>, now()+interval '1 hour');"

# run the API on an isolated port, then:
curl -b "hims_session=$TOKEN" http://127.0.0.1:18097/api/v1/wireless/controller-vendors
curl -b "hims_session=$TOKEN" -X POST http://127.0.0.1:18097/api/v1/wireless/controllers/test \
  -H 'Content-Type: application/json' -d '{"vendor":"omada","ip":"<ip>","username":"...","password":"...","controller_id":"..."}'
```

(Delete the session row and stop the binary afterward.)

## Tests

Parser/fixture tests live next to each client and cover every new parser:

- `internal/ruckuszd/client_test.go` — `TestAPRadios`
- `internal/extremexcc/collect_test.go` — `TestAPRadios`
- `internal/unifi/client_test.go` — `TestParseWLANConf/Stations/Radios/Sysinfo/Health/Events`
- `internal/omada/client_test.go` — `TestParseSSIDs/Clients/RadioDetail/Info/Alerts`
- `internal/ruckus/client_test.go` — `TestParseWLANs/Clients/Radios/Controller/Events/NewestAPIBase`
- `internal/aruba/client_test.go` — `TestParseAPDatabase/UserTable/Version/BSSTable/Login`
- `internal/arubacentral/client_test.go` — `TestParseAPs/Clients/Networks/Radios/Alerts/MostCommonFirmware`
- `internal/api/wireless_registry_test.go` — catalog invariants + probe-error classifier

Run: `go test ./internal/...` · build: `go build ./...` · web: `npm --prefix web run build`.
