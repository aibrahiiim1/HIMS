# HIMS Enterprise Program — Retrospective & Pattern Vocabulary

This program took HIMS from a working NMS to a comprehensive enterprise system
across the 31-item roadmap (see `PROGRESS.md`). The same handful of design
patterns recurred in feature after feature. Naming them here turns incidental
decisions into reusable vocabulary — reference them by name in future specs and
reviews instead of re-deriving them.

---

## 1. Derived status, never stored

When a value is a pure function of other data + "now", **compute it at read
time** rather than persisting it, so it can never go stale.

- License/support expiry → `operations.ComputeLicenseStatus` (#6-era).
- Work-order **SLA** deadline + standing from priority policy (#19).
- **Asset** warranty / EOL status — reuses `ComputeLicenseStatus` (#18).
- Config **drift** `changed` flag = sha256 of normalised config (#11).
- Device **reachability** badge is frontend-derived, backend status stays honest
  (per memory: device-status vocabulary).

Payoff: changing an input (priority, a date) instantly re-targets the output;
no migration, no reconciler, no drift bug.

## 2. Pure core + thin handler

Put the real logic in a pure, dependency-free package (`internal/...`) that's
unit-tested against crafted inputs; keep the HTTP handler a thin wrapper that
does I/O and calls the core.

- `internal/config` (cmd-map / hash / diff), `internal/ssh` (KEX assembly),
  `internal/fingerprint` (matcher + library), `internal/reports` (xlsx/csv +
  schedule due-calc), `internal/netflow` (v5 decode + aggregation),
  `internal/backup` (archive build/validate), `internal/migrate` (version
  parse / pending), `internal/operations` (SLA).

Payoff: the hard part is tested without a DB or network; protocol/format code
is verified against real wire bytes (NetFlow v5, xlsx round-trip).

## 3. Idempotent apply (dedup before write)

Any "apply this to many targets" action plans first, dedups against what
already exists, and only writes the delta — re-running is a no-op.

- Device-template apply → `monitoring_checks` (dedup by port/OID) + `alert_rules`
  (dedup by name) (#8).
- Fingerprint / permission **library seeds** — dedup by natural key (#9, #23).
- `hims-migrate up` — apply only pending versions; `baseline` adopts an existing
  DB (#26).

Payoff: safe to re-run, safe to ship; verification asserts "second run created
nothing".

## 4. Honest operational gating

When a feature's live data needs a prerequisite the environment doesn't have
(a bound credential, a configured exporter, SMTP, a collection run), ship the
whole verifiable pipeline and return a **clear, actionable** gate — never fake
data, never hide the gap.

- Config backup → `bound credential is "snmp_v2c", need an 'ssh' credential` (#10).
- Scheduled report email → `generated; no channel — not sent` (#21).
- NetFlow → collector-status banner with the listen address to point exporters (#12).
- Full pg_dump backup → operator runbook + DR checklist item (#25).
- RBAC enforcement → documented as awaiting an auth layer; data model shipped (#23).

Payoff: the operator knows exactly what to do; the code is real and tested up to
the boundary; PROGRESS.md records the gate + trigger.

## 5. Extend an existing encoding before adding schema

Before a new column/table, check whether an existing structure can carry the new
semantic.

- Device-template monitor/alert profile lives in the existing `monitoring_rules`
  JSONB — no migration (#8).
- Reuse `ComputeLicenseStatus` for SLA + warranty + EOL rather than three status
  engines (#18, #19).
- Audit CSV export reuses the `internal/reports` encoder (#24).

## 6. Secret-safe by construction

Secrets (credential blobs, channel targets, config text) are AES-256-GCM
encrypted at rest and **never** leave the server except through an explicit,
key-gated path; metadata DTOs strip blobs.

- Config-backup content: key-gated content/diff only (#10).
- Backup config-snapshot exports credentials as **metadata only**, never the
  blob (#25).
- One-way key fingerprints surfaced for DR readiness, never the key (#25).

## 7. Verify live, against real data, before commit

Every feature was exercised against the live DB / a real protocol packet, and
test artifacts were cleaned up. Several "real findings" surfaced this way:

- 448/601 devices now site-assigned (unblocked #22); 153 still unassigned
  (surfaced as a bucket, not hidden).
- HIMS persists only a truncated sysDescr + vendor (shaped #9's matcher).
- The whole DB schema was hand-migrated with no ledger (motivated #26).

---

## Patterns that crossed into vocabulary (applied ≥4×)

**Derived status**, **pure core + thin handler**, **idempotent apply**, and
**honest operational gating** each recurred across four or more independent
features. They are now project vocabulary: a new feature should state which of
these it uses, and a reviewer should expect them.
