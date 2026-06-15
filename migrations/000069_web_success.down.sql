ALTER TABLE devices
    DROP COLUMN IF EXISTS web_last_proto,
    DROP COLUMN IF EXISTS web_last_scheme,
    DROP COLUMN IF EXISTS web_last_port,
    DROP COLUMN IF EXISTS web_last_credential_id,
    DROP COLUMN IF EXISTS web_pref_proto;
