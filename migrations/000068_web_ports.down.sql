ALTER TABLE devices
    DROP COLUMN IF EXISTS web_scheme_pref,
    DROP COLUMN IF EXISTS web_port_pref,
    DROP COLUMN IF EXISTS web_alt_ports,
    DROP COLUMN IF EXISTS web_notes,
    DROP COLUMN IF EXISTS web_last_ok,
    DROP COLUMN IF EXISTS web_last_ok_at;

DROP TABLE IF EXISTS web_port_candidates;
