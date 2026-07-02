-- Inventory-only devices: real, discovered gear that HIMS should keep as a RECORD
-- and MONITOR for reachability (online/offline) but that is intentionally NOT
-- applicable for authenticated access/collection — e.g. third-party-owned kit, a
-- device the operator has no credentials for by policy, or an appliance that must
-- not be probed for management. It is the opposite of is_virtual: an inventory-only
-- device is genuinely on the network and IS monitored; it simply must never be
-- flagged "needs credential / credential_failed / collection_failed" or counted as
-- a management gap, because access was deliberately opted out of.
--
-- is_inventory_only is an indexed discriminator (same audit-discriminator pattern
-- as is_virtual): the management-state derivation short-circuits to a distinct
-- "inventory_only" state, and the trust-audit / data-quality credential-gap logic
-- excludes it. Reachability monitoring is untouched, so offline is still detected.
ALTER TABLE devices ADD COLUMN IF NOT EXISTS is_inventory_only boolean NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS idx_devices_is_inventory_only ON devices (is_inventory_only) WHERE is_inventory_only;
COMMENT ON COLUMN devices.is_inventory_only IS 'Operator-marked record-and-monitor-only device: monitored for reachability but excluded from access/credential expectations.';
