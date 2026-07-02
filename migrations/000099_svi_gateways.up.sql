-- VLAN gateway (SVI) modelling. A layer-3 switch/router configures IP addresses on
-- its VLAN interfaces (SVIs) — these are the VLAN gateway IPs (e.g. 172.21.210.250
-- on the core). HIMS previously only saw such an IP when it answered a ping and
-- created a phantom standalone device for it. Now we collect the switch's own
-- ipAddrTable and bind each SVI IP to its VLAN, so a gateway IP is attributed to
-- the switch that owns it instead of scattered as a fake device.

-- Per-VLAN SVI gateway IP (null for pure L2 VLANs / switches with no L3).
ALTER TABLE vlans ADD COLUMN IF NOT EXISTS gateway_ip inet;
CREATE INDEX IF NOT EXISTS idx_vlans_gateway_ip ON vlans (gateway_ip) WHERE gateway_ip IS NOT NULL;
COMMENT ON COLUMN vlans.gateway_ip IS 'The VLAN''s L3 SVI gateway IP on this switch (from ipAddrTable); null for pure L2.';

-- Every IP configured ON a device (from ipAddrTable): SVI/VLAN gateways, loopbacks,
-- the management IP. if_index binds the IP to the owning interface; net_mask gives
-- the subnet. This is the evidence used to attribute a discovered gateway IP to the
-- switch that owns it (a phantom SVI device is linked here, not treated as its own).
CREATE TABLE IF NOT EXISTS ip_interfaces (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id         uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    if_index          integer NOT NULL,
    ip_address        inet NOT NULL,
    net_mask          inet,
    collection_source text NOT NULL DEFAULT 'snmp',
    last_seen_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (device_id, ip_address)
);
CREATE INDEX IF NOT EXISTS idx_ip_interfaces_device ON ip_interfaces (device_id);
CREATE INDEX IF NOT EXISTS idx_ip_interfaces_ip ON ip_interfaces (ip_address);
