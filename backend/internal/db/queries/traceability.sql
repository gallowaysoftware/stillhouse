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

-- name: DistillationChainFromGauge :one
-- Pull the distillation run + first ferment + first mash + recipe behind
-- a production_gauge bulk_movement. Returns the "earliest origin" row;
-- real chains may fan out and require iterating per-charge.
SELECT dr.id AS distillation_run_id, dr.run_no AS distillation_run_no, dr.still_label,
       fr.id AS fermentation_run_id, fr.fermenter_label,
       fr.yeast_lot_id AS yeast_lot_id,
       yml.supplier_lot AS yeast_supplier_lot,
       ym.name          AS yeast_material_name,
       mr.id AS mash_run_id, mr.mash_no, mr.mash_date,
       rv.id AS recipe_version_id, rv.version_no AS recipe_version_no,
       r.id  AS recipe_id, r.name AS recipe_name
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
ORDER BY dc.charge_order
LIMIT 1;

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
