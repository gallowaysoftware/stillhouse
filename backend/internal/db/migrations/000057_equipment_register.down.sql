-- alert_kind keeps 'equipment_service_due' and 'equipment_down'; Postgres
-- cannot remove an enum value without rewriting the type, and a kind
-- nothing raises costs nothing.
ALTER TABLE work_orders        DROP COLUMN IF EXISTS equipment_id;
ALTER TABLE bottling_runs      DROP COLUMN IF EXISTS equipment_id;
ALTER TABLE mash_runs          DROP COLUMN IF EXISTS equipment_id;
ALTER TABLE distillation_runs  DROP COLUMN IF EXISTS equipment_id;
DROP TABLE IF EXISTS equipment_service_events;
DROP TABLE IF EXISTS equipment;
DROP TYPE IF EXISTS equipment_status;
DROP TYPE IF EXISTS equipment_kind;
