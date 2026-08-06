# Wireless Controllers — REST/XML as the PRIMARY collection method (SNMP/SSH fallback)

> **Audience:** an AI agent (or engineer) implementing this in the HIMS Go repo.
> **Status:** implementation runbook. Follow it top-to-bottom in safe staged commits
> (every commit compiles + `go vet` clean, per `CLAUDE.md`).
> **Source of truth:** the live-verified sibling desktop tool at
> `D:\WebProjects\NetworkToolWinApp` — its connectors and `FIELD_SOURCES.md` were
> validated against the exact production controllers this feature targets
> (ExtremeCloud IQ Controller `172.21.96.100`, Ruckus ZoneDirector ZD3050 `192.168.2.2`).
> Treat that tool's endpoint paths, auth flows, and field mappings as **proven** and
> port them faithfully.

---

## 0. Goal

Make **vendor management-plane APIs the primary way HIMS collects wireless inventory**,
matching the desktop tool exactly:

- **Extreme** on-prem ExtremeCloud IQ Controller / Extreme Campus Controller (XCC/XIQC)
  → **REST/JSON** on `:5825`.
- **Ruckus ZoneDirector** (ZD3050, firmware 10.x) → **internal Web XML (AJAX)** on `:443`.

Behavior required by the operator:

1. A screen under **Inventory → Wireless** lets the operator **add a controller manually**:
   IP + vendor (Extreme / Ruckus ZoneDirector) + credential (+ optional port/api-base).
2. Once a controller is added manually, HIMS uses **REST (Extreme) / Web-XML (Ruckus ZD)
   as the PRIMARY** collection method — **even if** the same device was already discovered
   by SNMP/SSH.
3. If a controller was **not** added manually (no vendor profile), the scanner keeps using
   the **current SNMP/SSH solution as the fallback** — unchanged.

This is **additive**. SNMP/SSH wireless collection stays exactly as-is and remains the
fallback. We are adding a higher-priority path and a manual-config gate to select it.

---

## 1. Current state of HIMS (what already exists — reuse it, don't reinvent)

HIMS already has ~80% of the scaffolding. Confirmed by code read:

| Concern | Where it lives | Reuse |
|---|---|---|
| **Manual controller config** | table `vendor_connection_profiles` (migration `000042`): `vendor_type, target_url, credential_id, location_id, device_id, config JSONB, enabled, status, last_test_*, last_collection_*`. CRUD + test + run-collection at `internal/api/vendor_profiles.go` (`/vendor-profiles`, `/vendor-profiles/{id}/test`, `/vendor-profiles/{id}/run-collection`). UI `web/src/pages/VendorProfiles.tsx`. | **This IS the "add controller manually" store.** Extend it. |
| **Encrypted credentials** | table `credentials` (AES-256-GCM `encrypted_blob`+`key_id`); `internal/secret.Cipher.Seal/Unseal`; key from `HIMS_ENCRYPTION_KEY` (dev: `bin/dev-encryption-key`). Never returned by API. | Store the controller admin password as a `http_basic` / `vendor_api` credential, bind via `vendor_connection_profiles.credential_id`. |
| **Collection abstraction** | `internal/driver`: `Driver` + optional `Collector{ Collect(Session, Probe) (Facts, error) }`; `Facts` carries `WLAN *WLANSnap`, `APs []APSnap`, `SSIDs []SSIDSnap`, `Stations []WirelessClientSnap`, `Radios []RadioSnap`, `WLANEvents []WirelessEventSnap`. | The normalized output shape. Map vendor data into `Facts`. |
| **Collection dispatch** | `internal/collect/collect.go`: `Controller(ctx, deps, kind, ip, loc, opts)` → `UniFi/Omada/Ruckus/Extreme/...`; `tryCandidates`; `persist`. Triggered by `POST /discovery/controller-import` (`internal/api/imports.go`) and by `POST /vendor-profiles/{id}/run-collection`. | Add `extreme_xcc` (fix) + `ruckus_zd` (new) dispatch. |
| **Persistence** | `internal/apply/apply.go`: `Apply()` upserts `wlan_controller_info`, `access_points`, `wireless_ssids`, `wireless_clients`, `wireless_radio_status`, `wireless_events` — each with a `source` column. | Reuse; we add a few client columns (§4.1). |
| **Wireless data model** | migrations `000016_wireless`, `000051_wireless_deep`. Tables above. sqlc in `internal/storage/postgres/queries/wireless.sql` → `internal/.../db/`. | Extend (§4.1). |
| **Wireless UI** | `web/src/pages/Inventory.tsx`, `web/src/pages/WirelessDetail.tsx` (`GET /devices/{id}/wireless`), `web/src/pages/Discovery.tsx` (Controllers tab → `POST /discovery/controller-import`). | Add the "Add Wireless Controller" form + show new fields. |
| **SNMP fallback** | Ruckus ZoneDirector SNMP MIB pack is **already built-in** (`internal/api/mib_packs.go`, `.mib-stage/ruckus/`), driven via `POST /devices/{id}/collect-wireless-mib`. | This is the fallback. Do **not** change it. |

