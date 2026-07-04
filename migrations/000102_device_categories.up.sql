-- Operator-manageable device categories. Built-in categories (the ones the
-- classifier produces and the detail pages route on) are seeded with builtin=true
-- and can be hidden but NEVER deleted. Operators may add their own custom
-- categories (builtin=false) for MANUAL classification — fully CRUD-able.
--
-- The old devices_category_check CHECK constraint hard-coded the valid set, which
-- made custom categories impossible to store. Validation moves to the application
-- layer (api.isValidCategory: built-in list ∪ rows in this table), so custom
-- categories become storable while the built-ins stay always-valid.

CREATE TABLE device_categories (
    value       TEXT PRIMARY KEY,                 -- slug written to devices.category
    label       TEXT NOT NULL,                    -- display name
    icon        TEXT NOT NULL DEFAULT '',         -- optional lucide icon name
    builtin     BOOLEAN NOT NULL DEFAULT false,   -- seeded platform category (protected from delete)
    enabled     BOOLEAN NOT NULL DEFAULT true,    -- shown in the Edit / classify pickers
    sort_order  INTEGER NOT NULL DEFAULT 100,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed the built-in catalog (mirrors internal/api categoryList + domain.Cat*).
INSERT INTO device_categories (value, label, icon, builtin, enabled, sort_order) VALUES
    ('unknown','Unknown','CircleHelp',true,true,10),
    ('network_device_unclassified','Network Device (unclassified)','Network',true,true,15),
    ('switch','Switch','Network',true,true,20),
    ('router','Router','Router',true,true,21),
    ('firewall','Firewall','Flame',true,true,22),
    ('access_point','Access Point','Wifi',true,true,23),
    ('wireless_controller','Wireless Controller','Wifi',true,true,24),
    ('isp_router','ISP Router','Router',true,true,25),
    ('load_balancer','Load Balancer','Network',true,true,26),
    ('server','Server','Server',true,true,30),
    ('virtual_host','Virtual Host','Server',true,true,31),
    ('virtual_machine','Virtual Machine','Server',true,true,32),
    ('bmc','BMC / iLO / iDRAC','Cpu',true,true,33),
    ('storage','NAS Storage','Database',true,true,34),
    ('nvr','NVR','Video',true,true,40),
    ('dvr','DVR','Video',true,true,41),
    ('camera','Camera','Camera',true,true,42),
    ('printer','Printer','Printer',true,true,50),
    ('ip_phone','IP Phone','Phone',true,true,51),
    ('pbx','PBX','Phone',true,true,52),
    ('voice_gateway','Voice Gateway','Phone',true,true,53),
    ('pos','Point of Sale','DollarSign',true,true,54),
    ('pos_device_unclassified','POS Device (unclassified)','DollarSign',true,true,55),
    ('biometric','Biometric','ScanLine',true,true,56),
    ('biometric_device_unclassified','Biometric Device (unclassified)','ScanLine',true,true,57),
    ('endpoint','Endpoint / Workstation','Laptop',true,true,60),
    ('ups','UPS','BatteryCharging',true,true,61),
    ('pdu','PDU','Plug',true,true,62),
    ('database','Database','Database',true,true,70),
    ('directory','Directory','Users',true,true,71),
    ('dns','DNS','Globe',true,true,72),
    ('dhcp','DHCP','Globe',true,true,73),
    ('fingerprint','Fingerprint','ScanLine',true,true,74),
    ('application','Application','Boxes',true,true,75);

-- Drop the hard-coded CHECK so custom categories can be stored; the app validates.
ALTER TABLE devices DROP CONSTRAINT IF EXISTS devices_category_check;
