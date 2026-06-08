# Wireless field sources & honest gates (REST/XML primary collection)

For every wireless column HIMS stores, this records **where the value comes from**
and **how trustworthy it is** — per vendor. It is the contract for what each column
means and why some are blank. Reproduced from the live-verified desktop tool's
`FIELD_SOURCES.md`, adapted to HIMS table/column names.

Verified live on **2026-06-08**: Extreme XCC `172.21.96.100:5825`
(`source=extreme_xcc_api` — 123 APs, 4 SSIDs, 339 clients) and Ruckus ZoneDirector
ZD3050 `192.168.2.2:443` (`source=ruckus_zd_xml` — 233 APs, 9 SSIDs, 748 clients).

| Vendor | Product | Transport | Auth |
|---|---|---|---|
| **Extreme XCC** | on-prem ExtremeCloud IQ Controller | REST/JSON `https://host:5825` | OAuth2 password grant (JSON) → JWT bearer |
| **Ruckus ZD** | ZoneDirector ZD3050 (fw 10.x) | internal Web-XML AJAX `https://host:443` | web login → `-ejs-session-` cookie + `csfrToken` |

**Mapping type** — *Direct*: read from one raw field · *Derived*: computed/aggregated
by HIMS · *Fallback*: alternate field when the primary is absent · *Not Available*:
the API doesn't expose it on this firmware (left blank, never fabricated).

## Controller (`wlan_controller_info`)
| Column | Extreme XCC | Ruckus ZD |
|---|---|---|
| `vendor` | "Extreme Networks" (Direct) | "Ruckus Wireless" (Direct) |
| `version` | **Derived** — most-common AP `softwareVersion` (no controller version endpoint) | **Derived** — most-common AP `firmware-version` |
| `controller_name` | device name / JWT (Fallback) | `<system><identity name>` (Direct, e.g. CSHV-ZD) |
| `serial` | JWT `iss` suffix (Direct) | **Not Available** (not in ZD system config) |
| `model` | **Not Available** | **Not Available** |
| `source` | `extreme_xcc_api` | `ruckus_zd_xml` |

## Access points (`access_points`)
| Column | Extreme XCC (`/management/v1/aps/query`) | Ruckus ZD (`getstat comp='stamgr' <ap LEVEL=1>`) |
|---|---|---|
| `name` | `apName` (Direct) | `ap-name`/`devname` (Direct) |
| `serial` | `serialNumber` | `serial-number` |
| `model` | `platformName`/`hardwareType` | `model` |
| `mac` / `ip` | `macAddress` / `ipAddress` | `mac` / `ip` |
| `status` | **Direct** — real `status`: `InService`→"In Service", `critical`→"Critical", `OutOfService`→"Out of Service" (never derived from proxied/adoptedBy) | **Direct (mapped)** — numeric `state`: 0=Disconnected, 1=Connected, 2=Approval Pending, 3=Upgrading, 4=Provisioning, else "Unknown (N)" |
| `firmware` | `softwareVersion` | `firmware-version`/`build-version` |
| `client_count` | `clientCount` (Direct) / derived | **Derived** — sum of child `<radio num-sta>` |
| `site` | `hostSite` (Direct) | `location`/`group-id` (Fallback) |

> AP `status` is a vendor label rendered as a badge — the original
> `online|offline|unknown` CHECK was dropped (migration 000058) so the real
> operational states persist.

## Clients (`wireless_clients`)
| Column | Extreme XCC (`/management/v1/stations/query`) | Ruckus ZD (`<client LEVEL=2>`) |
|---|---|---|
| `mac` / `ip` | `macAddress` / `ipAddress` | `mac` / `ip` |
| `hostname` | `dhcpHostName`→`deviceType` (Fallback) | `hostname`/`user` |
| `ssid` | `serviceName` | `ssid` |
| `ap_name` | `accessPointName`/`accessPointSerialNumber` | `ap-name` |
| `band` | `protocol`/`channel` | `radio-type-text`/`radio-type` |
| `rssi` | `rss` (dBm, Direct) | **`received-signal-strength`** (true dBm — *not* the `rssi` field) |
| `snr` | **Derived** — `rss` − AP-radio `noise` (matched by serial+channel from `aps/query` `radios[]`); blank if unmatched | **Fallback** — the ZD `rssi` field *is* the SNR (verified `rssi == signal − noise`), else `snr`, else signal−noise |
| `rx_bytes` / `tx_bytes` | `inBytes` / `outBytes` (Direct) | `total-rx-bytes` / `total-tx-bytes` (Direct, requires LEVEL=2) |
| `connected_since` | **Not Available** — station record exposes only `lastSeen` | `first-assoc` (epoch → local, Direct) |

## SSIDs (`wireless_ssids`)
| Column | Extreme XCC (`/management/v1/services`) | Ruckus ZD (`getconf comp='wlansvc-list'`) |
|---|---|---|
| `name` | `ssid`/`serviceName` | `name`/`ssid` |
| `status` | `status` ("enabled") | **Not Available** (listed = active) |
| `security` | **Derived** — `privacy` object type key (e.g. WpaPsk2→"WPA/WPA2 PSK (aesOnly)"); the PSK is **never** surfaced | `authentication` (Direct) |
| `vlan` | `vlanId`/`dot1dPortNumber` (Fallback) | `vlan-id` (Direct) |
| `client_count` | **Derived** — count of stations whose SSID matches | **Derived** (same) |
| `band` | **Derived** — from the SSID's clients' channels (1–14→2.4, 32–196→5, >196→6 GHz); blank when no active clients | **Derived** (same) |

## Events (`wireless_events`)
| Column | Extreme XCC | Ruckus ZD |
|---|---|---|
| all | `platformmanager/v1/logging/events` (last 24h; epoch-ms range required) → `timestamp`/`severity`/`component`/`description` (Direct) | **Not Available** — the ZD AJAX interface returns zero rows on this firmware; events need SNMP traps |

## Honest gates (detection + UI + next-action, never silent)
| Gate | Vendor | Behaviour |
|---|---|---|
| Events not exposed | Ruckus ZD | Collect returns 0 events; detail notes "events not exposed by this ZoneDirector firmware (AJAX) — available via SNMP traps". Not an error. |
| Controller model / uptime | Extreme XCC | Left blank; version is backfilled from AP firmware. |
| Client connected-since | Extreme XCC | Blank — the station record exposes only `lastSeen`. |
| No credential / missing vendor params | both | Profile `status=untested`; WirelessDetail shows a "configure the vendor profile" next-action. |
| `HIMS_ENCRYPTION_KEY` unset | both | `POST /wireless/controllers` returns 503 (secret-write guard). |
| TLS legacy ciphers | Ruckus ZD | The ZD's Appweb TLS needs legacy cipher suites; the ZD HTTP client enables the full secure+legacy set (matches curl). |
