-- Inventory organisation: new evidence-based device categories so the inventory can
-- separate out-of-band management controllers, biometric/attendance devices, and POS
-- endpoints into their own views instead of burying them under "server"/"endpoint"/
-- "unknown". Also adds network_device_unclassified (an SNMP-answered device with no exact
-- type), which the classifier already emits but the CHECK constraint had not yet allowed.
--   bmc                          — out-of-band controllers (HPE iLO, Dell iDRAC, Lenovo
--                                  XClarity/IMM, Supermicro IPMI, generic Redfish BMC).
--   biometric                    — fingerprint / time-attendance / access-control devices
--                                  (ZKTeco, Suprema, Anviz, Hik/Dahua access control).
--   biometric_device_unclassified— evidence says biometric, exact vendor/model unknown.
--   pos                          — point-of-sale terminals / POS PCs / payment endpoints.
--   pos_device_unclassified      — evidence says POS, exact type unknown.
-- The classifier persists devices.category (CHECK-constrained), so the set is extended
-- here before the catalog/classifier can emit these tokens.
ALTER TABLE devices DROP CONSTRAINT IF EXISTS devices_category_check;
ALTER TABLE devices ADD CONSTRAINT devices_category_check CHECK (category IN (
    'unknown','network_device_unclassified','switch','router','firewall','access_point',
    'wireless_controller','server','virtual_host','virtual_machine',
    'storage','nvr','dvr','camera','printer','ip_phone','pbx',
    'voice_gateway','database','directory','dns','dhcp',
    'fingerprint','endpoint','ups','isp_router','application',
    'load_balancer','pdu',
    'bmc','biometric','biometric_device_unclassified','pos','pos_device_unclassified'));

-- One-time, EVIDENCE-BASED reclassification of existing out-of-band controllers from
-- 'server' to 'bmc'. The evidence is concrete, never a guess: either a bmc_info row was
-- collected for the device (a Redfish/IPMI controller was reached), or its identity model/
-- subtype is unambiguously a management controller (iLO/iDRAC/XClarity/IMM/IPMI). A plain
-- server that merely exposes Redfish is NOT moved (no iLO/iDRAC identity). Locked
-- classifications are left untouched (operator authority).
UPDATE devices d SET category = 'bmc'
WHERE d.category = 'server'
  AND COALESCE(d.classification_locked, false) = false
  AND (
    EXISTS (SELECT 1 FROM bmc_info b WHERE b.device_id = d.id)
    OR d.model ILIKE '%ilo%' OR d.model ILIKE '%idrac%' OR d.model ILIKE '%xclarity%'
    OR d.model ILIKE '%integrated management module%' OR d.subtype ILIKE '%ilo%'
    OR d.subtype ILIKE '%idrac%' OR d.subtype ILIKE '%xclarity%' OR d.subtype ILIKE '%imm%'
  );
