-- name: CreateStampDisposition :one
INSERT INTO excise_stamp_dispositions (
    tenant_id, stamp_order_id, kind, quantity, serial_start, serial_end,
    occurred_on, explanation, reported_ref, recorded_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: ListStampDispositionsForOrder :many
SELECT d.*, COALESCE(u.display_name, '') AS recorded_by_name
FROM excise_stamp_dispositions d
LEFT JOIN users u ON u.id = d.recorded_by
WHERE d.stamp_order_id = $1
ORDER BY d.occurred_on, d.created_at;

-- name: ListStampDispositions :many
-- Everything not applied to a bottle, most recent first. The losses and
-- thefts are what CRA asks about; the spoilage is what makes the
-- arithmetic add up.
SELECT d.*, o.jurisdiction, COALESCE(u.display_name, '') AS recorded_by_name
FROM excise_stamp_dispositions d
JOIN excise_stamp_orders o ON o.id = d.stamp_order_id
LEFT JOIN users u ON u.id = d.recorded_by
WHERE (sqlc.arg(kind)::TEXT = '' OR d.kind::TEXT = sqlc.arg(kind)::TEXT)
ORDER BY d.occurred_on DESC, d.created_at DESC;

-- name: ListStampUsageForOrder :many
-- Every bottling run that drew on this order, with the range it took.
-- Voided runs are included and flagged: the stamps were applied to
-- bottles, and voiding the run does not un-apply them.
SELECT u.id, u.bottling_run_id, u.bottle_count, u.serial_start, u.serial_end,
       u.voids, u.created_at,
       br.run_no, br.bottling_date, (br.voided_at IS NOT NULL)::BOOLEAN AS run_voided,
       p.name AS product_name
FROM bottling_run_stamp_usage u
JOIN bottling_runs br ON br.id = u.bottling_run_id
JOIN products p       ON p.id = br.product_id
WHERE u.stamp_order_id = $1
ORDER BY br.bottling_date, br.run_no;
