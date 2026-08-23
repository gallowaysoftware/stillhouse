-- 000066_forecasting: demand forecasting, kept visibly apart from demand.
-- PLAN F7.
--
-- Stage 185 built the production plan from ACTUAL demand — confirmed,
-- unshipped order lines — and says so on the page every time, "because a
-- plan built on an invented forecast looks exactly as authoritative as
-- one built on orders". That sentence is the whole constraint on this
-- migration.
--
-- So a forecast never joins the demand figure. It is computed separately,
-- carries the method that produced it, and is reported beside the orders
-- rather than added to them. A single number combining twelve bottles
-- somebody has paid for with forty somebody might buy is worse than
-- having no forecast at all, because nobody can take it apart again.
--
-- And the method is the licensee's, not ours. A trailing average and a
-- seasonal naive disagree, sometimes by a lot, and which is right depends
-- on whether this distillery's sales are trending or seasonal — a thing
-- Stillhouse cannot see and the operator can. Unset means refuse, exactly
-- as the WIP charge basis does (000061) and the chart of accounts does
-- (000040).

CREATE TYPE forecast_method AS ENUM (
    -- The mean of the last N complete months of actual removals.
    -- Good where sales are steady, wrong where they are seasonal.
    'trailing_average',
    -- The same month last year. Good where sales are seasonal, wrong in
    -- the first year and wrong after a step change.
    'same_period_last_year',
    -- The operator's own numbers. Not a worse answer — for a distillery
    -- with a listing decision or a festival in the diary it is the only
    -- one that can be right.
    'manual'
);

-- NULL means unset, and unset means refuse. Deliberately not defaulted:
-- picking a method here would be Stillhouse choosing and then reporting
-- the result as though the licensee had.
ALTER TABLE tenants ADD COLUMN forecast_method forecast_method;
ALTER TABLE tenants ADD COLUMN forecast_trailing_months INTEGER NOT NULL DEFAULT 3
    CHECK (forecast_trailing_months BETWEEN 1 AND 24);

COMMENT ON COLUMN tenants.forecast_method IS
    'How demand is projected. NULL means the licensee has not chosen one and forecasts are refused rather than guessed.';

-- The manual method's numbers, and an override for the others: a
-- forecast the operator has replaced by hand beats a computed one, and
-- the reason is kept so the next person knows why.
CREATE TABLE demand_forecasts (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    product_id   UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    period_start DATE NOT NULL,
    period_end   DATE NOT NULL,
    bottles      INTEGER NOT NULL CHECK (bottles >= 0),
    -- Why this number rather than the computed one. Free text and
    -- required in the handler, because "somebody typed 400" is not a
    -- basis anybody can check later.
    reason       TEXT NOT NULL DEFAULT '',
    created_by   UUID REFERENCES users(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, product_id, period_start, period_end),
    CHECK (period_end >= period_start)
);

CREATE INDEX demand_forecasts_tenant_idx ON demand_forecasts (tenant_id, period_start DESC);

CREATE TRIGGER demand_forecasts_updated_at
    BEFORE UPDATE ON demand_forecasts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE demand_forecasts ENABLE ROW LEVEL SECURITY;
ALTER TABLE demand_forecasts FORCE  ROW LEVEL SECURITY;
CREATE POLICY demand_forecasts_tenant ON demand_forecasts FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));
