# ADR 0001 — Generic CMDB core + vendor driver engine

Status: accepted · 2026-06-03

## Context
The prior system (NIMS) drifted toward vendor-aware code paths and a device
model that didn't cleanly separate "what every device is" from "what this
vendor exposes." The rebuild must scale to ~20 device categories and ~20
vendors without the schema or core logic knowing about any specific vendor.

## Decision
1. **Generic core tables only.** `devices`, `device_roles`, `interfaces`,
   `vlans`, `mac_addresses`, `neighbors`, `metrics`, etc. No
   `CiscoSwitch`/`HuaweiSwitch` tables. Vendor-specific data is stored in
   `device_facts` (normalized key/value/JSON) and raw snapshots.
2. **A driver engine.** Each vendor/platform is a `Driver` implementing:
   `Fingerprint` (identify + confidence), `Authenticate`, `Collect`
   (normalized facts + raw snapshot), `Template` (which detail template).
   Drivers register into a central registry; core code resolves a driver by
   fingerprint and never branches on vendor names.
3. **Templates describe presentation, not storage.** A category →
   template mapping defines which sections a detail page renders; the same
   generic tables back every vendor of that category.
4. **Multi-role devices.** Roles are rows in `device_roles`, not a single
   category enum, so a box can be Hyper-V Host + DC + DNS at once.

## Consequences
- Adding a vendor = a new driver package + registration; zero schema change.
- Adding a category = a new template + (if needed) generic sub-tables shared
  across all vendors of that category.
- The collector, classifier, and UI stay vendor-agnostic; only drivers hold
  vendor knowledge.
- Migration cost is front-loaded into getting the generic core right
  (Phase 0); the payoff is every later vendor/category is additive.
