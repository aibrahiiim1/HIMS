-- Device Intelligence Phase 4: two new first-class device categories the vendor
-- packs classify into and that had no home before:
--   load_balancer — F5 BIG-IP, Citrix NetScaler/ADC, A10 Thunder, Kemp LoadMaster
--   pdu           — switched/metered power distribution units (APC/Eaton/ServerTech),
--                   distinct from a UPS (the APC enterprise PEN .318 covers both, so
--                   without a pdu category a rack PDU would misclassify as a UPS).
-- The classifier persists devices.category, which is CHECK-constrained, so the set
-- must be extended here before the catalog can emit these tokens.
ALTER TABLE devices DROP CONSTRAINT IF EXISTS devices_category_check;
ALTER TABLE devices ADD CONSTRAINT devices_category_check CHECK (category IN (
    'unknown','switch','router','firewall','access_point',
    'wireless_controller','server','virtual_host','virtual_machine',
    'storage','nvr','dvr','camera','printer','ip_phone','pbx',
    'voice_gateway','database','directory','dns','dhcp',
    'fingerprint','endpoint','ups','isp_router','application',
    'load_balancer','pdu'));
