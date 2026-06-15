-- Revert: fold any DVRs back to NVR (the prior collapsed category) so the CHECK
-- without 'dvr' still holds, then restore the original constraint.
UPDATE devices SET category = 'nvr' WHERE category = 'dvr';
ALTER TABLE devices DROP CONSTRAINT IF EXISTS devices_category_check;
ALTER TABLE devices ADD CONSTRAINT devices_category_check CHECK (category IN (
    'unknown','switch','router','firewall','access_point',
    'wireless_controller','server','virtual_host','virtual_machine',
    'storage','nvr','camera','printer','ip_phone','pbx',
    'voice_gateway','database','directory','dns','dhcp',
    'fingerprint','endpoint','ups','isp_router','application'));
