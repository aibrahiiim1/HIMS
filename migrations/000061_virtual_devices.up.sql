-- Virtual devices: operator-entered placeholders for gear that cannot be
-- integrated or probed (no reachable SNMP/SSH/API, or intentionally so) but must
-- still appear in inventory so the network picture is complete. A virtual device
-- reuses every inventory table (interfaces, vlans, mac_addresses, neighbors,
-- port_vlans) with collection_source='manual', so the existing device-detail
-- pages, topology and global search render it with no extra plumbing.
--
-- is_virtual is an indexed discriminator (audit-discriminator pattern): it lets
-- dashboards report "N devices, M virtual" cheaply and lets monitoring/collection
-- skip these devices (they can't be probed).
ALTER TABLE devices ADD COLUMN IF NOT EXISTS is_virtual boolean NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS idx_devices_is_virtual ON devices (is_virtual) WHERE is_virtual;
COMMENT ON COLUMN devices.is_virtual IS 'Operator-entered placeholder device (not auto-discovered/probed); its inventory data is manual.';
