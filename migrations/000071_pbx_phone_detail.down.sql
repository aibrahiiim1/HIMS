ALTER TABLE pbx_phones
    DROP COLUMN IF EXISTS extension,
    DROP COLUMN IF EXISTS mac_address,
    DROP COLUMN IF EXISTS ip_address;
