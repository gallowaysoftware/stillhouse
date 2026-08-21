package server

// The supporting schedules in an audit binder.
//
// Written as SQL here rather than as sqlc queries because they are a dump,
// not a projection: what belongs in a binder is the rows as they stand,
// and a query that shaped them would be one more thing between the ledger
// and the evidence.
//
// Every one of them resolves ids to names. An auditor reading
// `container_id = 3f2a8c1e-…` learns nothing; `Spirit Receiver #2` is the
// same fact in a form a human can check against the tank in front of them.
//
// $1 is the period start and $2 the exclusive end.

type binderTable struct {
	// file is the name inside the zip. Numbered so a reviewer reading the
	// directory top to bottom follows the chain: what was filed, then what
	// is behind each line, then what determined it, then who did it.
	file string
	// title is what the same schedule is called in binder.html.
	title string
	// why says what this schedule is evidence of. It goes in README.txt,
	// because a folder of CSVs with no explanation is not a binder.
	why string
	// standing marks a schedule that is not bounded by the period. The
	// instrument register is one: an instrument that determined a figure
	// in this period belongs in the binder whether or not anything about
	// it changed during it.
	standing bool
	sql      string
}

var binderTables = []binderTable{
	{
		file:  "02-bulk-movements.csv",
		title: "Bulk movements",
		why:   "Every movement of bulk spirits in the period, with the determination behind it and the instruments that made it. This is what each line of page 3 is built from.",
		sql: `
SELECT bm.occurred_at,
       bm.reason::text                                   AS reason,
       COALESCE(src.name, '')                            AS source_container,
       COALESCE(dst.name, '')                            AS destination_container,
       bm.volume_l, bm.abv_pct, bm.laa,
       bm.observed_volume_l, bm.observed_density_kg_m3, bm.temperature_c,
       bm.volume_factor_c,
       bm.strength_source::text                          AS strength_determined_by,
       COALESCE(vi.label || ' (' || vi.serial_no || ')', '')  AS volume_instrument,
       COALESCE(si.label || ' (' || si.serial_no || ')', '')  AS strength_instrument,
       COALESCE(ti.label || ' (' || ti.serial_no || ')', '')  AS temperature_instrument,
       bm.counterparty_name, bm.counterparty_licence_no, bm.document_reference,
       bm.loss_duty_treatment::text                      AS loss_duty_treatment,
       bm.loss_treatment_authority,
       COALESCE(ru.display_name, '')                     AS recorded_by,
       bm.reference_type, bm.notes
FROM bulk_movements bm
LEFT JOIN bulk_containers src ON src.id = bm.source_container_id
LEFT JOIN bulk_containers dst ON dst.id = bm.destination_container_id
LEFT JOIN instruments vi      ON vi.id  = bm.volume_instrument_id
LEFT JOIN instruments si      ON si.id  = bm.strength_instrument_id
LEFT JOIN instruments ti      ON ti.id  = bm.temperature_instrument_id
LEFT JOIN users ru            ON ru.id  = bm.recorded_by
WHERE bm.occurred_at >= $1 AND bm.occurred_at < $2
ORDER BY bm.occurred_at, bm.created_at`,
	},
	{
		file:  "03-production-gauges.csv",
		title: "Production gauges",
		why:   "The determinations that put new alcohol into the ledger, and the approved instruments that made them (EDM3-1-1 ¶24, EDM1-1-5).",
		sql: `
SELECT pg.gauge_date,
       dr.run_no                                         AS distillation_run,
       c.name                                            AS destination_container,
       pg.volume_l, pg.abv_pct, pg.laa,
       pg.observed_volume_l, pg.observed_density_kg_m3, pg.temperature_c,
       pg.volume_factor_c,
       pg.strength_source::text                          AS strength_determined_by,
       COALESCE(vi.label || ' (' || vi.serial_no || ')', '')  AS volume_instrument,
       COALESCE(si.label || ' (' || si.serial_no || ')', '')  AS strength_instrument,
       COALESCE(ti.label || ' (' || ti.serial_no || ')', '')  AS temperature_instrument,
       COALESCE(u.display_name, '')                      AS gauged_by,
       pg.notes
FROM production_gauges pg
JOIN bulk_containers c        ON c.id  = pg.destination_container_id
LEFT JOIN distillation_runs dr ON dr.id = pg.distillation_run_id
LEFT JOIN instruments vi      ON vi.id = pg.volume_instrument_id
LEFT JOIN instruments si      ON si.id = pg.strength_instrument_id
LEFT JOIN instruments ti      ON ti.id = pg.temperature_instrument_id
LEFT JOIN users u             ON u.id  = pg.gauger_user_id
WHERE pg.gauge_date >= $1 AND pg.gauge_date < $2
ORDER BY pg.gauge_date`,
	},
	{
		file:  "04-barrel-events.csv",
		title: "Barrel events",
		why:   "Fills, dumps and regauges. A regauge is where the angels' share is measured, so these are the determinations behind the evaporation losses on the return.",
		sql: `
SELECT be.event_date,
       be.kind::text                                     AS event,
       c.name                                            AS barrel,
       be.volume_l, be.abv_pct, be.laa,
       be.observed_volume_l, be.observed_density_kg_m3, be.temperature_c,
       be.volume_factor_c,
       be.strength_source::text                          AS strength_determined_by,
       COALESCE(vi.label || ' (' || vi.serial_no || ')', '')  AS volume_instrument,
       COALESCE(si.label || ' (' || si.serial_no || ')', '')  AS strength_instrument,
       COALESCE(ti.label || ' (' || ti.serial_no || ')', '')  AS temperature_instrument,
       COALESCE(u.display_name, '')                      AS recorded_by,
       be.location_after, be.notes,
       be.voided_at, be.voided_reason
FROM barrel_events be
JOIN bulk_containers c   ON c.id  = be.container_id
LEFT JOIN instruments vi ON vi.id = be.volume_instrument_id
LEFT JOIN instruments si ON si.id = be.strength_instrument_id
LEFT JOIN instruments ti ON ti.id = be.temperature_instrument_id
LEFT JOIN users u        ON u.id  = be.user_id
WHERE be.event_date >= $1 AND be.event_date < $2
ORDER BY be.event_date`,
	},
	{
		file:  "05-bottling-runs.csv",
		title: "Bottling runs",
		why:   "What was packaged, what it drew from bulk, and — for a licensee whose duty point is at packaging — the duty event itself.",
		sql: `
SELECT br.bottling_date, br.run_no,
       p.name                                            AS product,
       p.bottle_size_ml, p.target_abv_pct,
       c.name                                            AS source_container,
       br.lot_code, br.destination_jurisdiction,
       br.bottle_count, br.bottling_loss_l,
       br.tank_gauge_volume_l, br.tank_gauge_abv_pct, br.tank_gauge_laa,
       br.duty_rate_per_laa, br.duty_amount_cad, br.duty_rate_source,
       br.notes, br.voided_at, br.voided_reason
FROM bottling_runs br
JOIN products p        ON p.id = br.product_id
JOIN bulk_containers c ON c.id = br.source_container_id
WHERE br.bottling_date >= $1 AND br.bottling_date < $2
ORDER BY br.bottling_date, br.run_no`,
	},
	{
		file:  "06-removals.csv",
		title: "Removals",
		why:   "Packaged spirits leaving, with the duty each carried and the rate it was charged at.",
		sql: `
SELECT pr.removal_date, pr.removal_no,
       p.name                                            AS product,
       pi.lot_code, pi.jurisdiction,
       pr.destination_kind::text                         AS destination_kind,
       pr.destination_name, pr.reference,
       pr.bottles_removed, pr.bottle_size_ml, pr.bottle_abv_pct,
       pr.total_litres, pr.total_laa,
       pr.duty_rate_per_laa, pr.duty_amount_cad,
       pr.notes, pr.voided_at, pr.voided_reason
FROM packaging_removals pr
JOIN packaged_inventory pi ON pi.id = pr.packaged_inventory_id
JOIN products p            ON p.id  = pi.product_id
WHERE pr.removal_date >= $1 AND pr.removal_date < $2
ORDER BY pr.removal_date, pr.removal_no`,
	},
	{
		file:  "07-inventory-adjustments.csv",
		title: "Inventory adjustments",
		why:   "Line D: reconciliations of book stock to physical, each with the reason, the explanation, and the person who made it.",
		sql: `
SELECT ia.occurred_at,
       c.name                                            AS container,
       ia.reason::text                                   AS reason,
       ia.explanation,
       ia.book_volume_l, ia.book_abv_pct, ia.book_laa,
       ia.counted_volume_l, ia.counted_abv_pct, ia.counted_laa,
       ia.delta_volume_l, ia.delta_laa,
       ia.temperature_c, ia.volume_factor_c,
       ia.strength_source::text                          AS strength_determined_by,
       COALESCE(si.label || ' (' || si.serial_no || ')', '')  AS strength_instrument,
       COALESCE(u.display_name, '')                      AS adjusted_by,
       ia.notes
FROM inventory_adjustments ia
JOIN bulk_containers c   ON c.id  = ia.container_id
LEFT JOIN instruments si ON si.id = ia.strength_instrument_id
JOIN users u             ON u.id  = ia.adjusted_by
WHERE ia.occurred_at >= $1 AND ia.occurred_at < $2
ORDER BY ia.occurred_at`,
	},
	{
		file:  "08-losses.csv",
		title: "Losses and destructions",
		why:   "Every loss in the period and how it is treated for duty (EDM3-4-1), with the authority any relief rests on and who ruled on it.",
		sql: `
SELECT bm.occurred_at,
       bm.reason::text                                   AS kind,
       COALESCE(src.name, '')                            AS container,
       bm.volume_l, bm.abv_pct, bm.laa,
       bm.loss_duty_treatment::text                      AS duty_treatment,
       bm.loss_treatment_authority                       AS authority_for_relief,
       COALESCE(cu.display_name, '')                     AS classified_by,
       bm.loss_classified_at,
       bm.document_reference, bm.reference_type, bm.notes
FROM bulk_movements bm
LEFT JOIN bulk_containers src ON src.id = bm.source_container_id
LEFT JOIN users cu            ON cu.id  = bm.loss_classified_by
WHERE bm.reason IN ('loss_evaporation', 'loss_unaccounted', 'destruction')
  AND bm.reference_type NOT IN ('distillation_run_void', 'bottling_run_void')
  AND bm.occurred_at >= $1 AND bm.occurred_at < $2
ORDER BY bm.occurred_at`,
	},
	{
		file:     "09-instruments.csv",
		title:    "Instrument register",
		why:      "The register as it stands, with each instrument's CRA approval and its last calibration. EDM1-1-5 requires each individual instrument to be approved, so the schedules above are only as good as this one.",
		standing: true,
		sql: `
SELECT i.kind::text AS kind, i.label, i.manufacturer, i.model, i.serial_no,
       i.approval_reference, i.approval_date, i.approval_expires_on,
       i.status::text AS status, i.status_reason,
       i.calibration_interval_days,
       lc.calibrated_on   AS last_calibrated_on,
       lc.performed_by    AS last_calibration_by,
       lc.certificate_ref AS last_calibration_certificate,
       i.notes
FROM instruments i
LEFT JOIN LATERAL (
    SELECT calibrated_on, performed_by, certificate_ref
    FROM instrument_calibrations ic
    WHERE ic.instrument_id = i.id AND ic.passed
    ORDER BY ic.calibrated_on DESC, ic.created_at DESC
    LIMIT 1
) lc ON TRUE
ORDER BY i.kind, i.label`,
	},
	{
		file:  "10-audit-trail.csv",
		title: "Audit trail",
		why:   "Who did what, and when. Every entry in the period, in order.",
		sql: `
SELECT ae.occurred_at,
       COALESCE(u.display_name, '(account since removed)') AS actor,
       COALESCE(u.email, '')                              AS actor_email,
       ae.action::text AS action,
       ae.entity_type, ae.entity_id,
       ae.payload
FROM audit_events ae
LEFT JOIN users u ON u.id = ae.user_id
WHERE ae.occurred_at >= $1 AND ae.occurred_at < $2
ORDER BY ae.occurred_at`,
	},
}
