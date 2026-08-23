-- name: ListJournalAccounts :many
SELECT * FROM journal_accounts ORDER BY kind;

-- name: UpsertJournalAccount :one
INSERT INTO journal_accounts (
    tenant_id, kind, debit_account, credit_account, debit_name, credit_name, memo_prefix
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (tenant_id, kind) DO UPDATE SET
    debit_account  = EXCLUDED.debit_account,
    credit_account = EXCLUDED.credit_account,
    debit_name     = EXCLUDED.debit_name,
    credit_name    = EXCLUDED.credit_name,
    memo_prefix    = EXCLUDED.memo_prefix
RETURNING *;

-- name: JournalDutyEvents :many
-- Duty crystallised in the period, from wherever the duty point falls.
--
-- Both sources are read and neither is filtered out, because across a
-- duty-point cutover both coexist: stock bottled under the old basis is
-- dutied on its way out, stock bottled under the new basis was dutied on
-- the way in. Taking only one would understate the period. Voided rows
-- are excluded — they were withdrawn, and the duty with them.
SELECT
    'bottling'::TEXT       AS source,
    br.id                  AS source_id,
    br.bottling_date       AS event_date,
    br.duty_amount_cad     AS amount_cad,
    p.name                 AS description,
    br.lot_code            AS reference
FROM bottling_runs br
JOIN products p ON p.id = br.product_id
WHERE br.voided_at IS NULL
  AND br.duty_amount_cad IS NOT NULL
  AND br.bottling_date >= sqlc.arg(period_start)::DATE
  AND br.bottling_date <= sqlc.arg(period_end)::DATE
UNION ALL
SELECT
    'removal'::TEXT,
    pr.id,
    pr.removal_date,
    pr.duty_amount_cad,
    pr.product_name,
    pr.destination_name
FROM (
    SELECT r.id, r.removal_date, r.duty_amount_cad, r.destination_name, pd.name AS product_name
    FROM packaging_removals r
    JOIN packaged_inventory pi ON pi.id = r.packaged_inventory_id
    JOIN products pd ON pd.id = pi.product_id
    WHERE r.voided_at IS NULL AND r.duty_amount_cad > 0
) pr
WHERE pr.removal_date >= sqlc.arg(period_start)::DATE
  AND pr.removal_date <= sqlc.arg(period_end)::DATE
ORDER BY event_date, source_id;

-- name: JournalMaterialReceipts :many
-- Raw material in, at the lot cost actually recorded. A lot with no unit
-- cost contributes nothing and is reported as unpriced rather than as
-- zero — a zero would silently understate inventory.
SELECT ml.id, ml.received_at, ml.quantity_received, ml.unit_cost_cad,
       ml.landed_unit_cost_cad,
       m.name AS material_name, m.uom, ml.supplier_lot
FROM material_lots ml
JOIN materials m ON m.id = ml.material_id
WHERE ml.received_at >= sqlc.arg(period_start)::TIMESTAMPTZ
  AND ml.received_at <  sqlc.arg(period_end)::TIMESTAMPTZ
ORDER BY ml.received_at, ml.id;

-- name: JournalMaterialConsumption :many
-- Raw material into a mash, valued at the lot it came from.
SELECT mu.id, mr.mash_date, mu.quantity_used,
       COALESCE(ml.landed_unit_cost_cad, ml.unit_cost_cad) AS unit_cost_cad,
       m.name AS material_name, mu.uom, mr.mash_no
FROM mash_ingredient_usage mu
JOIN mash_runs mr ON mr.id = mu.mash_run_id
JOIN materials m  ON m.id = mu.material_id
LEFT JOIN material_lots ml ON ml.id = mu.material_lot_id
WHERE mr.mash_date >= sqlc.arg(period_start)::DATE
  AND mr.mash_date <= sqlc.arg(period_end)::DATE
ORDER BY mr.mash_date, mu.id;

-- name: JournalRemovalsForCOGS :many
-- Packaged stock leaving, with the bottling run behind it so its material
-- cost can be apportioned per bottle.
SELECT r.id, r.removal_date, r.bottles_removed, r.destination_name,
       p.name AS product_name,
       pi.bottling_run_id,
       br.bottle_count AS run_bottle_count
FROM packaging_removals r
JOIN packaged_inventory pi ON pi.id = r.packaged_inventory_id
JOIN products p            ON p.id = pi.product_id
LEFT JOIN bottling_runs br ON br.id = pi.bottling_run_id
WHERE r.voided_at IS NULL
  AND r.removal_date >= sqlc.arg(period_start)::DATE
  AND r.removal_date <= sqlc.arg(period_end)::DATE
ORDER BY r.removal_date, r.id;

