# HIMS — Discovery → Collection Flow Audit

**Scope:** the full operator path — *add IP / range / subnet → scan → classify → credential → enrol → collect → complete inventory*.
**Target system:** production `150.0.0.120` (Ubuntu 24.04, systemd + Postgres container).
**Date:** 2026-08-06 · **Branch:** `fix/collection-failed-wmi-remediation`
**Method:** code trace of each stage, cross-checked against the live production database and live probes of `172.21.96.0/24` / `172.21.60.0/24`. Findings below are labelled **VERIFIED** (reproduced against the live system) or **BY INSPECTION** (read from code, not exercised).

---

## 1. Executive summary

The pipeline is structurally sound: every stage gates on real evidence, credentials bind only on successful authentication, and secrets are never returned by the API. The flow reached a healthy end state during this audit — **129 of 139 devices fully managed**, with the remaining 10 explained by genuine credential gaps, not defects.

Three defects were found that affect correctness or operator trust. One (**F1**) was introduced by the liveness-sweep work earlier in this engagement and is fixed here. Two (**F2**, **F3**) are open and carry concrete fix plans. Nothing found is a data-loss or security defect.

**Production readiness: GO, with F2 and F3 scheduled.** Neither blocks operation; both cause an operator to be misinformed in a specific situation, which is the category of bug this system is explicitly designed to avoid.

### Current production state (live, end of audit)

| Management state | Count |
|---|---|
| managed | **129** |
| credential_failed | 7 |
| needs_credential | 2 |
| not_authorized | 1 |
| **Total** | **139** |

Relay agent `CHR` — `CHV-MISMGR`, `windows/amd64`, v1.2.21, **online**, capabilities `winrm,wmi`.
Sites: `Coral Sea Group` (group) → `CAC`, `CHR` (hotels). Subnets mapped: `172.21.96.0/24` → CHR, `172.21.60.0/24` → CHR.

---

## 2. The flow, stage by stage

### Stage 0 — Scope input (single IP / range / CIDR / site subnets)

`internal/discovery/targets.go` · `ParseTargets`, `expandRange`, `FilterExcluded`

Accepts a mixed, delimiter-tolerant spec: `10.0.0.5`, `172.21.96.0/24`, `172.21.96.1-172.21.96.254`, and the last-octet shorthand `172.21.96.1-254`. Exclusions accept the same grammar.

**VERIFIED GOOD**
- Deduplicates while preserving order.
- **Errors rather than truncating** when the scope exceeds `maxHosts` — the operator re-scopes deliberately instead of silently scanning a fraction. This is the right call and rare in tools of this class.
- Range end < start is rejected; ranges are IPv4-only with an explicit error.
- Wrap-around at `255.255.255.255` is guarded.
- `ExpandCIDR` skips network and broadcast for IPv4 /30-or-wider, so those addresses are never scanned as hosts — which is what makes them usable as sweep controls (Stage 1).

**No defects found in this stage.**

### Stage 1 — Liveness sweep (which addresses are real)

`internal/discovery/sweep.go` · `LivenessSweep`, `ControlsFor`, `PortsForHost`

Runs **before** any deep probe, credential attempt or enrolment. TCP-probes every address, plus each range's network and broadcast addresses as **negative controls**. A port answering on a control address proves the reply comes from something on the path, not the target.

**VERIFIED** on `172.21.96.0/24`: `254` probed → **61 alive**, 193 suppressed, `tcp/5060` flagged untrusted with `proved_by_control=true`. This matches the operator's independent IP-scanner count exactly.

**Why this exists:** a SIP ALG on the path answers TCP/5060 for every address in the voice subnets. Under the previous rule (`alive = any open port`), a /24 holding 61 real devices enrolled all 254 — 193 of them with `[5060]` as their only evidence. Confirmed from two independent vantage points, including on addresses that cannot host anything.

**Design note (intentional, keep):** addresses whose only evidence is an untrusted port are reported as *suppressed*, never silently dropped. On such a network a genuine phone exposing only 5060 is indistinguishable from an empty address by TCP alone; hiding that ambiguity would be dishonest. See **F2** for the case where this becomes wrong.

