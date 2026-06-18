# Windows Collection Reliability & Discovery State Model

> Authoritative description of how HIMS collects Windows hosts under a full-subnet
> discovery storm and decides each host's final state. Written after the
> `172.21.60.0/24` fresh-discovery acceptance (from-zero #6, 83/83 managed).
>
> **Core principles (read first):**
> 1. **WinRM negotiation failures are treated as transient unless proven otherwise.**
> 2. **No single WinRM 401 / "invalid content type" pattern is terminal by itself.**
> 3. **Governed retry + WMI/DCOM fallback + self-heal decide the final state.**
> 4. **"Host-policy-blocked" is an operator-facing diagnosis only AFTER every automatic
>    option is exhausted** — never a premature terminal classification.

---

## 1. The Windows collection ladder (relay agent)

The API service runs as LocalSystem and cannot authenticate to domain Windows hosts, so
Windows collection is routed to the in-domain **relay agent**, which runs a multi-rung
ladder per candidate credential and stops at the first success (`cmd/hims-agent`):

1. **WinRM command shell** (`collectWinRM`, Go `masterzen/winrm` + `go-ntlmssp`).
   NTLM/Negotiate over HTTP/5985 **with WSMan message encryption** (`AllowUnencrypted`
   is *not* used — the payload is NTLM-sealed). `Get-CimInstance`/registry reads run
   locally inside the shell, so a **local-admin** account succeeds where remote WMI is
   UAC-blocked. Writes `collection_method = winrm-agent`.
2. **WMI / DCOM** (`collectWMI`, PowerShell `Get-WmiObject` over RPC/135). Fallback for
   hosts with WinRM unusable but DCOM open. Writes `collection_method = wmi`.
   - Internally this rung *also* falls back to a **WSMan CIM session** (`New-CimSession`)
     when DCOM (`Get-WmiObject`) throws — same classes over a different transport.

**Short-circuit rule (`winRMShortCircuitsWMI`):** only a clean **`auth_failed`** stops
the ladder before WMI (the same credential would be rejected over DCOM too, and a second
logon only adds lockout pressure). **Every other WinRM failure — connect-timeout,
negotiate-error (401 invalid content type), refused/closed — falls through to WMI/DCOM**,
because WMI is a different transport (RPC/135) and a WinRM transport problem says nothing
about DCOM reachability. This was the fix that recovered `.161`/`.194` (WinRM 5985 closed,
RPC 135 open → collected over WMI).

When **both** rungs fail, `pickWindowsFailCat` keeps a retryable WinRM transient as the
headline (so the host retries with backoff and self-heal stays eligible — never masked
into a terminal verdict by a UAC `wmi_access_denied`). Otherwise the WMI verdict (which
proves the host was reached over DCOM) wins.

---

## 2. Classifier categories — auth vs transport vs method-authorization

Failure categories are split by *what they prove*, which decides retry + final state:

| Category | Layer | Meaning | Retryable? | Auth failure? |
|---|---|---|---|---|
| `auth_failed` | WinRM auth | clean 401/unauthorized — credential rejected | no | **yes** → `credential_failed` |
| `winrm_connect_timeout` | WinRM transport | 5985 did not respond (host usually pings) | **yes** | no |
| `winrm_negotiate_error` | WinRM negotiation | 401 with non-SOAP body ("invalid content type") — listener rejected the handshake (typically **under scan-storm load**) | **yes** | no |
| `auth_ok_operation_fault` | WSMan op | NTLM ok, WSMan SOAP fault — legacy WSMan 2.0 | n/a | no → `needs_agent` |
| `unreachable` | WinRM transport | refused/reset/no route | yes | no |
| `wmi_access_denied` | WMI authz | authenticated over DCOM but namespace/DCOM access denied (UAC) | no | **yes** |
| `wmi_auth_failed` | WMI auth | DCOM logon rejected | no | **yes** |
| `rpc_unreachable` / `dcom_unreachable` / `firewall_blocked` | WMI transport | RPC/135 not reachable | yes | no |

**Key distinction:** a **transport/negotiation** failure (WinRM or WMI) is *never* an
auth failure — it is at worst `collection_failed` (operator/transport-fixable), never
`credential_failed`. Only a clean credential rejection (`auth_failed`, `wmi_access_denied`,
`wmi_auth_failed`) drives `credential_failed`.

**Why no `winrm_negotiate_error` is terminal:** during the `172.21.60.0/24` acceptance,
`.106`/`.119` returned a *persistent-looking* `401 invalid content type` + WMI
`Access is denied` across 10+ attempts over two runs — yet on the next governed run they
collected via `winrm-agent` on the first try **with no host-side change** (operator
confirmed: no WinRM / `AllowUnencrypted` / `LocalAccountTokenFilterPolicy` / firewall /
listener change). The failures were **load/timing-induced negotiation instability** under
the full-subnet storm, not a host policy wall. Therefore that pattern stays a **retryable
transient**; a dedicated terminal category for it was tried and reverted (it would
short-circuit the very persistence that fixes such hosts).

---

## 3. The four reliability layers (defense in depth)

A full-subnet scan enqueues one `collect_os` job per Windows host. The pressure that combo
puts on weak WinRM listeners is handled by four composing layers:

### Layer 1 — Retry envelope
`collect_os` jobs get `max_attempts = 5` (migration `000081`) with a long-tailed backoff
(`agentRetryBackoff`: 30s, 2m, 5m, 10m). The final retry lands ~17.5 min after the first
failure — past a typical ~12-min subnet drain — so a host that only failed under load
gets a post-storm attempt. Auth rejections are not retried.

### Layer 2 — Adaptive load governor
The per-agent dispatch cap (`agentDispatchCap`, default 8) bounds peak concurrency. On top
of it, `agentPollBudgetAdaptive` watches `CountAgentLoadBackoff` (the agent's jobs bouncing
on load-induced transient backoff); when that crosses a threshold the poll budget is
**clamped to a trickle** (`max(2, cap/4)`) so concurrent WinRM negotiations drop and the
listeners recover, then reopens to the full cap as the backoff queue drains. Regulates
pressure at the source instead of only retrying after the damage.

### Layer 3 — Self-heal sweep
`StartCollectionSelfHeal` (every 5 min) re-collects hosts left in a **terminal transient**
`collection_failed` (latest job failed with `winrm_negotiate_error` /
`winrm_connect_timeout` / `agent_no_result`, **no** os_inventory evidence, **no** in-flight
job, failure older than a 15-min cooldown, and fewer than 4 failed transient rounds in 24h).
It re-routes through the same `routeViaSiteAgent` path (fresh 5-attempt envelope). It
**never** re-collects an auth/authz failure (no credential re-spray) and the 4-round/24h
budget bounds a genuinely-blocked host so it cannot loop forever. *(In the #6 acceptance,
self-heal re-enqueued 0 hosts — the retry envelope + WMI fallback sufficed. It is the
safety net, not the primary path.)*

### Layer 4 — WMI/DCOM fallback after WinRM transport failure
See §1. The most-used method on `172.21.60.0/24` is `wmi` (39 of 68 os_inventory hosts).

---

## 4. State precedence (`deriveManagement`)

Two independent axes, never conflated: **Reachability** (online/offline from monitoring)
and **Management** (can HIMS authenticate + collect). Management is derived from *proven
evidence*, in this precedence:

1. **`access.hasProven()` → `managed`.** Proven = an os_inventory row (any
   `collection_method`) OR a credential-test that *ever* succeeded. **Durable — wins over
   any later failure.** (This is why `.183`, which has inventory but a later redundant
   `agent_no_result` job, stays `managed`.)
2. NVR-channel camera (managed via the recorder) → `managed`.
3. Active collect job in flight (incl. retry-waiting) → `pending_collection`.
4. legacy WSMan (`auth_ok_operation_fault`) → `needs_agent`.
5. `auth`-classified failure → `credential_failed`.
6. bound credential but nothing collected → `collection_failed`.
7. tested, non-auth failure (transport) → `collection_failed`.
8. Windows-like, never attempted → `not_attempted`.
9. credentialed class never attempted → `needs_credential`.
10. else → `unmanaged`.

A transient/transport failure can only ever reach `collection_failed` (7) — and only
after the retry + self-heal budget is exhausted — never `credential_failed`.

---

## 5. UI phase honesty

`scanPhase` derives the operator-facing scan phase from probe status + collection drain +
self-heal eligibility, so the UI never shows a premature "complete":

`queued → discovering → collecting → self_healing → complete`

- **collecting** while any `collect_os` job is in flight.
- **self_healing** while any host is still self-heal-eligible (incl. the cooldown window,
  where no job is in flight yet).
- **complete** only when in-flight = 0 **and** self-heal-eligible = 0.

The Scan Jobs list, the job header, and the settle poller all use the *same* definition of
done. Transient failure categories surface a precise, non-terminal remediation hint
(retried + WMI-fallback'd, never "wrong password"); the "Fix the rejected credential" hint
is keyed only off `credential_failed`.

---

## 6. `172.21.60.0/24` fresh-discovery acceptance (from-zero #6)

**ACCEPTED.** From a true from-zero (all devices deleted), the governed pipeline settled at
**83/83 managed, 0 `collection_failed`, 0 `credential_failed`, 0 `needs_credential`, 0
`not_attempted`, 0 `pending_collection`**, with queued/dispatched/retry_waiting/self-heal
all 0, stable for two checks (~56 min). Gate hosts `.49`/`.50` (winrm-agent), `.106`/`.119`
(winrm-agent), `.161`/`.194` (wmi) all managed automatically, no manual recovery. `.106`/
`.119` were previously *false* terminal failures caused by storm-induced WinRM negotiation
instability; no host-side change was made — they collect automatically under the governed
pipeline, proving the reliability model fixed the issue.

### Future enhancement (not built — gated on need)
WinRM message encryption already exists in the client, so there is no encryption gap. If a
host is ever found to *genuinely* refuse all transports after the full automatic budget is
exhausted (confirmed by stable host probes), the operator-facing options are: enable WinRM
HTTPS/5986, set `LocalAccountTokenFilterPolicy` for remote local-admin WMI, or supply a
policy-allowed credential. A dedicated post-exhaustion "host-policy-blocked" diagnosis
(only after retry + self-heal are spent, confirmed by probes) could be added if such hosts
prove common — but per the #6 evidence, the load-induced transient case must not be
classified terminal up front.
