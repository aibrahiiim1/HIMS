-- SSH CLI command-probe results: one row per (device, source, command) recording
-- whether a read-only command was supported on this device and what it returned.
-- output_preview is size-capped and secret-redacted at write time. This drives
-- the Wireless Controller "SSH CLI" collection-source panel and lets the
-- operator see exactly which commands a controller exposes.
CREATE TABLE IF NOT EXISTS ssh_cli_results (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id      UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    source         TEXT NOT NULL DEFAULT 'extreme_xcc_ssh',
    command        TEXT NOT NULL,
    -- parsed | not_parsed | unsupported | failed | timeout
    status         TEXT NOT NULL,
    output_preview TEXT NOT NULL DEFAULT '',
    parsed_rows    INTEGER NOT NULL DEFAULT 0,
    error_message  TEXT NOT NULL DEFAULT '',
    collected_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (device_id, source, command)
);

CREATE INDEX IF NOT EXISTS ssh_cli_results_device_idx ON ssh_cli_results (device_id, source);
