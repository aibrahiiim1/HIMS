-- Revert: fold the two new categories into their nearest pre-existing home so the
-- stricter CHECK can be re-applied without orphaning rows — load_balancer → the
-- generic network-appliance bucket 'firewall', pdu → 'ups' (both power).
UPDATE devices SET category='firewall' WHERE category='load_balancer';
UPDATE devices SET category='ups'      WHERE category='pdu';
ALTER TABLE devices DROP CONSTRAINT IF EXISTS devices_category_check;
ALTER TABLE devices ADD CONSTRAINT devices_category_check CHECK (category IN (
    'unknown','switch','router','firewall','access_point',
    'wireless_controller','server','virtual_host','virtual_machine',
    'storage','nvr','dvr','camera','printer','ip_phone','pbx',
    'voice_gateway','database','directory','dns','dhcp',
    'fingerprint','endpoint','ups','isp_router','application'));