### Stage 2 — Port scan / fingerprint

`internal/discovery/pipeline.go` · `scanPorts`, `PortsForHost`

One definition of a host's port set (standard management ports + operator web ports + the Hikvision `8000+octet` convention), shared by the sweep and the deep pass, so the sweep cannot decide a host is dead on narrower evidence than the pipeline would have used.

**VERIFIED** — ports for a host are probed concurrently (16 in flight). Measured on the real /24 at production settings (`scan_concurrency=16`, `tcp_timeout_ms=800`): **~6 min → 29 s**, same result. Sequential probing previously cost `ports × timeout` ≈ 23 s per unresponsive address.

The deep pass reuses the sweep's findings (`KnownOpenPorts`) instead of re-scanning. The known-device retry deliberately clears that reuse — that pass exists to probe *again*, slower, with the device's last-known ports.

### Stage 3 — Credential resolution and authentication

`internal/credresolver`, `internal/api/credtest.go`, `internal/api/credential_assign.go`

**VERIFIED GOOD — the core invariant holds.** A credential is bound to a device **only** after that credential actually authenticated. The bulk-assign endpoint (`POST /devices/credential-assign`) tests every selected device first and writes the binding only where authentication succeeded; failures are returned per device with the protocol and reason and change nothing.

- Every attempt — success and failure — is persisted to credential-test history with a non-secret reason.
- Open ports gate which protocols are attempted (`portAllowsProto`), so a host with no 5985 is not WinRM-probed.
- SNMP is always offered as a candidate because it is UDP/161 and invisible to a TCP port scan. Correct.
- **Secrets never leave the server.** The credential DTO carries `id, name, kind, weak, needs_secret_reentry, created_at, usage_count` — no secret field exists. Verified against the live API and the config-backup export.
- Credential kinds are validated against an allowlist mirroring the DB CHECK constraint, with a test that parses the constraint out of the newest migration and diffs it against the Go list, so the two cannot drift.

**Caveat (not a defect):** the config backup **does** include `users.password_hash` (bcrypt). Restoring users requires it, but the file should be treated as sensitive rather than freely shareable.

### Stage 4 — Classification

`internal/classify`, `internal/fingerprint`, `pipeline.go` Step 5/5b

Driver match first, then evidence-based candidate classification from **safe unauthenticated evidence only** (open ports, SNMP sysDescr, HTTP server/title, SSH banner). Never counts as managed access — classification and management are separate axes throughout. Manual classification locks are respected.

**BY INSPECTION — no defects found.** The explainable-classification record (winners, rejected candidates with reasons) is persisted into `discovery_results.probe_data`, which is a genuine strength for diagnosis.

### Stage 5 — Enrolment and site resolution

`internal/apply`, `subnetLocationResolver`

Only alive hosts are enrolled. Site is resolved from subnet→site mappings; an existing device with a null location gets it filled on re-scan (COALESCE, non-destructive).

**This stage caused the most operator-visible pain in production.** See **F3**.

### Stage 6 — Deep collection (direct, then relay agent)

`internal/api/os_inventory.go`, `agent_routing.go`

Order: direct WinRM from the server → on specific failures, route to the site relay agent for **WMI/DCOM**. WMI is Windows-only; a Linux agent is honestly a WinRM-only collector and now advertises itself as such, with the server refusing to queue a protocol the chosen agent does not support.

**VERIFIED** — `172.21.60.105` collected over direct WinRM: 2 disks, 1 NIC, 50 processes, 272 services, 51 software entries, roles. This is the "complete data" end state.

### Stage 7 — Completion, self-heal, monitoring

`collection_selfheal.go`, monitoring loop, Data Quality

Deep collection is asynchronous; the job phase reports `collecting` while it drains and `self_healing` while terminal-transient failures still await automatic retry — never a premature `complete`. **VERIFIED** during this audit: the queue drained on its own from 37 pending to 0 without intervention.

---

## 3. Findings

