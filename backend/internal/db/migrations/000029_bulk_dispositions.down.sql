ALTER TABLE bulk_movements
    DROP COLUMN IF EXISTS bottles_unpackaged,
    DROP COLUMN IF EXISTS packaged_inventory_id,
    DROP COLUMN IF EXISTS recorded_by,
    DROP COLUMN IF EXISTS temperature_instrument_id,
    DROP COLUMN IF EXISTS strength_instrument_id,
    DROP COLUMN IF EXISTS volume_instrument_id,
    DROP COLUMN IF EXISTS strength_source,
    DROP COLUMN IF EXISTS volume_factor_c,
    DROP COLUMN IF EXISTS observed_density_kg_m3,
    DROP COLUMN IF EXISTS observed_volume_l,
    DROP COLUMN IF EXISTS temperature_c,
    DROP COLUMN IF EXISTS document_reference,
    DROP COLUMN IF EXISTS counterparty_licence_no,
    DROP COLUMN IF EXISTS counterparty_name;
-- The bulk_movement_reason values stay: PostgreSQL cannot drop an enum
-- value, and rows may already carry them.
