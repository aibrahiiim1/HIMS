-- Add 'dvr' as a first-class device category. Hikvision/OEM recorders report
-- <deviceType>DVR</deviceType> over ISAPI; until now both NVR and DVR collapsed
-- to 'nvr'. CCTV Phase 2 classifies DVRs distinctly so the fleet CCTV summary can
-- count NVRs vs DVRs vs standalone cameras separately.
ALTER TABLE devices DROP CONSTRAINT IF EXISTS devices_category_check;
ALTER TABLE devices ADD CONSTRAINT devices_category_check CHECK (category IN (
    'unknown','switch','router','firewall','access_point',
    'wireless_controller','server','virtual_host','virtual_machine',
    'storage','nvr','dvr','camera','printer','ip_phone','pbx',
    'voice_gateway','database','directory','dns','dhcp',
    'fingerprint','endpoint','ups','isp_router','application'));
