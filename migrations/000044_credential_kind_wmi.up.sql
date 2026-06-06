-- Allow the wmi credential kind (WMI/DCOM legacy Windows fallback). Also include
-- 'cli' (Manual CLI Probe) so the constraint matches every kind the app issues.
-- Replace the CHECK constraint with the full, current set.
ALTER TABLE credentials DROP CONSTRAINT IF EXISTS credentials_kind_check;
ALTER TABLE credentials ADD CONSTRAINT credentials_kind_check
    CHECK (kind IN ('snmp_v2c','snmp_v3','ssh','cli','winrm','wmi','http_basic',
                    'onvif','vendor_api','ldap'));
