# ZKTeco / Biometric Connector — protocol notes & parity rules

HIMS onboards ZKTeco fingerprint / time-attendance / access-control devices with a real,
**read-only** connector over the native ZK protocol (`internal/zkteco`), default **TCP/4370**.
It connects, authenticates, reads device **identity only** (serial / firmware / device name /
platform), then disconnects. These notes record the protocol behavior that was live-validated
against real hardware (`150.0.0.41-44`) so the parity rule is never regressed.

## The `ACK_UNAUTH` parity rule (important)

A ZKTeco device answers `CMD_CONNECT` with `CMD_ACK_UNAUTH` (2005) when it expects a
`CMD_AUTH` exchange. **`ACK_UNAUTH` does NOT always mean the operator must provide a custom
secret.** The ZK SDK default communication key is **`0`**, and IP-only SDK tools authenticate
by sending `CMD_AUTH` with `make_commkey(0, session)` — no operator secret at all.

Therefore the connector rule is:

1. On `ACK_UNAUTH`, send `CMD_AUTH` derived from the communication key.
2. An empty/absent key **defaults to `0`** (the SDK default) and is attempted automatically.
3. Mark the device `zkteco_comm_key_required` / `credential_failed` **only if the default key
   `0` is also rejected** (i.e. the device really has a non-default key set). A supplied
   non-default key is then stored **encrypted** (AES-256-GCM, credential kind `zkteco`),
   bound to the device, and reused for future collection — never returned in plaintext.

Do **not** declare a key "required" on `ACK_UNAUTH` before trying `0`. (This was the original
bug: `.41-.44` were wrongly reported as key-required when an external tool connected by IP
only — they simply use the default key `0`.)

## `makeCommKey` must be ZK/pyzk-compatible

The `CMD_AUTH` payload is derived by the documented ZK/pyzk algorithm: reverse the 32 key
bits, add the session id, XOR the four bytes with `'Z' 'K' 'S' 'O'`, swap the two 16-bit
halves, then emit `{b0^ticks, b1^ticks, ticks, b3^ticks}` with `ticks = 50`. The **third
output byte is the `ticks` value itself**, not the scrambled byte — getting this wrong makes
even key `0` fail with `ACK_UNAUTH` on real hardware (the fake test server cannot catch it, so
the gated `TestZKParity` live harness is the real guard).

## Protocol framing

- 8-byte command header `(command, checksum, session_id, reply_id)` + data, wrapped for TCP
  with an 8-byte top header `(0x5050, 0x7d82, length)`.
- `reply_id` starts at `USHRT_MAX-1` and is taken from each response; the checksum is computed
  over the buffer with the **current** `reply_id` while the **sent** `reply_id` is incremented.
  Real devices silently drop a packet whose checksum used a different `reply_id`.
- TCP is sufficient; UDP/4370 also speaks the protocol but is not required.

## Scope & safety

- **Read-only identity only.** Reads `~SerialNumber`, `~DeviceName`, `~Platform` (via
  `CMD_OPTIONS_RRQ`) and firmware (`CMD_GET_VERSION`), then `CMD_EXIT`.
- **Attendance logs and user/template data are intentionally OUT OF SCOPE** and are never
  read or written unless a future, explicitly-approved, read-safe path is implemented.
- A device is marked **managed only after real identity is collected** (a `zkteco`
  credential-test success is recorded); identity values are never fabricated — an empty field
  means the device did not expose it.

## Status vocabulary

- `implemented_collected` — real identity collected end-to-end from a real device (current
  state: `.41-.44` collected live).
- `zkteco_comm_key_required` — device rejected the default key `0`; a non-default key is set
  and must be supplied. Not managed.
- `credential_failed` — a supplied key was rejected. Not managed.

## Other biometric vendors

Hikvision / Dahua / generic reuse shared protocol clients (ISAPI / HTTP / ONVIF / SNMP) proven
on other device classes but not yet validated against a real biometric device of that vendor →
`implemented_live_validation_pending`. Suprema / Anviz deep inventory needs the vendor
SDK/server API → `external_dependency_required` (the auth test is available now). None are
overstated as collected/tested without a real device.
