-- name: ListKegs :many
-- The register, with the freshness figure the fill/return cycle needs:
-- how long the current contents have been in the keg.
--
-- days_since_fill is NULL for an empty keg, which is different from zero
-- and has to stay different — an empty keg is not fresh, it is empty.
SELECT k.*,
       COALESCE(c.name, '')::text AS customer_name,
       COALESCE(l.name, '')::text AS location_name,
       msc.container_no,
       -- Contents come from whichever table holds them. A keg under
       -- 100 L holds packaged spirits; at or above it, a marked special
       -- container (EDM3-8-1). See 000064.
       -- Zero for an empty keg, which here is the truth rather than a
       -- missing value: it holds nothing. Note volume does NOT fall back
       -- to the keg's capacity — an empty 100 L keg contains nothing, not
       -- 100 litres, and a register that said otherwise would read as
       -- stock on hand.
       COALESCE(msc.laa,
                pi.bottles_on_hand * pr.bottle_size_ml * pr.target_abv_pct / 100000.0,
                0)::double precision AS contents_laa,
       COALESCE(msc.volume_l,
                pi.bottles_on_hand * pr.bottle_size_ml / 1000.0,
                0)::double precision AS contents_volume_l,
       COALESCE(msc.abv_pct, pr.target_abv_pct, 0)::double precision AS contents_abv_pct,
       COALESCE(pi.lot_code, '')::text AS contents_lot_code,
       -- Carried as a value plus a flag rather than as a nullable int.
       -- The cast that makes sqlc emit int32 also makes NULL scan as
       -- zero, and "filled today" reading the same as "empty" is the
       -- distinction this column exists for.
       COALESCE(CURRENT_DATE - k.last_filled_on, 0)::int AS days_since_fill,
       (k.last_filled_on IS NOT NULL
        AND (k.marked_container_id IS NOT NULL OR k.packaged_inventory_id IS NOT NULL))::boolean AS days_since_fill_known
FROM kegs k
LEFT JOIN customers c ON c.id = k.current_customer_id
LEFT JOIN locations l ON l.id = k.current_location_id
LEFT JOIN marked_special_containers msc ON msc.id = k.marked_container_id
LEFT JOIN packaged_inventory pi ON pi.id = k.packaged_inventory_id
LEFT JOIN products pr ON pr.id = pi.product_id
ORDER BY k.serial;

-- name: GetKeg :one
SELECT * FROM kegs WHERE id = $1;

-- name: GetKegBySerial :one
SELECT * FROM kegs WHERE serial = $1;

-- name: CreateKeg :one
INSERT INTO kegs (tenant_id, serial, capacity_l, material, purchase_cost_cad,
                  deposit_cad, purchased_on, notes, kind)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
RETURNING *;

-- name: SetKegState :one
-- One statement for every transition, so the status, the location, the
-- contents and the dates can never be updated separately and disagree.
-- The table's CHECK enforces the one pairing that must hold — full
-- against having contents — and this is where it is kept true.
UPDATE kegs
SET status              = @status,
    current_customer_id = @customer_id,
    current_location_id = @location_id,
    marked_container_id   = @marked_container_id,
    packaged_inventory_id = @packaged_inventory_id,
    last_filled_on      = COALESCE(@last_filled_on, last_filled_on),
    last_returned_on    = COALESCE(@last_returned_on, last_returned_on),
    updated_at          = NOW()
WHERE id = @id
RETURNING *;

-- name: RecordKegEvent :one
INSERT INTO keg_events (tenant_id, keg_id, kind, occurred_on, customer_id,
                        marked_container_id, packaged_inventory_id,
                        deposit_delta_cad, notes, user_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
RETURNING *;

-- name: ListKegEvents :many
SELECT e.*, COALESCE(c.name, '')::text AS customer_name
FROM keg_events e
LEFT JOIN customers c ON c.id = e.customer_id
WHERE e.keg_id = $1
ORDER BY e.occurred_on DESC, e.created_at DESC;

-- name: KegDepositLiability :many
-- What is owed back, by customer. The running sum of deposit deltas, so
-- a keg that went out and came back nets to nothing without anybody
-- having to remember to clear a balance.
--
-- Customers with a zero net are excluded: a list of everyone who has ever
-- had a keg is not the question being asked.
SELECT COALESCE(c.name, 'unattributed')::text AS customer_name,
       COALESCE(e.customer_id, '00000000-0000-0000-0000-000000000000'::uuid) AS customer_id,
       SUM(e.deposit_delta_cad)::numeric AS outstanding_cad,
       COUNT(*) FILTER (WHERE e.kind = 'shipped')::int AS kegs_shipped,
       COUNT(*) FILTER (WHERE e.kind = 'returned')::int AS kegs_returned
FROM keg_events e
LEFT JOIN customers c ON c.id = e.customer_id
WHERE e.deposit_delta_cad IS NOT NULL
GROUP BY e.customer_id, c.name
HAVING SUM(e.deposit_delta_cad) <> 0
ORDER BY SUM(e.deposit_delta_cad) DESC;

-- name: KegRegisterSummary :one
SELECT COUNT(*)::int AS total,
       COUNT(*) FILTER (WHERE kind <> 'keg')::int         AS non_keg,
       COUNT(*) FILTER (WHERE status = 'available')::int      AS available,
       COUNT(*) FILTER (WHERE status = 'filled')::int         AS filled,
       COUNT(*) FILTER (WHERE status = 'at_customer')::int    AS at_customer,
       COUNT(*) FILTER (WHERE status = 'returned_dirty')::int AS returned_dirty,
       COUNT(*) FILTER (WHERE status = 'out_of_service')::int AS out_of_service,
       COUNT(*) FILTER (WHERE status = 'lost')::int           AS lost
FROM kegs;