### 1.1 The two gaps to close

1. **`internal/extremexcc`** (on-prem Extreme REST) is close but hits the **wrong endpoints**
   and misses fields. Critically it fetches `/aps` and `/stations`, but the proven tool
   uses **`/management/v1/aps/query`** and **`/management/v1/stations/query`** — on
   BridgedAtAp deployments `/stations` returns an **empty array** while `/stations/query`
   returns the real clients. It also lacks events, the real AP `status` field, SNR, and
   identity backfill. **Fix it** (§4.2).

2. **`internal/ruckus`** is a **SmartZone/vSZ REST** client (`/wsg/api/public/v9_1/...`) —
   **not** ZoneDirector. The operator's hardware is **ZoneDirector ZD3050**, which has **no
   REST API**; it uses the internal **Web-XML AJAX** interface. **Create a new package
   `internal/ruckuszd`** implementing the proven `/admin10/` login + CSRF + `_cmdstat.jsp`/
   `_conf.jsp` flow (§4.3). Leave `internal/ruckus` (SmartZone) untouched for SmartZone sites.

---

## 2. Target architecture & routing rule

```
Operator: Inventory → Wireless → "Add controller" (IP + vendor + credential [+ port/apiBase])
        → creates a vendor_connection_profiles row (enabled=true) bound to the controller device
        → triggers an immediate collection.

Collection of a wireless_controller device:
   resolve enabled vendor_connection_profile for (device_id) OR (ip+location, vendor_type)
   ├─ profile found (PRIMARY)          → vendor collector:
   │     vendor_type=extreme_xcc       → internal/extremexcc  (REST :5825)
   │     vendor_type=ruckus_zd         → internal/ruckuszd    (Web-XML :443)
   │     → Facts → apply.Apply(source="extreme_xcc_api" | "ruckus_zd_xml")
   └─ no profile (FALLBACK)            → existing SNMP/SSH wireless MIB collection (unchanged)
```

**The routing rule (single source of truth):** _“a wireless_controller with an enabled
vendor_connection_profile is collected via that profile's API/XML collector; otherwise it
falls back to SNMP/SSH.”_ Implement this in **one** function (§4.5) and call it from both the
scheduled monitoring sweep and the manual “Collect now” action so the precedence can never
drift.

`source` values written to the wireless tables (used by the UI to be honest about coverage):
`extreme_xcc_api`, `ruckus_zd_xml` (primary) vs `snmp_baseline` / `snmp_mib` / `ssh_cli`
(fallback). The UI already keys off `source`.

---

## 3. The PROVEN vendor reference (port this exactly)

Everything in this section is live-verified by the desktop tool. Do not re-derive it.

### 3A. Extreme — ExtremeCloud IQ Controller / XCC (on-prem REST)

- **Base:** `https://<ip>:5825` (self-signed TLS → the HTTP client must skip verification,
  gated by an `insecure`/`ssl_verify=false` profile flag).
- **Auth — OAuth2 password grant → JWT bearer:**
  `POST /management/v1/oauth2/token`, `Content-Type: application/json`,
  body `{"grantType":"password","userId":"<user>","password":"<pass>","scope":""}`.
  Response: `{"access_token":"<JWT>","token_type":"Bearer","expires_in":7200, ...}`.
  Use `Authorization: Bearer <access_token>` on every call; on `401`, re-auth once and retry.
- **Data endpoints (note the `…/query` variants — these are the fix):**

  | Need | Endpoint | Notes |
  |---|---|---|
  | **APs** | `GET /management/v1/aps/query` | The runtime view. Has the real `status` field + per-radio `noise`. **Not** `/aps`. |
  | **Clients** | `GET /management/v1/stations/query` | **Not** `/stations` (empty under BridgedAtAp). |
  | **SSIDs** | `GET /management/v1/services` | Config (no live client-count/band — derive). |
  | **Events** | `GET /platformmanager/v1/logging/events?startTime=<ms>&endTime=<ms>` | epoch **ms**; required (omitting → 422). Use now-24h … now. |
  | (no system/version endpoint) | — | model/version/uptime not exposed; version backfilled from AP firmware. |

- **Field mappings (AP — from `aps/query`):**
  `Name←apName`, `Serial←serialNumber`, `Model←platformName||hardwareType`,
  `MAC←macAddress`, `IP←ipAddress`, `Firmware←softwareVersion`,
  `Status←status` (`InService`→online, `OutOfService`→offline, `critical`/`major`/`minor`→
  keep label — **never** use `proxied`/`adoptedBy` as status), `Site←hostSite`.
  Index each `radios[]`: `(serial|opChannel) → noise` for SNR.
