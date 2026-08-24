-- name: BottlingRunChainFeeds :many
-- Given a bottling run, return the bulk_movements that fed into its source
-- container in the 365 days leading up to the bottling date. Most useful
-- when the source container is a blend tank or spirit receiver that
-- collected from multiple barrels/distillations.
SELECT bm.*,
       src.name AS source_name,
       dst.name AS destination_name
FROM bulk_movements bm
LEFT JOIN bulk_containers src ON src.id = bm.source_container_id
LEFT JOIN bulk_containers dst ON dst.id = bm.destination_container_id
WHERE bm.destination_container_id = $1
  AND bm.occurred_at <= $2
  AND bm.occurred_at >= $2 - INTERVAL '365 days'
ORDER BY bm.occurred_at DESC;

-- name: DistillationChainFromGauge :many
-- Pull the distillation run + every charge → ferment → mash → recipe
-- subtree behind a production_gauge bulk_movement. One row per charge
-- so multi-charge blends are fully represented in trace + cost rollups.
SELECT dr.id AS distillation_run_id, dr.run_no AS distillation_run_no, dr.still_label,
       fr.id AS fermentation_run_id, fr.fermenter_label,
       fr.yeast_lot_id AS yeast_lot_id,
       yml.supplier_lot AS yeast_supplier_lot,
       ym.name          AS yeast_material_name,
       mr.id AS mash_run_id, mr.mash_no, mr.mash_date,
       rv.id AS recipe_version_id, rv.version_no AS recipe_version_no,
       r.id  AS recipe_id, r.name AS recipe_name,
       dc.charge_order AS charge_order
FROM production_gauges pg
JOIN distillation_runs dr      ON dr.id = pg.distillation_run_id
LEFT JOIN distillation_charges dc ON dc.distillation_run_id = dr.id
LEFT JOIN fermentation_runs fr    ON fr.id = dc.fermentation_run_id
LEFT JOIN material_lots yml       ON yml.id = fr.yeast_lot_id
LEFT JOIN materials ym            ON ym.id = fr.yeast_material_id
LEFT JOIN mash_runs mr            ON mr.id = fr.mash_run_id
LEFT JOIN recipe_versions rv      ON rv.id = mr.recipe_version_id
LEFT JOIN recipes r               ON r.id = rv.recipe_id
WHERE pg.bulk_movement_id = $1
ORDER BY dc.charge_order;

-- name: BarrelDumpsForContainerFill :many
-- For a barrel_dump-tagged bulk_movement, return the barrel + its fill
-- history (so we can include the original distillation behind the fill).
SELECT bm.id AS movement_id, bm.occurred_at,
       barrel.id AS barrel_id, barrel.name AS barrel_name,
       ba.cooperage_supplier, ba.fill_date, ba.days_aged_at_dump
FROM bulk_movements bm
JOIN bulk_containers barrel       ON barrel.id = bm.source_container_id
LEFT JOIN barrel_attributes ba    ON ba.container_id = barrel.id
WHERE bm.id = $1
  AND barrel.kind = 'barrel';

-- name: RecallExactChainFromMaterialLot :many
-- Forward from a material lot to every production gauge it reached.
--
-- This half of a recall is exact. A material lot goes into named mashes,
-- those mashes into named fermentations, those into named distillation
-- charges, and each run has one gauge. Every link is a recorded row and
-- nothing is inferred.
--
-- Where it stops is the point of the query. A gauge puts spirit into a
-- container, and from there it is blended, transferred and vatted — after
-- which "which mash is in this tank" is no longer a fact the ledger
-- holds. Everything past the gauge is possible contact, not certainty,
-- and is asked for separately so the two never get added together.
SELECT ml.id            AS material_lot_id,
       ml.supplier_lot,
       m.name           AS material_name,
       s.name           AS supplier_name,
       mr.id            AS mash_run_id,
       mr.mash_no,
       mr.mash_date,
       mu.quantity_used,
       mu.uom,
       fr.id            AS fermentation_run_id,
       fr.fermenter_label,
       dr.id            AS distillation_run_id,
       dr.run_no        AS distillation_run_no,
       dr.voided_at     AS distillation_voided_at,
       g.id             AS production_gauge_id,
       g.gauge_date,
       g.laa            AS gauge_laa,
       g.destination_container_id,
       bc.name          AS container_name
FROM material_lots ml
JOIN materials m               ON m.id = ml.material_id
LEFT JOIN suppliers s          ON s.id = ml.supplier_id
JOIN mash_ingredient_usage mu  ON mu.material_lot_id = ml.id
JOIN mash_runs mr              ON mr.id = mu.mash_run_id
LEFT JOIN fermentation_runs fr ON fr.mash_run_id = mr.id
LEFT JOIN distillation_charges dc ON dc.fermentation_run_id = fr.id
LEFT JOIN distillation_runs dr ON dr.id = dc.distillation_run_id
LEFT JOIN production_gauges g  ON g.distillation_run_id = dr.id
LEFT JOIN bulk_containers bc   ON bc.id = g.destination_container_id
WHERE ml.id = @material_lot_id
ORDER BY mr.mash_date, mr.mash_no, fr.fermenter_label, dr.run_no;

