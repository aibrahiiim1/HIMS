-- Widen collection_source to allow operator-entered ('manual') rows in the
-- inventory tables a virtual device populates (interfaces, VLANs, port-VLANs,
-- learned MACs, neighbors, ARP). The live collectors only ever write
-- snmp/cli/api, so adding 'manual' is backward-compatible, and the distinct
-- source keeps manual rows out of every collector's source-scoped stale-prune
-- (DeleteStale* is always scoped to the collecting source).
ALTER TABLE interfaces    DROP CONSTRAINT IF EXISTS interfaces_collection_source_check;
ALTER TABLE interfaces    ADD  CONSTRAINT interfaces_collection_source_check    CHECK (collection_source IN ('snmp','cli','api','manual'));
ALTER TABLE vlans         DROP CONSTRAINT IF EXISTS vlans_collection_source_check;
ALTER TABLE vlans         ADD  CONSTRAINT vlans_collection_source_check         CHECK (collection_source IN ('snmp','cli','api','manual'));
ALTER TABLE port_vlans    DROP CONSTRAINT IF EXISTS port_vlans_collection_source_check;
ALTER TABLE port_vlans    ADD  CONSTRAINT port_vlans_collection_source_check    CHECK (collection_source IN ('snmp','cli','api','manual'));
ALTER TABLE mac_addresses DROP CONSTRAINT IF EXISTS mac_addresses_collection_source_check;
ALTER TABLE mac_addresses ADD  CONSTRAINT mac_addresses_collection_source_check CHECK (collection_source IN ('snmp','cli','api','manual'));
ALTER TABLE neighbors     DROP CONSTRAINT IF EXISTS neighbors_collection_source_check;
ALTER TABLE neighbors     ADD  CONSTRAINT neighbors_collection_source_check     CHECK (collection_source IN ('snmp','cli','api','manual'));
ALTER TABLE arp_entries   DROP CONSTRAINT IF EXISTS arp_entries_collection_source_check;
ALTER TABLE arp_entries   ADD  CONSTRAINT arp_entries_collection_source_check   CHECK (collection_source IN ('snmp','cli','api','manual'));

-- A manually-entered neighbor isn't discovered via LLDP/CDP, so allow 'manual'
-- as its protocol too (keeps the operator's neighbor rows honest).
ALTER TABLE neighbors     DROP CONSTRAINT IF EXISTS neighbors_protocol_check;
ALTER TABLE neighbors     ADD  CONSTRAINT neighbors_protocol_check     CHECK (protocol IN ('lldp','cdp','manual'));
