-- 000009_removals_b266: duty-paid removals from the excise warehouse +
-- the snapshot frozen when a B266 monthly return is submitted.
--
-- A removal is what crystallizes duty in Canada: packaged spirits leaving
-- the excise warehouse to a duty-paid destination. Stillhouse computes
-- duty at removal time using the product's target ABV and the rate in
-- effect on the removal date.
--
-- B266 itself is a *derived view* over bulk_movements + bottling_runs +
-- packaged_inventory + packaging_removals for a tenant/period. We only
-- persist the snapshot in b266_periods once the operator marks the
-- period submitted, so the values are frozen for audit purposes.

CREATE TYPE removal_destination_kind AS ENUM (
    'duty_paid_customer',     -- normal sale to a Canadian buyer
    'export',                 -- duty-relief, ex-Canada
    'sample',
    'destroyed',
    'transfer_out_in_bond',   -- to another licensee (not duty paid)
    'other'
);

CREATE TABLE packaging_removals (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    removal_no              INTEGER NOT NULL,
    packaged_inventory_id   UUID NOT NULL REFERENCES packaged_inventory(id) ON DELETE RESTRICT,
    removal_date            DATE NOT NULL,
    bottles_removed         INTEGER NOT NULL CHECK (bottles_removed > 0),
    destination_kind        removal_destination_kind NOT NULL DEFAULT 'duty_paid_customer',
    destination_name        TEXT NOT NULL DEFAULT '',
    reference               TEXT NOT NULL DEFAULT '',          -- BOL / invoice / etc.
    -- Derived at insert time and frozen.
    bottle_size_ml          INTEGER NOT NULL,
    bottle_abv_pct          DOUBLE PRECISION NOT NULL,
    total_litres            DOUBLE PRECISION NOT NULL,
    total_laa               DOUBLE PRECISION NOT NULL,
    duty_rate_per_laa       DOUBLE PRECISION NOT NULL,         -- $/LAA in effect on removal_date
    duty_amount_cad         DOUBLE PRECISION NOT NULL,
    notes                   TEXT NOT NULL DEFAULT '',
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, removal_no)
);

CREATE INDEX packaging_removals_tenant_date_idx
    ON packaging_removals (tenant_id, removal_date DESC);
CREATE INDEX packaging_removals_packaged_idx
    ON packaging_removals (packaged_inventory_id);

ALTER TABLE packaging_removals ENABLE ROW LEVEL SECURITY;
ALTER TABLE packaging_removals FORCE  ROW LEVEL SECURITY;
CREATE POLICY packaging_removals_tenant ON packaging_removals FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- ------------------------------------------------------------------------
-- b266_periods: a frozen snapshot of a submitted B266 return.
-- Computed values land in `snapshot` as JSONB so the schema doesn't need
-- to mirror the line items exactly (which may evolve with CRA changes).
-- ------------------------------------------------------------------------
CREATE TYPE b266_status AS ENUM (
    'draft',
    'submitted'
);

CREATE TABLE b266_periods (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    period_start    DATE NOT NULL,
    period_end      DATE NOT NULL,
    status          b266_status NOT NULL DEFAULT 'draft',
    snapshot        JSONB,                          -- populated on submit
    submitted_at    TIMESTAMPTZ,
    submitted_by    UUID REFERENCES users(id),
    notes           TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, period_start, period_end),
    CHECK (period_end >= period_start)
);

CREATE INDEX b266_periods_tenant_idx ON b266_periods (tenant_id, period_start DESC);

CREATE TRIGGER b266_periods_updated_at
    BEFORE UPDATE ON b266_periods
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE b266_periods ENABLE ROW LEVEL SECURITY;
ALTER TABLE b266_periods FORCE  ROW LEVEL SECURITY;
CREATE POLICY b266_periods_tenant ON b266_periods FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));
