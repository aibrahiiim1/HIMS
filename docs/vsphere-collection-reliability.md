# vSphere / ESXi Collection Reliability & Credential Handling

> Authoritative rules for collecting VMware ESXi / vCenter hosts, and for handling
> SSH and malformed credentials. Written after the `150.0.0.0/24` discovery, where
> physical servers, Hyper-V, and ESXi hosts appeared "unmanaged". Companion to
> [`windows-collection-reliability.md`](windows-collection-reliability.md).

## 1. ESXi account lockout — NEVER spray credentials at vSphere

ESXi locks the **root** account after repeated failed logins. The host defaults are
`Security.AccountLockFailures = 5` and `Security.AccountUnlockTime = 900` (15 min): **5
failed logins → root locked for 15 minutes**. While locked, **even the correct
credential is rejected** with the SOAP fault `ServerFaultCode: Cannot complete login
due to an incorrect user name or password`.

Therefore HIMS must **never spray many credentials against an ESXi/vSphere SOAP API**:

- **If the device has a BOUND credential, try ONLY that credential** (a single login).
  A bound credential is a prior proven success — it must never be buried under, or
  followed by, a multi-credential spray that re-locks the account on every scan.
- **Discovery (nothing bound) caps attempts well below the lockout threshold.** The cap
  is `maxVSphereCands = 3` (under ESXi's 5-failure default), so a from-zero scan can try
  a few candidates to find the right one without tripping the lockout.
- A successful login **binds** the credential, so every subsequent scan is a single
  bound-credential login — no spray, no lockout.

**The 150.0.0.0/24 case:** `root/<pw>` authenticated on a single clean attempt and
returned full host + VM inventory on 5 of 6 ESXi — but during discovery HIMS tried 8
stored credentials, locked out root, and then read the *correct* credential as
"incorrect user name or password". The fix (cap + bound-credential-alone) is in
`internal/api/vsphere_collect.go`. `150.0.0.20` legitimately stays `credential_failed`
— its root password genuinely differs (rejected over BOTH SSH and the vSphere API) — and
its Action Center reason is `vsphere_credential_required` (provide the correct root
password for that host), NOT a spray target.

## 2. A vSphere auth rejection is a CREDENTIAL problem, not a transient error

The govmomi SOAP fault for a bad login names neither "authentication" nor "401", so it
previously fell through to a generic `collection_error` — which reads like a HIMS/transient
bug and hides the real fix. `categorizeCollectErr` now maps these to **`auth_failed`**
(→ `credential_failed`): `incorrect user name or password`, `cannot complete login`,
`incorrect password`, `invalid login`, `login failure`. So a wrong ESXi credential is
honestly a credential problem the operator must fix, never a transient collection retry.

## 3. ESXi root is the SAME credential for SSH and the vSphere API

On ESXi, the `root` account authenticates both the vSphere SOAP API and SSH. HIMS accepts
`vendor_api`, `http_basic`, AND `ssh`-kind credentials as vSphere login candidates. A host
whose vSphere API is restricted/locked may still be reachable over SSH with the same
credential (and vice-versa) — but the same lockout discipline applies: prefer the bound
credential, never spray.

## 4. SSH legacy handshake failure is SEPARATE from authentication failure

Two distinct SSH outcomes that must never be conflated:

- **Handshake / algorithm-negotiation failure** — the client and server share no common
  key-exchange, cipher, or host-key algorithm. Modern `x/crypto/ssh` no longer offers the
  SHA-1 host-key algorithms (`ssh-rsa`/`ssh-dss`) or legacy KEX by default, so an old
  switch/server fails **before** authentication is even attempted. HIMS now **auto-retries
  with a legacy ladder** (diffie-hellman-group1/14-sha1 + CBC ciphers + `ssh-rsa`/`ssh-dss`
  host keys) when the first modern handshake fails at algorithm negotiation
  (`isHandshakeAlgoError` in `internal/credtest`). This is transport, NOT a credential
  problem — never reported as `auth_failed`.
- **Authentication failure** — the handshake succeeded and the server rejected the
  username/password. This IS a credential problem (`auth_failed`).

A genuinely non-compliant SSH server (advertises `ssh-rsa` but signs `rsa-sha2-256`) cannot
be validated by Go's strict client even with the legacy ladder — that is a server firmware
defect, recorded as such, not a HIMS gap. OpenSSH connects because it is lenient.

## 5. Malformed credentials are rejected at create/update time

A `user:password` credential (kinds: ssh / winrm / onvif / http_basic / vendor_api) stored
**without a password** can never authenticate — the secret has no `:` (so it silently
becomes username-only with an empty password) or the password half is blank. This is the
defect behind the 150.0.0.0/24 ESXi credential `C0r@lSe@` (→ user=`C0r@lSe@`, pass="") and
an earlier empty-password SSH credential. `malformedUserPassSecret` now **rejects these at
create AND update time** with a clear message. SNMP communities (no colon — the whole
secret IS the community) are exempt.

## 6. Operator checklist for "ESXi/servers not managed"

1. Is the bound credential well-formed `user:password`? (A malformed one is now rejected;
   re-enter it correctly.)
2. Is the ESXi root account **locked** from an earlier spray? Wait 15 min, then a single
   bound-credential login succeeds.
3. A `credential_failed` ESXi means the password is genuinely wrong for THAT host — supply
   the correct root credential and bind it (do not rely on discovery spray).
4. A Linux/appliance reported `handshake failed` needs the legacy SSH ladder (automatic);
   `auth_failed` needs the correct SSH login.
5. SNMP `auth_failed` (no response) means the community is wrong OR SNMP is disabled —
   confirm the community and version (v2c vs v3) before assuming a device is unmanageable.
