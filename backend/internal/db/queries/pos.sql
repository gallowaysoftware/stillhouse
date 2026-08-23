-- name: UpsertPOSProductMap :one
INSERT INTO pos_product_map (tenant_id, source, external_sku, product_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (tenant_id, source, external_sku) DO UPDATE
SET product_id = EXCLUDED.product_id
RETURNING *;

-- name: DeletePOSProductMap :exec
DELETE FROM pos_product_map WHERE id = $1;

-- name: ListPOSProductMap :many
SELECT m.*, p.name AS product_name
FROM pos_product_map m
JOIN products p ON p.id = m.product_id
ORDER BY m.source, m.external_sku;

-- name: LookupPOSProduct :one
SELECT product_id FROM pos_product_map
WHERE source = $1 AND external_sku = $2;

-- name: InsertPOSSale :one
-- The idempotent write. A redelivered line collides on
-- (tenant, source, external_id) and DO NOTHING means no second row and
-- therefore no second removal — see 000065.
--
-- Returns nothing on a collision, which is how the caller counts
-- duplicates without a second query.
INSERT INTO pos_sales (tenant_id, source, external_id, external_sku, description,
                       quantity, unit_price_cad, sold_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (tenant_id, source, external_id) DO NOTHING
RETURNING *;

-- name: ListPOSSales :many
SELECT s.*, COALESCE(p.name, '')::text AS product_name
FROM pos_sales s
LEFT JOIN pos_product_map m ON m.source = s.source AND m.external_sku = s.external_sku
LEFT JOIN products p ON p.id = m.product_id
WHERE (@status_filter::text = '' OR s.status::text = @status_filter::text)
ORDER BY s.sold_at DESC
LIMIT @row_limit::int;

-- name: ListPendingPOSSales :many
SELECT * FROM pos_sales WHERE status = 'pending' ORDER BY sold_at;

-- name: MarkPOSSalePosted :exec
UPDATE pos_sales
SET status = 'posted', removal_id = $2, posted_at = NOW(), reject_reason = ''
WHERE id = $1;

-- name: MarkPOSSaleRejected :exec
UPDATE pos_sales SET status = 'rejected', reject_reason = $2 WHERE id = $1;

-- name: SetPOSSaleIgnored :exec
UPDATE pos_sales SET status = 'ignored', reject_reason = $2 WHERE id = $1;

-- name: POSSaleSummary :one
SELECT COUNT(*) FILTER (WHERE status = 'pending')::int  AS pending,
       COUNT(*) FILTER (WHERE status = 'posted')::int   AS posted,
       COUNT(*) FILTER (WHERE status = 'rejected')::int AS rejected,
       COUNT(*) FILTER (WHERE status = 'ignored')::int  AS ignored
FROM pos_sales;

-- name: OldestPackagedLotForProduct :one
-- Which lot a tasting-room sale comes off. Oldest first with stock on
-- hand: a bottle sold over the counter came from the case that was
-- already open, and FIFO is the only rule that does not require the
-- till to know which lot the server reached for.
--
-- Released lots only where release is required, because selling stock
-- nobody has released is the thing batch release exists to stop.
SELECT pi.* FROM packaged_inventory pi
WHERE pi.product_id = $1
  AND pi.bottles_on_hand > 0
  AND (NOT @require_release::boolean OR pi.released_at IS NOT NULL)
ORDER BY pi.created_at
LIMIT 1;
