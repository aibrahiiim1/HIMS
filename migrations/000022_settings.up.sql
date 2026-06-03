-- HIMS settings: a small key/value store for operator-tunable knobs. Values are
-- text (parsed per-key by the API). Seeded with the timeout/concurrency
-- defaults the discovery scan + collectors use; the Settings page edits them.

CREATE TABLE app_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO app_settings (key, value) VALUES
    ('snmp_timeout_ms',  '3000'),   -- per-SNMP-attempt timeout (discovery scan)
    ('tcp_timeout_ms',   '500'),    -- per-port TCP connect timeout (scan + aliveness)
    ('scan_concurrency', '16'),     -- default hosts probed in parallel
    ('http_timeout_ms',  '20000'),  -- HTTP client timeout (Redfish/UniFi/etc. imports)
    ('winrm_timeout_ms', '60000')   -- WinRM op timeout (Hyper-V import)
ON CONFLICT (key) DO NOTHING;
