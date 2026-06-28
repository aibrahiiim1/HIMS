-- Manual onboarding: ZKTeco biometric devices authenticate over their native protocol with
-- a numeric COMMUNICATION KEY (not a user:password). To collect such a device again after
-- onboarding, the key must be stored ENCRYPTED like any other secret and bound to the device
-- — so add 'zkteco' to the allowed credential kinds. The key is never stored in plaintext or
-- returned; it is sealed (AES-256-GCM) in credentials.encrypted_blob like every other secret.
ALTER TABLE credentials DROP CONSTRAINT IF EXISTS credentials_kind_check;
ALTER TABLE credentials ADD CONSTRAINT credentials_kind_check CHECK (kind = ANY (ARRAY[
    'snmp_v2c','snmp_v3','ssh','cli','windows','http_basic','onvif','vendor_api','ldap','zkteco']));
