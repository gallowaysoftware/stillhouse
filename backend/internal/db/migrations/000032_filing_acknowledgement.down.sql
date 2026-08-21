ALTER TABLE b266_periods DROP CONSTRAINT IF EXISTS b266_periods_acknowledgement_is_complete;
ALTER TABLE b266_periods
    DROP COLUMN IF EXISTS filing_acknowledgement,
    DROP COLUMN IF EXISTS filing_acknowledged_by,
    DROP COLUMN IF EXISTS filing_acknowledged_at;
