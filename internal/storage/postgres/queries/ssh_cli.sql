-- name: UpsertSSHCliResult :exec
INSERT INTO ssh_cli_results (device_id, source, command, status, output_preview, parsed_rows, error_message, line_count, headers, skipped_rows, warnings, collected_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11, now())
ON CONFLICT (device_id, source, command) DO UPDATE SET
    status = EXCLUDED.status,
    output_preview = EXCLUDED.output_preview,
    parsed_rows = EXCLUDED.parsed_rows,
    error_message = EXCLUDED.error_message,
    line_count = EXCLUDED.line_count,
    headers = EXCLUDED.headers,
    skipped_rows = EXCLUDED.skipped_rows,
    warnings = EXCLUDED.warnings,
    collected_at = now();

-- name: ListSSHCliResults :many
SELECT * FROM ssh_cli_results WHERE device_id = $1 ORDER BY command;

-- name: DeleteSSHCliResultsForSource :exec
DELETE FROM ssh_cli_results WHERE device_id = $1 AND source = $2;

-- name: CountSSHCliBySource :many
SELECT source, status, count(*) AS n FROM ssh_cli_results WHERE device_id = $1 GROUP BY source, status;

-- name: UpsertWirelessControllerSummary :exec
INSERT INTO wireless_controller_summary
    (device_id, summary_source, networks_count, switches_count, ap_total, adoption_primary, adoption_backup,
     active_aps, non_active_aps, clients_total, parsed_ap_rows, parsed_client_rows, parsed_ssid_rows,
     collection_status, detail, collected_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15, now())
ON CONFLICT (device_id) DO UPDATE SET
    summary_source = EXCLUDED.summary_source,
    networks_count = EXCLUDED.networks_count,
    switches_count = EXCLUDED.switches_count,
    ap_total = EXCLUDED.ap_total,
    adoption_primary = EXCLUDED.adoption_primary,
    adoption_backup = EXCLUDED.adoption_backup,
    active_aps = EXCLUDED.active_aps,
    non_active_aps = EXCLUDED.non_active_aps,
    clients_total = EXCLUDED.clients_total,
    parsed_ap_rows = EXCLUDED.parsed_ap_rows,
    parsed_client_rows = EXCLUDED.parsed_client_rows,
    parsed_ssid_rows = EXCLUDED.parsed_ssid_rows,
    collection_status = EXCLUDED.collection_status,
    detail = EXCLUDED.detail,
    collected_at = now();

-- name: GetWirelessControllerSummary :one
SELECT * FROM wireless_controller_summary WHERE device_id = $1;