| ID | Severity | Status | Title |
|---|---|---|---|
| F1 | **High** | **FIXED** `5e71532` | `host_count` reported the alive subset, not the scanned scope |
| F2 | **Medium** | **FIXED** `a2c33bd` | Explicitly targeted IP silently not enrolled when only an ALG port answers |
| F3 | **Medium** | **FIXED** `a2c33bd` | "No Relay Agent for this site" misreports a missing *site* as a missing *agent* |
| F4 | Low | **Open — needs an operator decision** | Agent↔site matching is exact, not hierarchical |
| F5 | Low | **FIXED** `a2c33bd` | `credtest` test is environment-coupled and fails on any host serving :80/:443 |
| F6 | Info | Accepted | Historical job rows retain pre-fix counters |

**Status after remediation:** F1, F2, F3 and F5 are fixed, tested and deployed. F4 is the
only open item and needs a policy decision (below). The full `internal/...` test suite is
green — including `credtest`, which was red before this engagement began.

---

### F1 — `host_count` reported the alive subset, not the scanned scope · **HIGH · FIXED**

**Evidence (live production, before fix):**

```
job      | scope_cidr     | host_count | scanned_count | found_count | lv_total | lv_alive
278383a4 | 172.21.60.0/24 |         76 |            76 |          78 |      254 |       76
5fc96220 | 172.21.60.0/24 |         77 |            77 |          77 |      254 |       77
70279ba4 | (site subnets) |         61 |            61 |          61 |      254 |       61
```

`host_count` equalled `lv_alive` in **every** job while the sweep had actually probed 254 addresses. Worse, job `278383a4` shows **`found_count` (78) greater than `host_count` (76)** — a logical impossibility to an operator reading the jobs list.

**Cause:** `runScanJob` reassigns `hosts = sweep.Alive` to narrow the deep pass, and the completion write at the end used `len(hosts)` for both `HostCount` and `ScannedCount`. Introduced by the liveness-sweep change earlier in this engagement.

**Impact:** the jobs list and the "targets probed" KPI understated the scanned scope; a /24 scan read as "76 hosts". Progress still reached 100% so it *looked* correct, which is what let it pass unnoticed.

**Fix applied:** capture `scopeHosts := len(hosts)` before narrowing and use it for `HostCount`/`ScannedCount`. `found_count` is unchanged (it counts persisted non-missed result rows) and can no longer exceed the scope.

---

### F2 — Explicitly targeted IP silently not enrolled · **MEDIUM · OPEN**

**Evidence (live):** scanning the single address `172.21.96.3`:

```
target     : 172.21.96.3
open ports : [5060]
ALIVE      : 0
SUPPRESSED : 1  (172.21.96.3)
untrusted  : tcp/5060 (proved by control address)
```

**Cause:** the sweep applies promiscuous-port suppression identically whether the operator swept a /24 or typed one address. For a CIDR sweep this is correct — it is what stops 193 phantoms. For an explicitly typed target it is wrong: the operator has *asserted* the device exists, and the system answers with an empty result.

**Impact:** an operator adding a known SIP phone by IP gets nothing, with the explanation only visible in the job's liveness panel. This is a trust problem: the tool appears to ignore a direct instruction.

**Fix plan:**
1. Have `ParseTargets` return the set of **explicitly named** addresses (single IPs typed by the operator), distinct from CIDR/range-expanded ones.
2. Pass that set into `SweepConfig` as `AssertedByOperator`.
3. In classification, an asserted address whose only evidence is an untrusted port is classified **alive** rather than suppressed, and enrolled — but flagged with a Data Quality issue: *"liveness unproven: only TCP/5060 answered, and that port is answered by a middlebox on this subnet."*
4. Add tests: asserted-single survives; CIDR-expanded address with identical evidence is still suppressed.

**Effort:** ~half a day. **Risk:** low — additive, does not change CIDR sweep behaviour.

---

### F3 — "No Relay Agent for this site" misreports a missing site · **MEDIUM · OPEN**

**Evidence (live production):** the CHR relay agent was installed, online, healthy (`winrm,wmi`) and physically on `172.21.60.20`, yet every Windows device on `172.21.60.0/24` reported *"no Relay Agent for this site"*. Root cause: only `172.21.96.0/24` was mapped to a site, so all **78** devices on `172.21.60.0/24` had `location_id = NULL`.

