-- Purpose-specific CCTV credential binding. A camera/NVR/DVR is collected over
-- ONVIF/ISAPI with a WEB credential (http_basic/onvif), but the generic
-- credential_id is bind-on-success for ANY protocol — so an SNMP discovery/monitor
-- success would overwrite it with an SNMP credential and break CCTV collection.
-- cctv_credential_id holds the web credential that last authenticated for CCTV
-- collection; it is never touched by SNMP. CCTV collection prefers it; SNMP/general
-- monitoring keeps using credential_id.
ALTER TABLE devices ADD COLUMN cctv_credential_id UUID REFERENCES credentials(id) ON DELETE SET NULL;

-- Backfill: for already-collected camera/NVR/DVR devices whose generic
-- credential_id is currently a web (ONVIF/HTTP-Basic) credential, seed the new
-- CCTV binding from it so existing recorders keep working and are immediately
-- protected from SNMP drift without waiting for the next manual collect.
UPDATE devices d SET cctv_credential_id = d.credential_id
  WHERE d.category IN ('camera', 'nvr', 'dvr')
    AND d.cctv_credential_id IS NULL
    AND d.credential_id IN (SELECT id FROM credentials WHERE kind IN ('onvif', 'http_basic'));
