DROP TABLE IF EXISTS inventory_adjustments;
DROP TYPE IF EXISTS inventory_adjustment_reason;
-- The two bulk_movement_reason values stay: PostgreSQL cannot drop an enum
-- value, and rows may already carry them.
