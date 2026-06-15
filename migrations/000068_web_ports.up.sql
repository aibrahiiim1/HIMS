-- Configurable HTTP/Web candidate ports + per-device web-access override.
--
-- Web-managed devices (NVRs/DVRs/cameras, appliances) frequently expose their
-- HTTP/API surface on non-standard ports (e.g. Hikvision "8000 + host octet" →
-- 8008/8010/8012, or 8081/8082/9000…). HIMS must not assume only 80/443/8080/
-- 8443. These knobs let the operator declare the candidate ports for the whole
-- fleet, and override per device when one recorder differs from another.

-- Fleet-wide candidate ports the scan + collectors try (in addition to any the
-- scan discovers open). scheme = which scheme(s) to attempt for this port.
CREATE TABLE web_port_candidates (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    port       INTEGER NOT NULL UNIQUE CHECK (port > 0 AND port < 65536),
    scheme     TEXT NOT NULL DEFAULT 'http' CHECK (scheme IN ('http', 'https', 'both')),
    enabled    BOOLEAN NOT NULL DEFAULT true,
    note       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Sensible defaults including the common Hikvision/OEM custom HTTP ports.
INSERT INTO web_port_candidates (port, scheme, note) VALUES
    (80,   'http',  'Standard HTTP'),
    (443,  'https', 'Standard HTTPS'),
    (8008, 'http',  'Hikvision NVR/DVR custom HTTP (8000+octet)'),
    (8009, 'http',  'Custom HTTP'),
    (8010, 'http',  'Custom HTTP'),
    (8012, 'http',  'Hikvision NVR HTTP (8000+octet)'),
    (8080, 'http',  'Common alternate HTTP'),
    (8081, 'http',  'Common alternate HTTP'),
    (8443, 'https', 'Common alternate HTTPS')
ON CONFLICT (port) DO NOTHING;

-- Per-device web-access override + last-successful endpoint. All optional; empty
-- means "use the fleet defaults + discovered ports". web_last_ok records the base
-- URL that last authenticated/collected so the UI can show the working endpoint
-- and the collector can try it first.
ALTER TABLE devices
    ADD COLUMN web_scheme_pref TEXT NOT NULL DEFAULT '' CHECK (web_scheme_pref IN ('', 'http', 'https')),
    ADD COLUMN web_port_pref   INTEGER CHECK (web_port_pref IS NULL OR (web_port_pref > 0 AND web_port_pref < 65536)),
    ADD COLUMN web_alt_ports   TEXT NOT NULL DEFAULT '',   -- comma-separated extra ports, e.g. "8010,8000"
    ADD COLUMN web_notes       TEXT NOT NULL DEFAULT '',
    ADD COLUMN web_last_ok     TEXT NOT NULL DEFAULT '',   -- last successful base URL (scheme://ip[:port])
    ADD COLUMN web_last_ok_at  TIMESTAMPTZ;