`routeViaSiteAgent` opens with:

```go
if d.LocationID == nil { reason = "agent_missing"; ... }
```

so a site-less device is refused **before any agent is considered** — and the message names the agent, which is the wrong place to look. The operator (correctly following the message) installed an agent, and nothing changed.

**Impact:** high diagnostic cost. This consumed the majority of one troubleshooting cycle, including installing an agent that was never the problem.

**Remediation already applied to production:** mapped `172.21.60.0/24` → CHR and assigned the 78 devices. Result: managed rose 85 → 129 with no further intervention.

**Fix plan (code):**
1. Give the site-less case its **own** reason code — `device_no_site` — distinct from `agent_missing`, with the detail *"this device is not assigned to a site; relay routing is per-site. Map its subnet under Locations → Subnets, or set the site in Edit Device."*
2. Surface a Data Quality issue: *"N devices have no site — they cannot use a relay agent"*, with the offending subnets grouped, and a one-click "map subnet → site" action.
3. Add a startup/periodic check: any subnet holding devices but absent from `subnets` is reported as a coverage gap.
4. Tests asserting `device_no_site` for a null location and `agent_missing` only when the site genuinely has no agent.

**Effort:** ~half a day. **Risk:** very low — messaging and reporting only.

---

### F4 — Agent↔site matching is exact, not hierarchical · **LOW · OPEN**

`assignedSiteAgent` compares `*a.LocationID != loc` exactly. An agent assigned to the parent group `Coral Sea Group` does **not** serve devices in the child hotels `CAC` / `CHR`, even though the hierarchy implies it should.

**Impact:** not currently biting (the CHR agent is assigned directly to CHR), but it is a live trap: assigning an agent at group level looks correct and silently serves nothing.

**Fix plan:** walk the location ancestry (the tree is already loaded for site-scope checks in `locationParents`) so an agent at an ancestor serves descendants, preferring the most specific match. Alternatively, if exact matching is the deliberate policy, state it in the Agents UI. **Decide the policy before implementing.**

---

### F5 — `credtest` test is environment-coupled · **LOW · OPEN · PRE-EXISTING**

`TestHTTP_NonStandardPort` fails on the development machine:

```
no WebPorts: got "web_reachable", want unreachable
with WebPort 57620: got "web_reachable" (HTTP 200 OK — page served WITHOUT credentials)
```

**This is not a product defect.** The test spins up its own fixture but `testHTTP` probes `https://127.0.0.1/` and `http://127.0.0.1/` first, and this machine genuinely serves them (`127.0.0.1:80` → 301, `127.0.0.1:443` → 200, both confirmed open). The product's ordering — standard ports first, then configured web ports — is correct.

**Confirmed pre-existing:** reproduced at commit `76fd281`, the branch head before any work in this engagement, in a clean worktree. Not a regression.

**Fix plan:** make the standard-port candidate list injectable so the test exercises only its fixture, or bind the fixture to an address where 80/443 are guaranteed closed. Until then CI on any host running a web server will show a false red.

---

### F6 — Historical job counters · **INFO · ACCEPTED**

The pre-fix scan job still records `found_count = 254` for `172.21.96.0/24`. That is the honest historical record of what the buggy scan did, not a live count. Live inventory is correct (61 real devices on that subnet). No action; do not retro-edit history.

---

## 4. Recommendations

**Correctness**
1. Implement **F2** and **F3** before the next onboarding push — both are operator-trust issues in the exact workflow being rolled out.
2. Decide the **F4** policy (hierarchical vs exact agent↔site) and either implement or document it.
3. Fix **F5** so CI red means something.

**Operability**
4. **Map every subnet to a site before scanning it.** This is the single highest-value operational habit — an unmapped subnet silently produces site-less devices that can never use a relay agent (F3).
5. Keep an eye on disk on `150.0.0.120`. Root was at 100% at deploy time and is a shared box (the unrelated `nims-prod` stack, nginx, ollama). Check `df -h /` before any deploy.
6. Back up `/root/hims-deploy-secrets.txt` off-box. Losing `HIMS_ENCRYPTION_KEY` makes every stored device credential permanently undecryptable.
7. Rotate the HIMS admin password — it has been shared in a support conversation.

