-- name: UpsertSSHCliResult :exec
INSERT INTO ssh_cli_results (device_id, source, command, status, output_preview, parsed_rows, error_message, collected_at)
VALUES ($1,$2,$3,$4,$5,$6,$7, now())
ON CONFLICT (device_id, source, command) DO UPDATE SET
    status = EXCLUDED.status,
    output_preview = EXCLUDED.output_preview,
    parsed_rows = EXCLUDED.parsed_rows,
    error_message = EXCLUDED.error_message,
    collected_at = now();

-- name: ListSSHCliResults :many
SELECT * FROM ssh_cli_results WHERE device_id = $1 ORDER BY command;

-- name: DeleteSSHCliResultsForSource :exec
DELETE FROM ssh_cli_results WHERE device_id = $1 AND source = $2;

-- name: CountSSHCliBySource :many
SELECT source, status, count(*) AS n FROM ssh_cli_results WHERE device_id = $1 GROUP BY source, status;
