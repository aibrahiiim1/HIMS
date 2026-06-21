-- Add a unified "windows" credential kind: one user:password the system uses across WinRM, WMI/DCOM,
-- legacy SMB and the relay agent — no need to add the same secret twice as separate winrm + wmi.
ALTER TABLE credentials DROP CONSTRAINT IF EXISTS credentials_kind_check;
ALTER TABLE credentials ADD CONSTRAINT credentials_kind_check
  CHECK (kind = ANY (ARRAY['snmp_v2c','snmp_v3','ssh','cli','winrm','wmi','windows','http_basic','onvif','vendor_api','ldap']));
