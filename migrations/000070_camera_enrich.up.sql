-- Camera enrichment: beyond identity, capture the useful read-only facts a
-- Hikvision/ONVIF camera exposes over ISAPI — firmware/serial, the NIC (MAC + IP
-- config), and time/NTP. Populated by the camera collector; all optional.
ALTER TABLE camera_info
    ADD COLUMN device_name  TEXT,
    ADD COLUMN firmware     TEXT,
    ADD COLUMN serial       TEXT,
    ADD COLUMN mac_address  TEXT,
    ADD COLUMN ip_address   TEXT,
    ADD COLUMN subnet_mask  TEXT,
    ADD COLUMN gateway      TEXT,
    ADD COLUMN dns_server   TEXT,
    ADD COLUMN ntp_server   TEXT,
    ADD COLUMN time_zone    TEXT;
