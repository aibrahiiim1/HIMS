# HIMS — Device Classification, OS/NVR Discovery & Credential Testing

This document covers the production-readiness **add-on**: evidence-based device
classification, lightweight OS/NVR discovery probing, and the universal
credential tester. It is the operator + contributor reference for how a device
gets its `category`, `os_family`, subtype and `confidence_score`, and how to
verify which credentials work where.

## The classification model

A device's classification is **evidence-based and auditable** — never a black box.

| Field | Column | Meaning |
|---|---|---|
| Category | `devices.category` | top-level type (`switch`, `server`, `nvr`, `camera`, `firewall`, …) |
| OS family | `devices.os_family` | `windows` / `linux` / `network_os` / `embedded` / `macos` / `""` (unknown) |
| Subtype | `devices.device_class` | finer role: `windows_server`, `linux_server`, `domain_controller`, `nvr`, `ip_camera`, … |
| Confidence | `devices.confidence_score` | 0–100; `NULL` = unscored |
| Evidence | `devices.classification_evidence` | JSONB **array** of the signals that drove the decision |
| Lock | `devices.classification_locked` | operator manual override — auto-classification will not touch a locked device |

Each evidence item records **how** a signal was observed (`source`), the raw
observation (`signal`), what it points to (`category`/`os_family`/`subtype`) and
its individual weight (`confidence`). The classifier
(`internal/classify.FromEvidence`) merges them: the winning category is the one
whose strongest single signal — plus a small bonus per additional independent
corroborating source — scores highest (capped at 100).

### How evidence is gathered (`internal/osdiscovery`)

Lightweight, mostly-unauthenticated probing from the HIMS host:

- **TCP port profile** — RDP/WinRM/SMB → Windows hints; Kerberos+LDAP → domain
  controller; RTSP → camera/NVR; JetDirect → printer.
- **SSH banner** — OpenSSH/distro tokens → Linux; Cisco/Huawei → network OS.
- **HTTP `Server`/title** — `cisco-IOS` → switch; `Microsoft-IIS` → Windows;
  Hikvision web stack → camera/NVR.
- **Hikvision ISAPI** (`/ISAPI/System/deviceInfo`) — the definitive **NVR vs
  camera** signal via `<deviceType>` (`NVR`/`DVR` vs `IPCamera`).
- **SNMP sysDescr** (when already collected) and **AD computer OS string** also
  feed evidence.

> **Scope boundary.** This is *fingerprinting*, not deep inventory. Pure-Go
> WMI/DCOM from Linux is not viable, so deep Windows inventory (services,
> patches, installed software) is deferred to a future **Windows agent**. The
> add-on classifies `os_family`/category/subtype from reachable signals only.

> **NVR `deviceType` is credential-gated.** Hikvision ISAPI uses **Digest** auth.
> Without a bound camera credential the probe records "ISAPI present (401)" →
> `camera`/`embedded` at low confidence. Bind an `onvif`/`http_basic` credential
> to read `<deviceType>` and confirm NVR vs camera at high confidence.

### Endpoints

- `GET  /api/v1/devices/{id}/classification` — current classification + parsed evidence.
- `POST /api/v1/devices/{id}/reclassify` — probe live, re-run the classifier, persist
  (never downgrades a known category on a no-signal probe; a locked device is left untouched).
- `POST /api/v1/devices/{id}/classification-lock` — `{ "locked": true|false }` manual override.

All three are `devices`-permission gated, site-scoped, and audited. The
device-detail **Classification** card exposes Re-classify + Lock/Unlock and the
evidence trail. The **Data Quality** center surfaces:
`unknown_category` (unclassified) and `low_confidence` (auto-classified < 50% —
confirm and Lock).

## Universal credential testing

Verify which credentials authenticate to which devices, in any combination
(one-to-many, many-to-one, N×M), without exposing secrets.

- `POST /api/v1/credentials/test` — body `{ credential_ids:[], device_ids:[], legacy_kex?:bool }`,
  `credentials.manage`-gated, site-scoped on the device IDs, bounded to 500 pairs/run.
- Secrets are decrypted **server-side only** to run the probe — they are **never
  returned or logged**. Results carry only protocol, category
  (`success` / `auth_failed` / `unreachable` / `unsupported` / `error`), a
  non-secret detail, and latency. The action audits **counts only**.
- Probes per kind (`internal/credtest`): SNMP (v2c/v3 sysDescr read), SSH
  (handshake + auth only — no command, no side effect), HTTP basic, ONVIF
  (`GetDeviceInformation`), WinRM.
- **Legacy switches:** old Cisco/Aruba gear negotiates only legacy SSH KEX/ciphers.
  If a test returns "handshake failed (try legacy KEX)", re-run with the
  **Legacy SSH KEX** toggle on.

The UI lives on the **Credentials** page (Administration): pick credentials +
devices, optionally enable legacy KEX, and read the secrets-free result grid.

> HIMS never guesses or brute-forces device passwords — the operator supplies
> them. The success path is exercised once a real working credential is created/bound.
