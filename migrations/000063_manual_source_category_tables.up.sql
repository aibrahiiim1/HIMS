-- Extend collection_source to allow operator-entered ('manual') rows in the
-- remaining per-category inventory tables a virtual device populates: server
-- storage, firewall state/VPN/HA/licenses, and the OS-inventory tables. Same
-- rationale as 000062 — collectors only ever write their own source, so adding
-- 'manual' is backward-compatible and stays out of every collector's
-- source-scoped stale-prune.
ALTER TABLE server_storage       DROP CONSTRAINT IF EXISTS server_storage_collection_source_check;
ALTER TABLE server_storage       ADD  CONSTRAINT server_storage_collection_source_check       CHECK (collection_source IN ('snmp','cli','api','manual'));
ALTER TABLE firewall_status      DROP CONSTRAINT IF EXISTS firewall_status_collection_source_check;
ALTER TABLE firewall_status      ADD  CONSTRAINT firewall_status_collection_source_check      CHECK (collection_source IN ('snmp','cli','api','manual'));
ALTER TABLE firewall_vpn_tunnels DROP CONSTRAINT IF EXISTS firewall_vpn_tunnels_collection_source_check;
ALTER TABLE firewall_vpn_tunnels ADD  CONSTRAINT firewall_vpn_tunnels_collection_source_check CHECK (collection_source IN ('snmp','cli','api','manual'));
ALTER TABLE firewall_ha_members  DROP CONSTRAINT IF EXISTS firewall_ha_members_collection_source_check;
ALTER TABLE firewall_ha_members  ADD  CONSTRAINT firewall_ha_members_collection_source_check  CHECK (collection_source IN ('snmp','cli','api','manual'));
ALTER TABLE firewall_licenses    DROP CONSTRAINT IF EXISTS firewall_licenses_collection_source_check;
ALTER TABLE firewall_licenses    ADD  CONSTRAINT firewall_licenses_collection_source_check    CHECK (collection_source IN ('snmp','cli','api','manual'));

-- OS-inventory tables use the winrm/ssh/snmp vocabulary; add 'manual'.
ALTER TABLE os_disks    DROP CONSTRAINT IF EXISTS os_disks_collection_source_check;
ALTER TABLE os_disks    ADD  CONSTRAINT os_disks_collection_source_check    CHECK (collection_source IN ('winrm','ssh','snmp','manual'));
ALTER TABLE os_nics     DROP CONSTRAINT IF EXISTS os_nics_collection_source_check;
ALTER TABLE os_nics     ADD  CONSTRAINT os_nics_collection_source_check     CHECK (collection_source IN ('winrm','ssh','snmp','manual'));
ALTER TABLE os_services DROP CONSTRAINT IF EXISTS os_services_collection_source_check;
ALTER TABLE os_services ADD  CONSTRAINT os_services_collection_source_check CHECK (collection_source IN ('winrm','ssh','snmp','manual'));
ALTER TABLE os_software DROP CONSTRAINT IF EXISTS os_software_collection_source_check;
ALTER TABLE os_software ADD  CONSTRAINT os_software_collection_source_check CHECK (collection_source IN ('winrm','ssh','snmp','manual'));
ALTER TABLE os_roles    DROP CONSTRAINT IF EXISTS os_roles_collection_source_check;
ALTER TABLE os_roles    ADD  CONSTRAINT os_roles_collection_source_check    CHECK (collection_source IN ('winrm','ssh','snmp','manual'));
