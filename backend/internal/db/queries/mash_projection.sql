-- name: SumGaugeLAAForMash :one
-- Alcohol gauged out of the still runs this mash went into. A run charged
-- from several mashes is shared between them by the alcohol each put in, or
-- by volume unless every charge has a strength: zero is how an unknown one
-- is stored, and reading it as none would give that mash nothing. Each gauge counts once however
-- many charges fed it, and a voided run counts not at all.
SELECT COALESCE(SUM(pg.laa * CASE
           WHEN c.weakest > 0 THEN c.mash_laa / c.total_laa
           WHEN c.total_l   > 0 THEN c.mash_l   / c.total_l
           ELSE 0 END), 0)::double precision AS total_gauge_laa
FROM production_gauges pg
JOIN distillation_runs dr ON dr.id = pg.distillation_run_id AND dr.voided_at IS NULL
JOIN LATERAL (
    SELECT COALESCE(MIN(dc.abv_pct), 0)                                                              AS weakest,
           COALESCE(SUM(dc.volume_charged_l * dc.abv_pct), 0)                                        AS total_laa,
           COALESCE(SUM(dc.volume_charged_l * dc.abv_pct) FILTER (WHERE fr.mash_run_id = $1), 0)     AS mash_laa,
           COALESCE(SUM(dc.volume_charged_l), 0)                                                     AS total_l,
           COALESCE(SUM(dc.volume_charged_l) FILTER (WHERE fr.mash_run_id = $1), 0)                  AS mash_l
    FROM distillation_charges dc
    JOIN fermentation_runs fr ON fr.id = dc.fermentation_run_id
    WHERE dc.distillation_run_id = dr.id
) c ON TRUE;
