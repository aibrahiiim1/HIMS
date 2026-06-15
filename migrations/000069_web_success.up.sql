-- Web/HTTP Collection Framework: protocol-accurate last-success record + a
-- preferred-protocol override. Builds on 000068 (web_port_candidates + per-device
-- web_scheme/port/alt/notes + web_last_ok[_at]).
--
-- The point: when HIMS authenticates + collects a web-managed device it must
-- remember EXACTLY what worked — protocol (isapi/onvif/http/snmp/vendor_api),
-- scheme, port, endpoint, and which credential — so (a) the UI shows the REAL
-- source ("Managed via ISAPI", not ONVIF) and (b) the next collect/scan tries the
-- known-good endpoint first instead of re-guessing.
ALTER TABLE devices
    ADD COLUMN web_last_proto      TEXT NOT NULL DEFAULT '',  -- isapi | onvif | http | snmp | vendor_api | rtsp
    ADD COLUMN web_last_scheme     TEXT NOT NULL DEFAULT '',  -- http | https
    ADD COLUMN web_last_port       INTEGER,                   -- port that answered
    ADD COLUMN web_last_credential_id UUID REFERENCES credentials(id) ON DELETE SET NULL,
    ADD COLUMN web_pref_proto      TEXT NOT NULL DEFAULT '' CHECK (web_pref_proto IN ('', 'isapi', 'onvif', 'http'));
