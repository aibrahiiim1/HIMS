-- name: InsertFlowRecord :exec
INSERT INTO flow_records (kind, label, bytes, packets) VALUES ($1,$2,$3,$4);

-- name: TopFlowEntries :many
-- Roll up aggregate rows of one kind over a recent window, highest bytes first.
SELECT label, SUM(bytes)::bigint AS bytes, SUM(packets)::bigint AS packets
FROM flow_records
WHERE kind = $1 AND at >= $2
GROUP BY label
ORDER BY bytes DESC
LIMIT $3;

-- name: FlowOverview :one
SELECT
    COALESCE(SUM(bytes), 0)::bigint AS bytes,
    COALESCE(SUM(packets), 0)::bigint AS packets,
    COUNT(DISTINCT label) FILTER (WHERE kind = 'talker')::bigint AS talkers,
    MAX(at) AS last_at
FROM flow_records
WHERE at >= $1;
