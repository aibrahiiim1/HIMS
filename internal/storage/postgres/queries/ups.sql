-- name: UpsertUPSStatus :exec
INSERT INTO ups_status (device_id, manufacturer, model, battery_status, charge_pct, runtime_min, load_pct, last_seen_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (device_id) DO UPDATE SET
    manufacturer = EXCLUDED.manufacturer,
    model = EXCLUDED.model,
    battery_status = EXCLUDED.battery_status,
    charge_pct = EXCLUDED.charge_pct,
    runtime_min = EXCLUDED.runtime_min,
    load_pct = EXCLUDED.load_pct,
    last_seen_at = EXCLUDED.last_seen_at;

-- name: GetUPSStatus :one
SELECT * FROM ups_status WHERE device_id = $1;
