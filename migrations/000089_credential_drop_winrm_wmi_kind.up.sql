-- Retire the standalone winrm + wmi credential kinds — superseded by the unified "windows" kind.
-- (No winrm/wmi credentials remain; new ones are created as "windows".)
ALTER TABLE credentials DROP CONSTRAINT IF EXISTS credentials_kind_check;
ALTER TABLE credentials ADD CONSTRAINT credentials_kind_check
  CHECK (kind = ANY (ARRAY['snmp_v2c','snmp_v3','ssh','cli','windows','http_basic','onvif','vendor_api','ldap']));
