-- name: SaveEquipment :one
INSERT INTO equipment (
    id, tenant_id, name, kind, status, location_id, manufacturer, model,
    serial_no, commissioned_on, capacity_l, typical_run_hours,
    service_interval_hours, service_interval_days, notes,
    retired_on, retired_reason
) VALUES (
    COALESCE(sqlc.narg(id)::uuid, gen_random_uuid()),
    $1, $2, sqlc.arg(kind)::equipment_kind, sqlc.arg(status)::equipment_status,
    $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
)
ON CONFLICT (id) DO UPDATE
SET name                   = EXCLUDED.name,
    kind                   = EXCLUDED.kind,
    status                 = EXCLUDED.status,
    location_id            = EXCLUDED.location_id,
    manufacturer           = EXCLUDED.manufacturer,
    model                  = EXCLUDED.model,
    serial_no              = EXCLUDED.serial_no,
    commissioned_on        = EXCLUDED.commissioned_on,
    capacity_l             = EXCLUDED.capacity_l,
    typical_run_hours      = EXCLUDED.typical_run_hours,
    service_interval_hours = EXCLUDED.service_interval_hours,
    service_interval_days  = EXCLUDED.service_interval_days,
    notes                  = EXCLUDED.notes,
    retired_on             = EXCLUDED.retired_on,
    retired_reason         = EXCLUDED.retired_reason,
    updated_at             = NOW()
RETURNING *;

-- name: GetEquipment :one
SELECT e.*, COALESCE(l.name, '') AS location_name
FROM equipment e
LEFT JOIN locations l ON l.id = e.location_id
WHERE e.id = $1;

-- name: ListEquipment :many
-- Each item with its last service and the runs it has carried, so the
-- register answers "when was this last looked at" without a second call.
SELECT e.*, COALESCE(l.name, '') AS location_name,
       last.performed_on AS last_serviced_on,
       COALESCE(runs.n, 0)::int AS run_count
FROM equipment e
LEFT JOIN locations l ON l.id = e.location_id
LEFT JOIN LATERAL (
    SELECT s.performed_on FROM equipment_service_events s
    WHERE s.equipment_id = e.id
    ORDER BY s.performed_on DESC, s.created_at DESC LIMIT 1
) last ON TRUE
LEFT JOIN LATERAL (
    SELECT COUNT(*) AS n FROM distillation_runs d WHERE d.equipment_id = e.id
) runs ON TRUE
WHERE (sqlc.arg(include_retired)::boolean OR e.status <> 'retired')
ORDER BY e.status, e.kind, e.name;

-- name: DeleteEquipment :exec
DELETE FROM equipment WHERE id = $1;

-- name: RecordEquipmentService :one
INSERT INTO equipment_service_events (
    tenant_id, equipment_id, performed_on, description, performed_by,
    hours_at_service, cost_cad, work_order_id, notes, recorded_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: ListEquipmentService :many
SELECT * FROM equipment_service_events
WHERE equipment_id = $1
ORDER BY performed_on DESC, created_at DESC;

-- name: EquipmentServiceDue :many
-- Items whose recorded interval has elapsed since their last service, or
-- which have never been serviced at all.
--
-- Only items with an interval recorded. One without is never due, because
-- a service schedule Stillhouse invented is a schedule nobody agreed to —
-- and the register shows plainly that no interval is set.
SELECT e.id, e.name, e.kind, e.service_interval_days,
       last.performed_on AS last_serviced_on,
       (CURRENT_DATE - COALESCE(last.performed_on, e.commissioned_on,
                                e.created_at::date))::int AS days_since
FROM equipment e
LEFT JOIN LATERAL (
    SELECT s.performed_on FROM equipment_service_events s
    WHERE s.equipment_id = e.id
    ORDER BY s.performed_on DESC LIMIT 1
) last ON TRUE
WHERE e.status <> 'retired'
  AND e.service_interval_days IS NOT NULL
  AND (CURRENT_DATE - COALESCE(last.performed_on, e.commissioned_on,
                               e.created_at::date)) >= e.service_interval_days
ORDER BY days_since DESC;

-- name: EquipmentDown :many
SELECT id, name, kind FROM equipment WHERE status = 'down' ORDER BY name;

-- name: SetDistillationEquipment :exec
UPDATE distillation_runs SET equipment_id = $2 WHERE id = $1;

-- name: EquipmentRunDurations :many
-- What runs on this actually took, from the work orders that recorded a
-- start and a finish. The estimate F3 will need comes from here rather
-- than from a number somebody typed once.
SELECT w.id, w.started_at, w.completed_at,
       EXTRACT(EPOCH FROM (w.completed_at - w.started_at)) / 3600.0 AS hours
FROM work_orders w
WHERE w.equipment_id = $1
  AND w.started_at IS NOT NULL
  AND w.completed_at IS NOT NULL
ORDER BY w.completed_at DESC
LIMIT 50;
