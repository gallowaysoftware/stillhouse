DROP INDEX IF EXISTS barrel_events_instruments_idx;
DROP INDEX IF EXISTS production_gauges_instruments_idx;

ALTER TABLE barrel_events
    DROP COLUMN IF EXISTS temperature_instrument_id,
    DROP COLUMN IF EXISTS strength_instrument_id,
    DROP COLUMN IF EXISTS volume_instrument_id;

ALTER TABLE production_gauges
    DROP COLUMN IF EXISTS temperature_instrument_id,
    DROP COLUMN IF EXISTS strength_instrument_id,
    DROP COLUMN IF EXISTS volume_instrument_id;

DROP TABLE IF EXISTS instrument_calibrations;
DROP TABLE IF EXISTS instruments;

DROP TYPE IF EXISTS instrument_status;
DROP TYPE IF EXISTS instrument_kind;