-- name: RecallPackagedLotsFromContainers :many
-- Bottling runs that drew from a container after affected spirit entered
-- it, and the packaged lots they produced.
--
-- Possible contact, not certainty, and the distinction is load-bearing in
-- both directions: treating it as certainty recalls stock that was never
-- affected, and ignoring it leaves affected stock on a shelf. Stillhouse
-- reports the boundary and does not decide which side of it an operator
-- should act on — that is a food-safety judgement with a cost attached,
-- and it is theirs.
--
-- "After" is by bottling date against the earliest affected gauge into
-- that container. A run that bottled before the spirit arrived cannot
-- contain it.
SELECT br.id            AS bottling_run_id,
       br.bottling_date AS bottled_on,
       br.bottle_count,
       br.voided_at     AS bottling_voided_at,
       br.source_container_id,
       bc.name          AS container_name,
       pi.id            AS packaged_inventory_id,
       pi.lot_code,
       pi.bottles_packaged,
       pi.bottles_on_hand,
       pi.bottles_removed,
       p.name           AS product_name
FROM bottling_runs br
JOIN bulk_containers bc        ON bc.id = br.source_container_id
LEFT JOIN packaged_inventory pi ON pi.bottling_run_id = br.id
LEFT JOIN products p           ON p.id = pi.product_id
WHERE br.source_container_id = ANY(@container_ids::uuid[])
  AND br.bottling_date >= @earliest::date
ORDER BY br.bottling_date, br.id;

-- name: RecallRemovalsForPackagedLots :many
-- One down: every removal of an affected packaged lot, and who received
-- it. This is the list a recall notice is written from.
--
-- Voided removals are excluded — the stock did not leave — but a voided
-- removal is not the same as one that never happened, so the caller is
-- told the count separately rather than the rows just being absent.
SELECT r.id,
       r.removal_date,
       r.bottles_removed,
       r.destination_name,
       r.voided_at,
       COALESCE(c.name, '')::text AS customer_name,
       COALESCE(c.id::text, '')::text AS customer_id,
       pi.lot_code,
       pi.id AS packaged_inventory_id
FROM packaging_removals r
JOIN packaged_inventory pi ON pi.id = r.packaged_inventory_id
LEFT JOIN customers c      ON c.id = r.customer_id
WHERE r.packaged_inventory_id = ANY(@packaged_inventory_ids::uuid[])
ORDER BY r.removal_date, r.id;

-- name: RecallContainersFedBy :many
-- Every container the given one reached, following the movement graph
-- forward. PLAN I5.
--
-- Recursive because spirit does not move once. A cask is dumped into a
-- vatting tank, the vatting tank feeds a bottling tank, and a walk that
-- followed one hop would under-recall — which is the failure that leaves
-- affected stock on a shelf. An unbounded walk over-recalls, so it is
-- capped and the depth reached is reported: an operator told "followed 3
-- moves" can judge whether that is the whole story, and one told nothing
-- cannot.
--
-- Only movements at or after the origin date count. Spirit that left a
-- tank before the affected spirit arrived did not carry it.
WITH RECURSIVE reached AS (
    SELECT m.destination_container_id AS container_id, 1 AS depth
    FROM bulk_movements m
    WHERE m.source_container_id = @container_id
      AND m.destination_container_id IS NOT NULL
      AND m.occurred_at >= @since::timestamptz
    UNION
    SELECT m.destination_container_id, r.depth + 1
    FROM bulk_movements m
    JOIN reached r ON r.container_id = m.source_container_id
    WHERE m.destination_container_id IS NOT NULL
      AND m.occurred_at >= @since::timestamptz
      AND r.depth < 10
)
SELECT r.container_id::uuid AS container_id,
       MIN(r.depth)::int AS depth,
       COALESCE(c.name, '')::text AS container_name
FROM reached r
LEFT JOIN bulk_containers c ON c.id = r.container_id
GROUP BY r.container_id, c.name
ORDER BY MIN(r.depth), c.name;

-- name: RecallPackagedLotOneDown :many
-- Who received a specific packaged lot. Exact, not possible contact: the
-- lot code IS the thing being recalled, so there is no inference here at
-- all — which is why a consumer complaint naming a lot code is the
-- easiest recall to answer and the most common one to get.
SELECT r.id, r.removal_date, r.bottles_removed, r.destination_name,
       r.voided_at,
       COALESCE(c.name, '')::text AS customer_name,
       COALESCE(c.id::text, '')::text AS customer_id,
       pi.lot_code, pi.id AS packaged_inventory_id,
       pi.bottles_on_hand
FROM packaging_removals r
JOIN packaged_inventory pi ON pi.id = r.packaged_inventory_id
LEFT JOIN customers c      ON c.id = r.customer_id
WHERE pi.id = @packaged_inventory_id
ORDER BY r.removal_date, r.id;

-- name: RecallPackagedLotsFromContainerSet :many
-- Bottling runs drawing from any of a set of containers, on or after the
-- date affected spirit could have been in them.
SELECT br.id            AS bottling_run_id,
       br.bottling_date AS bottled_on,
       br.bottle_count,
       br.voided_at     AS bottling_voided_at,
       br.source_container_id,
       bc.name          AS container_name,
       pi.id            AS packaged_inventory_id,
       pi.lot_code,
       pi.bottles_packaged,
       pi.bottles_on_hand,
       pi.bottles_removed,
       p.name           AS product_name
FROM bottling_runs br
JOIN bulk_containers bc        ON bc.id = br.source_container_id
LEFT JOIN packaged_inventory pi ON pi.bottling_run_id = br.id
LEFT JOIN products p           ON p.id = pi.product_id
WHERE br.source_container_id = ANY(@container_ids::uuid[])
  AND br.bottling_date >= @earliest::date
ORDER BY br.bottling_date, br.id;
