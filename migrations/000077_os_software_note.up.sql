-- Per-device installed-software collection status/reason. Empty string when
-- software was collected normally (or the path doesn't set it); otherwise an
-- honest reason the Software section can show instead of a silent empty list:
-- which method succeeded (e.g. "collected via remote_registry") or the exact
-- blocker (remote_registry_disabled / access_denied / service_start_failed /
-- rpc_unreachable / registry_access_denied / winrm_registry_unsupported / ...).
ALTER TABLE os_inventory ADD COLUMN IF NOT EXISTS software_note text NOT NULL DEFAULT '';
