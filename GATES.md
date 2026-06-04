# HIMS — Operational Gate Activation Runbook (Production Readiness P2)

Several HIMS features are code-complete and protocol-verified but need a real
prerequisite to produce live data: a **bound device credential**, a **device
configured to export**, or an **SMTP server**. This runbook lists each gate, its
verified status, and the exact steps to activate it on the deployment.

Reachability confirmed from the build host during P2 (informs these steps):

| Target | Reachable ports | Evidence |
|---|---|---|
| `172.21.96.39` (switch) | 22 (SSH), 80, 443 | `Server: cisco-IOS`, `WWW-Authenticate: Basic realm="level_15_access"` → Cisco IOS |
| `172.21.96.1/.55` | 80 | limited web |
| `172.21.210.2–9` (CCTV) | 443, 554 (RTSP) | `GET /ISAPI/System/deviceInfo` → 401 → **Hikvision** ISAPI present |

So for the reachable devices the binding constraint is **credentials**, not the
network. HIMS never guesses device passwords — the operator supplies them.

---

## ✅ Gate 1 — Off-box pg_dump backup (ACTIVATED + verified)

Activated during P2: a real `pg_dump -Fc` of the HIMS DB (2.6 MB) was produced
and recorded; DR readiness flipped "Full DB pg_dump scheduled off-box" to ✅.

Operator (recurring, on the deployment host with DB access):
```
pg_dump "$HIMS_DATABASE_URL" -Fc -f /backups/hims-$(date +%F).dump
# copy /backups off-box (S3 / NAS / tape), then record it in HIMS:
#   Administration → Backup & Restore → Record External Backup (size + location)
```
Schedule it (cron / Task Scheduler). **Also back up `HIMS_ENCRYPTION_KEY` off-box**
— it is the only checklist item that stays red until done, and without it a
restored DB cannot decrypt any credential.

## ⏳ Gate 2 — SSH config backup on a switch (network-ready; needs credential)

Target `172.21.96.39` (Cisco IOS) is reachable on SSH. To activate:
1. Administration → Credentials → New credential, kind **ssh**, secret
   `username:password` for the switch's IOS login.
2. Inventory → the switch → bind that credential (or bind a site credential group).
3. Operations → Config Backup & Drift → select the switch → **Back up now**.
   - Per-vendor command is auto-selected (`show running-config`); legacy-KEX
     retry is automatic for older gear.
4. Verify a version appears (encrypted at rest); run a second backup to see
   drift = "no change".

Until a real `ssh` credential is bound, the endpoint returns the actionable
gate `bound credential is "<kind>", need an 'ssh' credential`.

## ⏳ Gate 3 — NetFlow live analytics (needs an exporter)

The v5 collector is verified (decode/aggregate). To get live data:
1. Ensure HIMS is started with `HIMS_NETFLOW_ADDR=:2055` (default) and the host
   firewall allows inbound UDP/2055 from the exporters.
2. On a switch/router/firewall, export **NetFlow v5** to `<hims-host>:2055`.
   - Cisco IOS example:
     ```
     ip flow-export version 5
     ip flow-export destination <hims-host> 2055
     interface <uplink>
       ip flow ingress
       ip flow egress
     ```
   - FortiGate: configure a NetFlow collector at `<hims-host>:2055`.
3. Verify within ~1 minute at Network → NetFlow: "Exports Received" rises and
   Top Talkers / Protocols populate.

## ⏳ Gate 4 — Scheduled report email (needs SMTP)

The email path is verified (test attempts a real SMTP dial; without a server it
returns `dial tcp …:25: connection refused`). To activate:
1. Administration → Notifications → New channel, type **email** (SMTP host/port,
   from, recipients; password encrypted at rest). Use **Test** to confirm
   delivery.
2. Reports → Scheduled → New schedule: pick report + frequency + that email
   channel. Use **Run now** to confirm `status: sent`.

## ⏳ Gate 5 — ONVIF / RTSP CCTV collection (Hikvision NVRs reachable; needs credential)

The `172.21.210.x` devices are Hikvision (ISAPI confirmed) on 443 + RTSP 554.
1. Credentials → New credential (kind **onvif** or **http_basic**) with the
   camera/NVR login.
2. Bind to the device(s) / site; run the ONVIF collection (collector binary /
   controller import) to populate manufacturer/model/resolution/channels.
3. Camera/NVR detail pages then show deep ONVIF fields; NVRs surface their
   channel map. (Classification of these as NVR vs camera is the OS/NVR
   discovery add-on.)

## ⏳ Gate 6 — Wireless / PBX collection (where present)

Bind the vendor REST / AXL credential (http_basic / vendor_api) to the
controller / call manager and run collection; AP inventory and phone registry
populate. No such controllers were reachable from the build host.

---

### Summary

| Gate | Status |
|---|---|
| Off-box pg_dump backup | ✅ activated + recorded + DR-verified |
| SSH config backup (96.39) | network-ready — bind an `ssh` credential |
| NetFlow live analytics | collector verified — configure an exporter |
| Scheduled report email | path verified — configure an SMTP channel |
| ONVIF/RTSP CCTV (210.x Hikvision) | reachable — bind an `onvif`/`http_basic` credential |
| Wireless / PBX | bind vendor/AXL credential where a controller exists |

Every gated item is **software-complete and protocol-verified**; activation is an
operator credential/exporter/SMTP step on the live deployment, not a code change.