- **Field mappings (Client — from `stations/query`):**
  `MAC←macAddress`, `IP←ipAddress`, `Host←dhcpHostName||userName||deviceType`,
  `SSID←serviceName`, `AP←accessPointName||accessPointSerialNumber`,
  `Band←protocol||channel`, `RSSI←rss` (dBm),
  `RxBytes←inBytes`, `TxBytes←outBytes`,
  `SNR←` **computed** = `rss − radioNoise(accessPointSerialNumber, channel)`,
  `ConnectedSince←` **Not Available** (station record has only `lastSeen`).
- **Field mappings (SSID — from `services`):**
  `Name←ssid||serviceName`, `Enabled←status` (`"enabled"`),
  `Security←` derive from nested `privacy` object **type key** (e.g. `WpaPskElement`→`WPA/WPA2 PSK`)
  — **never** surface `presharedKey`, `Encryption←privacy.<type>.mode` (e.g. `aesOnly`),
  `VLAN←vlanId||dot1dPortNumber` (fallback), `ClientCount←` derived, `Band←` derived.
- **Field mappings (Event):** `Time←timestamp`(epoch ms), `Severity←severity`,
  `Category←component`, `Message←description`, `Source←` Not Available.
- **Derived:** controller `Version` ← most-common AP `softwareVersion`; SSID `ClientCount` ←
  count clients whose `serviceName==ssid`; SSID `Band` ← from those clients' channels
  (ch 1–14→2.4, 32–196→5, >196→6 GHz); SNR as above.
