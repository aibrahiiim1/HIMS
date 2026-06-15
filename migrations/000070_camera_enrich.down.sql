ALTER TABLE camera_info
    DROP COLUMN IF EXISTS device_name,
    DROP COLUMN IF EXISTS firmware,
    DROP COLUMN IF EXISTS serial,
    DROP COLUMN IF EXISTS mac_address,
    DROP COLUMN IF EXISTS ip_address,
    DROP COLUMN IF EXISTS subnet_mask,
    DROP COLUMN IF EXISTS gateway,
    DROP COLUMN IF EXISTS dns_server,
    DROP COLUMN IF EXISTS ntp_server,
    DROP COLUMN IF EXISTS time_zone;
