-- Per-command parser diagnostics so the operator can see WHY rows were parsed or
-- skipped (line count, detected headers, skipped count, warnings).
ALTER TABLE ssh_cli_results ADD COLUMN IF NOT EXISTS line_count   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ssh_cli_results ADD COLUMN IF NOT EXISTS headers      TEXT NOT NULL DEFAULT '';
ALTER TABLE ssh_cli_results ADD COLUMN IF NOT EXISTS skipped_rows INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ssh_cli_results ADD COLUMN IF NOT EXISTS warnings     TEXT NOT NULL DEFAULT '';

-- Controller-reported summary counts, kept SEPARATE from parsed roster rows so
-- the UI can show "reported 123 APs vs parsed 80 rows" and never present partial
-- data as complete. One row per controller device.
CREATE TABLE IF NOT EXISTS wireless_controller_summary (
    device_id         UUID PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    summary_source    TEXT NOT NULL DEFAULT 'extreme_xcc_ssh',
    networks_count    INTEGER NOT NULL DEFAULT 0,
    switches_count    INTEGER NOT NULL DEFAULT 0,
    ap_total          INTEGER NOT NULL DEFAULT 0,
    adoption_primary  INTEGER NOT NULL DEFAULT 0,
    adoption_backup   INTEGER NOT NULL DEFAULT 0,
    active_aps        INTEGER NOT NULL DEFAULT 0,
    non_active_aps    INTEGER NOT NULL DEFAULT 0,
    clients_total     INTEGER NOT NULL DEFAULT 0,
    parsed_ap_rows    INTEGER NOT NULL DEFAULT 0,
    parsed_client_rows INTEGER NOT NULL DEFAULT 0,
    parsed_ssid_rows  INTEGER NOT NULL DEFAULT 0,
    -- complete | partial | summary_only | failed
    collection_status TEXT NOT NULL DEFAULT 'failed',
    detail            TEXT NOT NULL DEFAULT '',
    collected_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
