ALTER TABLE b266_periods DROP COLUMN IF EXISTS due_on;
ALTER TABLE tenants DROP CONSTRAINT IF EXISTS tenants_fiscal_basis_needs_day;
ALTER TABLE tenants
    DROP COLUMN IF EXISTS filing_frequency_authorization_ref,
    DROP COLUMN IF EXISTS fiscal_month_notification_ref,
    DROP COLUMN IF EXISTS fiscal_month_end_day,
    DROP COLUMN IF EXISTS fiscal_month_basis,
    DROP COLUMN IF EXISTS filing_frequency;
DROP TYPE IF EXISTS fiscal_month_basis;
DROP TYPE IF EXISTS filing_frequency;