- **Not available on this firmware (build honest gates, don't fake):** controller Model,
  controller Uptime, client Connected-since.

### 3B. Ruckus ZoneDirector ZD3050 — internal Web-XML (AJAX)

- **Base:** `https://<ip>:443` (self-signed TLS → skip verify). Server is Embedthis-Appweb.
- **Admin path discovery (do NOT hardcode `/admin/`):** `GET /` with redirects disabled →
  `302 Location: /admin10/login.jsp`. Take the first path segment (`admin10`) as the admin
  base. (ZD 10.x = `/admin10/`; older = `/admin/`; the old `/admin/login.jsp` returns 500 on
  10.x.)
- **Login:** `POST /<admin>/login.jsp`, `application/x-www-form-urlencoded`,
  `username=<user>&password=<pass>&ok=Log In`. Success = **302 → dashboard.jsp**; a `200`
  that re-renders the login form = bad credentials. Sets cookie `-ejs-session-=…` (use a
  cookie jar).
- **CSRF token:** `GET /<admin>/_csrfTokenVar.jsp` → `<script>var csfrToken = 'XXXXXXXXXX';</script>`.
  Note the **misspelling `csfrToken`** and the token is **~10 chars** (don't require 12+).
  Send it as header `X-CSRF-Token` on every AJAX POST; without it the AJAX returns 1 byte.
- **AJAX requests** (`Content-Type: text/xml`):

  | Need | Endpoint | XML body |
  |---|---|---|
  | **APs** | `POST /<admin>/_cmdstat.jsp` | `<ajax-request action='getstat' comp='stamgr' enable-gzip='0'><ap LEVEL='1'/></ajax-request>` |
  | **Clients** | `POST /<admin>/_cmdstat.jsp` | `<ajax-request action='getstat' comp='stamgr' enable-gzip='0'><client LEVEL='2'/></ajax-request>` (**LEVEL=2** for byte counters) |
  | **SSIDs** | `POST /<admin>/_conf.jsp` | `<ajax-request action='getconf' comp='wlansvc-list' updater='w.0.5' />` |
  | **System** | `POST /<admin>/_conf.jsp` | `<ajax-request action='getconf' comp='system' updater='s.0.5' />` |
  | **Events** | `POST /<admin>/_cmdstat.jsp` | `<ajax-request action='getstat' comp='eventd'><pieceStat pid='1' start='0' number='300' requestId='evt.1' cleanupId='0'/></ajax-request>` |

  On `3xx` (expired session) re-login once and retry. Response is XML wrapped in
  `<ajax-response><response …>…`. Parse by collecting each `<ap>`/`<client>`/`<wlansvc>`
  element's **attributes** into a `map[string]string` (vendor attr-soup), then map.

- **Field mappings (AP `<ap>`):** `Name←ap-name||devname`, `Serial←serial-number`,
  `Model←model`, `MAC←mac`, `IP←ip`, `Firmware←firmware-version||build-version`,
  `Site←location||group-id`, `ClientCount←` sum of child `<radio num-sta>`,
  `Status←` map the numeric **`state`** code:
  `0=Disconnected, 1=Connected, 2=Approval Pending, 3=Upgrading Firmware, 4=Provisioning`,
  else `Unknown (N)` (RUCKUS-ZD-WLAN-MIB `ruckusZDWLANAPStatus`; **0/1 confirmed live** —
  all state=0 stale, all state=1 recently-seen). Keep the raw code in `Raw`.
- **Field mappings (Client `<client>`, LEVEL=2):** `MAC←mac`, `IP←ip`,
  `Host←hostname||user`, `SSID←ssid`, `AP←ap-name`, `Band←radio-type-text||radio-type`,
  `RSSI←received-signal-strength||signal-strength` (**true dBm**, e.g. -84),
  `SNR←snr || rssi || (received-signal-strength − noise-floor)` — **note the ZD `rssi`
  0–100 field IS the SNR**, verified `rssi == signal − noise`,
  `RxBytes←total-rx-bytes`, `TxBytes←total-tx-bytes`,
  `ConnectedSince←first-assoc` (epoch **seconds** → local time).
- **Field mappings (SSID `<wlansvc>`):** `Name←name||ssid`, `Security←authentication`,
  `Encryption←encryption`, `VLAN←vlan-id`, `Enabled←` Not Available (listed = active),
  `ClientCount←` derived, `Band←` derived from clients.
- **Field mappings (System `<system>`):** `Hostname←identity@name`,
  `ManagementIP←mgmt-ip@ip`; `Version←` Not Available → backfill from AP firmware;
  Model/Serial/Uptime Not Available.
- **Events:** **Not Available** — every eventd/alarm/syslog/eventList form returns
  `<response/>` with zero rows on this firmware. Honest gate; events would need SNMP traps.

> The exact provenance of every column (Direct / Derived / Fallback / Not Available) is in
> `D:\WebProjects\NetworkToolWinApp\FIELD_SOURCES.md`. Reproduce it in HIMS as §6 of this doc.

---

## 4. Implementation steps

> Conventions (`CLAUDE.md`): migrations are `migrations/NNNNNN_name.up.sql` (sequential,
> up-only), apply with `go run ./cmd/hims-migrate`; regenerate sqlc with `sqlc generate`;
> every commit compiles + `go vet` clean; **never** log/return secrets. Use the existing
> `Doer` HTTP abstraction with a cookie jar + insecure TLS option (mirror `internal/omada`).

### 4.1 Data model additions (migration + sqlc)

The wireless tables already exist. Add the few columns the proven mapping needs but the
schema lacks (client SNR / byte counters / assoc time, AP site/uptime). New migration
`migrations/0000NN_wireless_rest_xml.up.sql`:

```sql
ALTER TABLE wireless_clients
  ADD COLUMN IF NOT EXISTS snr            INT,
  ADD COLUMN IF NOT EXISTS rx_bytes       BIGINT,
  ADD COLUMN IF NOT EXISTS tx_bytes       BIGINT,
  ADD COLUMN IF NOT EXISTS connected_since TEXT NOT NULL DEFAULT '';

ALTER TABLE access_points
  ADD COLUMN IF NOT EXISTS site   TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS uptime TEXT NOT NULL DEFAULT '';
```

`wireless_events` already exists (used for XIQC events). Update the sqlc query files
(`internal/storage/postgres/queries/wireless.sql`): add the new columns to
`UpsertWirelessClient` / `UpsertAccessPoint` (and their `…Params`), then `sqlc generate`.

No new table is needed for "manual controllers" — `vendor_connection_profiles` is it.
Register two `vendor_type` values used by the wireless flow: **`extreme_xcc`** (exists) and
**`ruckus_zd`** (new). Their `config` JSONB carries the vendor params:
`{"port":5825,"api_base":"/management/v1","ssl_verify":false}` for Extreme,
`{"port":443,"ssl_verify":false}` for Ruckus ZD (admin path is auto-discovered).

### 4.2 Fix `internal/extremexcc` to the proven endpoints/fields

Edit `internal/extremexcc/collect.go` (and `client.go` for auth/identity). Minimum changes:

1. **AP fetch path order** → put the runtime query first:
   ```go
   apRows, apOut := c.fetch(ctx, "aps", base, []string{"/aps/query", "/aps", "/devices"})
   ```
   and map the real status + index radio noise (parse `radios[]` from each AP row):
   ```go
   ap := AP{
       Name: pick(m,"apName","name","hostname"), Serial: pick(m,"serialNumber","serial"),
       Model: pick(m,"platformName","hardwareType","model"), MAC: pick(m,"macAddress","mac"),
       IP: pick(m,"ipAddress","ip"), Firmware: pick(m,"softwareVersion","firmware","version"),
       Status: normStatus(pick(m,"status","apStatus","operationalStatus")), // InService/critical
   }
   indexRadioNoise(m, ap.Serial, radioNoise) // (serial|channel) -> noise, from m["radios"]
   ```
   Extend `normStatus` to map `inservice→online`, `outofservice→offline`, and keep
   `critical/major/minor` verbatim (don't collapse to "unknown").

2. **Client fetch path order** → query first (BridgedAtAp fix) + the real fields incl. SNR:
   ```go
   stRows, stOut := c.fetch(ctx, "clients", base, []string{"/stations/query", "/stations", "/clients"})
   ...
   st := Station{
       MAC: pick(m,"macAddress","mac"), IP: pick(m,"ipAddress","ip"),
       Hostname: pick(m,"dhcpHostName","hostname","userName","deviceType"),
       APName: pick(m,"accessPointName","accessPointSerialNumber","apName"),
       SSID: pick(m,"serviceName","ssid"), Band: pick(m,"protocol","channel","band"),
   }
   if v := pickInt(m,"rss","rssi","signal"); v != 0 { st.RSSI = &v }
   st.RxBytes = pickInt64(m,"inBytes"); st.TxBytes = pickInt64(m,"outBytes")
   if snr, ok := computeSNR(st.RSSI, pick(m,"accessPointSerialNumber"), pickInt(m,"channel"), radioNoise); ok {
       st.SNR = &snr
   }
   ```
   (Add `RxBytes/TxBytes int64` and `SNR *int32` to the `Station` struct.)

3. **SSID security** without leaking the PSK: read the nested `privacy` object's **type key**
   → `WPA/WPA2 PSK`, mode → encryption; never emit `presharedKey`.

4. **Events:** add a fetch of
   `GET /platformmanager/v1/logging/events?startTime=<nowMs-86400000>&endTime=<nowMs>`
   → map `timestamp/severity/component/description` into `[]WirelessEventSnap`.

5. **Identity:** populate `CollectResult.Version` from the most-common AP firmware; optionally
   decode the JWT `iss` claim for the controller serial (e.g. `XCC.2248E-C42CF`). Leave
   Model/Uptime empty (Not Available).

6. **Derive SSID ClientCount + Band** from the collected clients (group by `serviceName`;
   band from client channel) — do this in the driver/`apply` layer so it's shared with Ruckus.

Then update `internal/driver/extreme*/…` (the XCC driver) so its `Collect` maps the richer
`CollectResult` into `Facts` (APs/SSIDs/Stations/Events + `WLAN.Source="extreme_xcc_api"`).

### 4.3 New package `internal/ruckuszd` (ZoneDirector Web-XML) — reference implementation

Create `internal/ruckuszd/client.go`. This is a faithful Go port of the desktop tool's
`RuckusAjaxSession` + `RuckusZoneDirectorConnector` (`D:\WebProjects\NetworkToolWinApp\
NetworkTool\Connectors\Ruckus\`). Reference skeleton (idiomatic Go; fill in parsing per the
field maps in §3B):

```go
package ruckuszd

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type Doer interface{ Do(*http.Request) (*http.Response, error) }

// Client speaks the ZoneDirector internal AJAX interface. Doer MUST have a cookie jar
// and (for self-signed mgmt certs) InsecureSkipVerify, and MUST NOT auto-follow redirects
// (so we can read the admin-path 302 and detect expired-session 3xx).
type Client struct {
	BaseURL  string // https://<ip>:443
	Username string
	Password string
	Doer     Doer

	adminBase string // discovered, e.g. "admin10"
	csrf      string
	loggedIn  bool
}

func New(baseURL, user, pass string, doer Doer) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Username: user, Password: pass,
		Doer: doer, adminBase: "admin10"}
}

var csrfRe = regexp.MustCompile(`cs[fr]{2}Token\s*=\s*['"]([^'"]+)['"]`) // matches csfrToken & csrfToken

func (c *Client) Login(ctx context.Context) error {
	c.csrf, c.loggedIn = "", false
	c.discoverAdminBase(ctx) // GET / (no redirect) -> Location /admin10/... -> first segment

	form := url.Values{"username": {c.Username}, "password": {c.Password}, "ok": {"Log In"}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.url("login.jsp"),
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.Doer.Do(req)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	redirected := resp.StatusCode >= 300 && resp.StatusCode < 400 &&
		!strings.Contains(strings.ToLower(loc), "login")
	if resp.StatusCode == 500 {
		return fmt.Errorf("ruckuszd: server error on login (wrong admin path?)")
	}
	if !redirected && looksLikeLoginPage(body) {
		return fmt.Errorf("ruckuszd: login rejected — check username/password")
	}
	c.fetchCSRF(ctx) // GET <admin>/_csrfTokenVar.jsp -> csfrToken
	c.loggedIn = true
	return nil
}

type endpoint int

const (
	cmdStat endpoint = iota // _cmdstat.jsp (getstat)
	conf                    // _conf.jsp    (getconf)
)

// postAjax POSTs an XML body and returns the response bytes, re-logging-in once on 3xx.
func (c *Client) postAjax(ctx context.Context, ep endpoint, xmlBody string, retry bool) ([]byte, error) {
	if !c.loggedIn {
		if err := c.Login(ctx); err != nil {
			return nil, err
		}
	}
	page := map[endpoint]string{cmdStat: "_cmdstat.jsp", conf: "_conf.jsp"}[ep]
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.url(page), strings.NewReader(xmlBody))
	req.Header.Set("Content-Type", "text/xml")
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	resp, err := c.Doer.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 { // session expired
		resp.Body.Close()
		if !retry {
			return nil, fmt.Errorf("ruckuszd: session expired")
		}
		c.loggedIn = false
		if err := c.Login(ctx); err != nil {
			return nil, err
		}
		return c.postAjax(ctx, ep, xmlBody, false)
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	resp.Body.Close()
	return b, nil
}

// Request bodies (kept as constants — schema drifts between firmware generations).
const (
	apStatsXML  = `<ajax-request action='getstat' comp='stamgr' enable-gzip='0'><ap LEVEL='1'/></ajax-request>`
	clientXML   = `<ajax-request action='getstat' comp='stamgr' enable-gzip='0'><client LEVEL='2'/></ajax-request>`
	wlanListXML = `<ajax-request action='getconf' comp='wlansvc-list' updater='w.0.5' />`
	systemXML   = `<ajax-request action='getconf' comp='system' updater='s.0.5' />`
	eventsXML   = `<ajax-request action='getstat' comp='eventd'><pieceStat pid='1' start='0' number='300' requestId='evt.1' cleanupId='0'/></ajax-request>`
)

// rows walks the XML and returns every element named `local` as its attribute map
// (vendor attr-soup → map[string]string). Mirrors the desktop XmlFlatten.
func rows(b []byte, local string) []map[string]string {
	dec := xml.NewDecoder(strings.NewReader(string(b)))
	var out []map[string]string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		if se, ok := tok.(xml.StartElement); ok && strings.EqualFold(se.Name.Local, local) {
			m := make(map[string]string, len(se.Attr))
			for _, a := range se.Attr {
				m[strings.ToLower(a.Name.Local)] = a.Value
			}
			out = append(out, m)
		}
	}
	return out
}

// ListAPs / ListClients / ListSSIDs / SystemInfo / ListEvents:
//   - call postAjax with the matching const
//   - rows(b,"ap") / rows(b,"client") / rows(b,"wlansvc") / rows(b,"system")
//   - map per §3B (apState(), snr fallback, sum child <radio num-sta>, etc.)
// Helpers to implement: apState(code) string, looksLikeLoginPage, discoverAdminBase,
//   fetchCSRF, url(page) = BaseURL + "/" + adminBase + "/" + page, atoi/atoi64, bandFromChannel.
```

Helper `apState`:
```go
func apState(code string) string {
	switch strings.TrimSpace(code) {
	case "0": return "Disconnected"
	case "1": return "Connected"
	case "2": return "Approval Pending"
	case "3": return "Upgrading Firmware"
	case "4": return "Provisioning"
	case "": return ""
	default:  return "Unknown (" + code + ")"
	}
}
```

> The full, line-by-line logic (admin discovery, login detection, CSRF regex, the per-radio
> `num-sta` sum, the SNR fallback, the `first-assoc` epoch formatting) is in the desktop files
> `RuckusAjaxSession.cs` and `RuckusZoneDirectorConnector.cs`. Port them 1:1 — they are
> live-verified (233 APs, 730 clients, 9 SSIDs against ZD3050 `192.168.2.2`).

Then add `internal/driver/ruckuszd/ruckuszd.go` (a `Collector` like the other vendor
drivers) mapping into `Facts` with `WLAN.Source="ruckus_zd_xml"`.

### 4.4 Wire into `collect` + dispatch + vendor-profile run-collection

- In `internal/collect/collect.go` add `ExtremeXCC(...)` (use fixed `extremexcc`) and
  `RuckusZD(ctx, d, ip, loc, opts)` (use `ruckuszd`), following the existing `Ruckus`/`Extreme`
  pattern (`tryCandidates` with `domain.CredHTTPBasic`/`CredVendorAPI`, then `persist`).
- Add both to the `Controller(kind, …)` switch: `case "extreme_xcc": …`, `case "ruckus_zd": …`.
- In `internal/api/vendor_profiles.go`'s `runVendorProfileCollection`, map
  `vendor_type=="extreme_xcc"` → `collect.ExtremeXCC` and `=="ruckus_zd"` → `collect.RuckusZD`,
  passing `target_url`/`config.port`/`config.api_base` and the decrypted credential.
- In `internal/api/imports.go` accept `kind` values `extreme_xcc` and `ruckus_zd` in
  `controllerImportReq` so the Discovery → Controllers tab can launch them directly.

### 4.5 Primary-vs-fallback routing (the core rule, in ONE place)

Add `internal/collect/wireless_route.go`:

```go
// CollectWireless runs the PRIMARY (vendor profile REST/XML) collector when an enabled
// profile exists for the controller, else falls back to SNMP/SSH. This is the single
// authority for precedence — call it from the monitoring sweep and the manual action.
func CollectWireless(ctx context.Context, d Deps, dev db.Device) (Result, error) {
	prof, err := d.Queries.ResolveVendorProfileForDevice(ctx, dev.ID) // enabled, vendor_type in (extreme_xcc, ruckus_zd, …)
	if err == nil && prof.Enabled {
		return runProfileCollection(ctx, d, prof) // REST/XML — source = *_api / *_xml
	}
	return collectWirelessSNMP(ctx, d, dev) // existing MIB/SSH path — unchanged fallback
}
```

Add the sqlc query `ResolveVendorProfileForDevice` (enabled profile bound to `device_id`,
or matched by `target_url` host == device primary_ip within the same location). Replace the
scheduler's current wireless-collection call (and the UI "Collect now") with `CollectWireless`.

> Per `CLAUDE.md` discovery semantics: the scan still **classifies** a controller and binds a
> credential only on auth success; making REST/XML primary means _“if the operator supplied a
> vendor profile, prefer it.”_ It does not change classification or the SNMP bind-on-success
> rules for un-profiled controllers.

### 4.6 Facts struct + apply additions

`internal/driver/driver.go` — extend the wireless snaps to carry the new fields:
```go
type WirelessClientSnap struct {
	MAC, IP, Hostname, APName, SSID, Band string
	RSSI, SNR        *int32
	RxBytes, TxBytes *int64
	ConnectedSince   string
}
type APSnap struct { /* …existing… */ Site, Uptime string }
```
`internal/apply/apply.go` — pass the new fields through to `UpsertWirelessClient` /
`UpsertAccessPoint`. Add the SSID `ClientCount`/`Band` derivation here (group `Stations` by
`SSID`; band from client channel) so **both** vendors get it for free.

### 4.7 API + UI — "Inventory → Wireless → Add controller"

Backend is mostly present (`/vendor-profiles` CRUD/test/run-collection, `/devices/{id}/wireless`).
Add a thin convenience endpoint so the operator flow is one step:

`POST /api/v1/wireless/controllers` (handler in `internal/api/wireless.go`):
```jsonc
{ "vendor": "extreme_xcc" | "ruckus_zd",
  "ip": "172.21.96.100",
  "name": "Aqua XIQC",
  "location_id": "…",
  "username": "admin", "password": "…",   // sealed → credentials row; never stored plain
  "port": 5825,                            // optional (defaults: extreme 5825, ruckus_zd 443)
  "api_base": "/management/v1",            // optional (extreme)
  "ssl_verify": false }
```
The handler: seals the password into a `credentials` row (kind `http_basic`); creates the
`devices` row (category `wireless_controller`) if absent; creates an enabled
`vendor_connection_profiles` row (`vendor_type`, `target_url=https://ip:port`, `credential_id`,
`config`); then kicks `CollectWireless` async. Returns the device id + job id. Returns `503` if
`HIMS_ENCRYPTION_KEY` is unset (matches existing secret-write behavior).

Frontend (`web/src/`):
- Under **Inventory**, add a **Wireless** view (`pages/WirelessInventory.tsx`) listing
  `wireless_controller` devices with their `source` (API/XML vs SNMP), AP/client/SSID counts,
  and a **"+ Add controller"** button opening a form: **Vendor** (Extreme XCC / Ruckus
  ZoneDirector), **IP**, **Name**, **Site**, **Username**, **Password**, **Port** (prefilled),
  **Ignore TLS cert** (default on). Submit → `POST /api/v1/wireless/controllers`.
- Reuse `WirelessDetail.tsx` for the per-controller view; ensure it renders the new client
  columns (SNR, Rx/Tx, Connected-since) and shows `source` honestly (e.g. "Collected via
  Extreme REST API" / "Ruckus ZoneDirector Web-XML" vs "SNMP baseline").
- Keep the existing Discovery → Controllers tab; just add the two vendor kinds to its dropdown.

---

## 5. (in the doc) Field-source provenance table

Reproduce `D:\WebProjects\NetworkToolWinApp\FIELD_SOURCES.md` here as the per-column
Direct/Derived/Fallback/Not-Available reference for Overview, APs, Clients, SSIDs, Events,
for both vendors. It is the contract for what each column means and why some are blank. (It is
short; copy it verbatim and adjust column names to the HIMS table names.)

---

## 6. Honest gates (per CLAUDE.md — detection + UI + next-action, never silent)

| Gate | Vendor | Behavior |
|---|---|---|
| Events not exposed | Ruckus ZD | Collect returns 0 events; UI shows "Events not exposed by this ZoneDirector firmware (AJAX) — available via SNMP traps". Don't error. |
| Controller model/uptime | XIQC | Leave blank; UI label "not published by the controller API". Version is backfilled from AP firmware (label it "from AP firmware"). |
| Client connected-since | XIQC | Blank; tooltip "station record exposes lastSeen only". |
| No credential / missing vendor params | both | Profile `status="untested"`; WirelessDetail shows next-action "Add a vendor connection profile (IP + credential)". |
| `HIMS_ENCRYPTION_KEY` unset | both | Secret writes return `503` (existing behavior). |

These mirror the desktop tool's honest empty-states and keep parity with `CLAUDE.md`.

---

## 7. Verification (live — reproduce the desktop tool's proof)

Run these before/after to prove parity. Use a throwaway cookie jar; never commit secrets.

**Extreme (REST):**
```bash
TOKEN=$(curl -sk -X POST 'https://172.21.96.100:5825/management/v1/oauth2/token' \
  -H 'Content-Type: application/json' \
  -d '{"grantType":"password","userId":"admin","password":"<pw>","scope":""}' \
  | sed -n 's/.*"access_token"[^"]*"\([^"]*\)".*/\1/p')
curl -sk -H "Authorization: Bearer $TOKEN" 'https://172.21.96.100:5825/management/v1/aps/query'      | head -c 400  # expect status:"InService"
curl -sk -H "Authorization: Bearer $TOKEN" 'https://172.21.96.100:5825/management/v1/stations/query' | head -c 400  # expect clients (NOT [])
```

**Ruckus ZD (Web-XML):**
```bash
curl -sk -c j 'https://192.168.2.2/admin10/login.jsp' -o /dev/null
curl -sk -c j -b j --data-urlencode username=admin --data-urlencode 'password=<pw>' \
  --data-urlencode 'ok=Log In' -w '%{http_code}\n' 'https://192.168.2.2/admin10/login.jsp'   # expect 302
CSRF=$(curl -sk -b j 'https://192.168.2.2/admin10/_csrfTokenVar.jsp' | sed -n "s/.*csfrToken *= *'\([^']*\)'.*/\1/p")
curl -sk -b j -H "X-CSRF-Token: $CSRF" -H 'Content-Type: text/xml' \
  --data "<ajax-request action='getstat' comp='stamgr' enable-gzip='0'><ap LEVEL='1'/></ajax-request>" \
  'https://192.168.2.2/admin10/_cmdstat.jsp' | head -c 400   # expect <ap mac=... state=...>
```

**HIMS build/run gates (per commit):**
```bash
go build ./...        # compiles
go vet ./...          # clean
sqlc generate         # after query/schema changes; no diff drift
go run ./cmd/hims-migrate   # applies the new migration
go test ./internal/extremexcc/... ./internal/ruckuszd/...   # add table-driven parser tests with captured XML/JSON fixtures
```

**End-to-end acceptance:**
1. `POST /api/v1/wireless/controllers` with the Extreme controller → device appears under
   Inventory → Wireless with `source=extreme_xcc_api`, ~123 APs / clients / 4 SSIDs, AP status
   `In Service`/`Critical`, client SNR/Rx/Tx populated.
2. Same for Ruckus ZD → `source=ruckus_zd_xml`, ~233 APs / ~730 clients / 9 SSIDs, AP status
   `Connected`/`Disconnected`, client RSSI in dBm + SNR + Rx/Tx + Connected-since; Events tab
   shows the honest "not exposed" gate.
3. Delete the vendor profile → next collection of that device falls back to SNMP/SSH (`source`
   flips to `snmp_*`), proving the precedence rule.

---

## 8. Acceptance criteria (definition of done)

- [ ] Operator can add a controller (IP + vendor + credential) under **Inventory → Wireless**;
      it is stored as an encrypted credential + a `vendor_connection_profiles` row.
- [ ] A controller **with** a profile is collected via **REST (Extreme)** / **Web-XML (Ruckus
      ZD)** — primary — even if previously discovered by SNMP/SSH.
- [ ] A controller **without** a profile still collects via **SNMP/SSH** — fallback, unchanged.
- [ ] Extreme uses `/management/v1/aps/query` + `/stations/query` + `/services` + events; client
      SNR computed; AP status from the real `status` field; PSK never surfaced.
- [ ] Ruckus ZD uses `/admin10/` login + CSRF + `_cmdstat.jsp`/`_conf.jsp`; AP `state` mapped to
      text; client LEVEL=2 byte counters + RSSI(dBm) + SNR + `first-assoc`.
- [ ] New client columns (SNR, Rx/Tx, connected-since) persisted and shown; `source` shown
      honestly; not-available fields gated with a next-action.
- [ ] `go build` + `go vet` clean; `sqlc generate` no-drift; migration applies; parser unit
      tests pass on captured fixtures; live acceptance (§7) verified against both controllers.

---

## Appendix — canonical reference files in the desktop tool

| Concern | File (`D:\WebProjects\NetworkToolWinApp\NetworkTool\`) |
|---|---|
| Extreme REST connector (auth, aps/query, stations/query, services, events, SNR, JWT) | `Connectors\Extreme\ExtremeCloudIqConnector.cs` |
| Ruckus ZD session (admin discovery, login, CSRF, cmdstat/conf, re-auth) | `Connectors\Ruckus\RuckusAjaxSession.cs` |
| Ruckus ZD connector (field maps, AP state, SNR, LEVEL=2 bytes, system) | `Connectors\Ruckus\RuckusZoneDirectorConnector.cs` |
| Cross-link clients↔APs↔SSIDs; SSID band/clients derivation | `Core\ClientLinking.cs` |
| Epoch (ms/seconds/float) formatting | `Core\EpochUtil.cs` |
| Error classification (auth/TLS/timeout/unreachable) | `Core\ConnectorErrors.cs` |
| Per-column provenance (Direct/Derived/Fallback/Not-Available) | `..\FIELD_SOURCES.md` |
| Live API findings (exact endpoints, why /stations is empty, state codes, etc.) | memory `network-tool-live-findings` |

Port the C# 1:1 into Go; the HTTP/XML/JSON shapes are identical and already live-verified.