**Coverage gaps that are gated, not broken**
8. The 10 unmanaged devices are credential problems, not pipeline problems: 7 `credential_failed`, 2 `needs_credential`, 1 `not_authorized`, all on `172.21.96.x`. Use **Device Access** (multi-select → test & assign) to work through them; it binds only on successful authentication so the result is trustworthy.
9. WMI/DCOM requires a **Windows** agent host. A Linux agent — including one on the HIMS server — is WinRM-only, and the server now refuses to queue WMI to it rather than queueing jobs that can never succeed.

---

## 5. Fix plan

| # | Item | Sev | Risk | Status |
|---|---|---|---|---|
| 1 | F1 — scope vs alive in `host_count`/`scanned_count` | High | low | **Fixed** `5e71532` |
| 2 | F3 — `device_no_site` reason + Data Quality subnet-coverage gap | Med | very low | **Fixed** `a2c33bd` |
| 3 | F2 — operator-asserted targets survive suppression (flagged) | Med | low | **Fixed** `a2c33bd` |
| 4 | F5 — make `testHTTP` standard ports injectable | Low | none | **Fixed** `a2c33bd` |
| 5 | F4 — decide + implement agent↔site hierarchy policy | Low | low | **Open — needs decision** |

### Remaining open item — F4, agent↔site hierarchy

`assignedSiteAgent` matches `*a.LocationID != loc` **exactly**. An agent assigned to the
parent group `Coral Sea Group` therefore serves **nothing** in the child hotels `CAC` / `CHR`.
It is not biting today (the CHR agent is assigned directly to CHR), but assigning an agent at
group level looks correct and silently serves no devices.

Two defensible policies — this is an operator decision, not a technical one:

- **Inherit down the tree** (recommended if you intend group-level agents): walk the location
  ancestry — already loaded by `locationParents` for site-scope checks — so an agent at an
  ancestor serves descendants, preferring the most specific match. ~0.5 d, low risk.
- **Keep exact matching** as deliberate policy: then say so in the Agents UI ("an agent serves
  only the exact site it is assigned to"), so nobody assigns one at group level expecting
  inheritance. ~1 h.

### What was changed in remediation

- **F3:** the site gate returns `device_no_site` — its own reason, threaded through scan
  events, next-action text and the UI badge — whose wording points at mapping the subnet or
  setting the site, and never suggests installing an agent. Data Quality now reports the root
  cause (`unmapped_subnet`: subnets holding devices but mapped to no site) alongside the
  symptom, and `missing_location` states the relay-collection consequence explicitly.
- **F2:** `ParseTargets` distinguishes addresses named individually from CIDR/range
  expansions. An asserted address is admitted on weak evidence and reported as
  liveness-unproven (scan event + `liveness.liveness_unproven` count) rather than dropped.
  CIDR sweeps are untouched, so phantom suppression still holds — both directions are tested.
- **F5:** the standard-port candidates are overridable so the test isolates itself from a
  machine that serves :80/:443. Product ordering is unchanged.

**Definition of done for each:** compiles, `go vet` clean, unit test covering the specific failure mode, deployed to `150.0.0.120`, and verified against the live system — the standard applied throughout this engagement.

---

## 6. What was verified live during this audit

- Target parsing across single IP, CIDR, range and shorthand forms — by inspection; caps and error paths read.
- Liveness sweep against the real `172.21.96.0/24`: 254 probed, 61 alive, `tcp/5060` untrusted via control address.
- Sweep timing at production settings: 29 s for a /24 (was ~6 min).
- Single-target suppression behaviour on `172.21.96.3` (**F2**).
- Job counter integrity against the production database (**F1**).
- Credential kind validation: `winrm`/`wmi`/nonsense → 400 with actionable text; `windows` → 201 and persisted.
- Bulk credential assign: tests before binding, binds only on success, records history.
- Direct WinRM collection on `172.21.60.105` producing complete inventory.
- Relay agent registration, capabilities, online state, and queue drain to zero.
- Full `go build`, `go vet`, and test suite — green except the pre-existing, environment-coupled `credtest` case (**F5**).
