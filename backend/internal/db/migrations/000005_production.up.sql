-- 000005_production: operational capture for mash + fermentation runs.
--
-- These are pre-alcohol records. They do NOT feed B266 (the bulk alcohol
-- ledger starts at the production gauge in a later stage). They exist so
-- the distiller can plan, dogfood the recipe, and compare projected vs
-- actual yield once distillation captures real LAA.

CREATE TYPE mash_status AS ENUM (
    'planned',
    'in_progress',
    'fermenting',     -- handed off to one or more fermentation runs
    'distilled',      -- consumed by distillation (Stage 4)
    'cancelled'
);

CREATE TYPE mash_metric_kind AS ENUM (
    'original_gravity',     -- specific gravity, e.g. 1.072
    'mash_ph',
    'mash_temp_c',
    'water_volume_l',
    'strike_temp_c',
    'other'
);

CREATE TABLE mash_runs (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    recipe_version_id   UUID NOT NULL REFERENCES recipe_versions(id) ON DELETE RESTRICT,
    mash_no             INTEGER NOT NULL,
    mash_date           DATE NOT NULL,
    status              mash_status NOT NULL DEFAULT 'planned',
    notes               TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, mash_no)
);

CREATE INDEX mash_runs_tenant_date_idx ON mash_runs (tenant_id, mash_date DESC);
CREATE INDEX mash_runs_recipe_version_idx ON mash_runs (recipe_version_id);

CREATE TRIGGER mash_runs_updated_at
    BEFORE UPDATE ON mash_runs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE mash_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE mash_runs FORCE  ROW LEVEL SECURITY;
CREATE POLICY mash_runs_tenant_isolation ON mash_runs
    FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- mash_ingredient_usage: actual quantities consumed (vs. recipe expectations).
-- v1 references material directly; lot-level deduction is a later refinement.
CREATE TABLE mash_ingredient_usage (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    mash_run_id     UUID NOT NULL REFERENCES mash_runs(id) ON DELETE CASCADE,
    material_id     UUID NOT NULL REFERENCES materials(id) ON DELETE RESTRICT,
    quantity_used   DOUBLE PRECISION NOT NULL CHECK (quantity_used > 0),
    uom             TEXT NOT NULL,
    notes           TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (mash_run_id, material_id)
);

CREATE INDEX mash_ingredient_usage_mash_idx ON mash_ingredient_usage (mash_run_id);

ALTER TABLE mash_ingredient_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE mash_ingredient_usage FORCE  ROW LEVEL SECURITY;
CREATE POLICY mash_ingredient_usage_tenant ON mash_ingredient_usage
    FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- mash_metrics: time-series observations on a mash run.
CREATE TABLE mash_metrics (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    mash_run_id     UUID NOT NULL REFERENCES mash_runs(id) ON DELETE CASCADE,
    kind            mash_metric_kind NOT NULL,
    value           DOUBLE PRECISION NOT NULL,
    unit            TEXT NOT NULL DEFAULT '',
    observed_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    notes           TEXT NOT NULL DEFAULT ''
);

CREATE INDEX mash_metrics_mash_observed_idx ON mash_metrics (mash_run_id, observed_at);

ALTER TABLE mash_metrics ENABLE ROW LEVEL SECURITY;
ALTER TABLE mash_metrics FORCE  ROW LEVEL SECURITY;
CREATE POLICY mash_metrics_tenant ON mash_metrics
    FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- ------------------------------------------------------------------------
-- fermentation_runs: one row per fermenter that receives some of the mash.
-- Multiple ferment runs can share a mash if the mash is split.
-- ------------------------------------------------------------------------
CREATE TYPE fermentation_status AS ENUM (
    'pitched',
    'active',
    'finished',
    'distilled',
    'cancelled'
);

CREATE TABLE fermentation_runs (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    mash_run_id         UUID NOT NULL REFERENCES mash_runs(id) ON DELETE RESTRICT,
    fermenter_label     TEXT NOT NULL,                   -- e.g. "Fermenter A"
    yeast_material_id   UUID REFERENCES materials(id),   -- optional FK to a yeast material
    yeast_notes         TEXT NOT NULL DEFAULT '',
    pitch_at            TIMESTAMPTZ NOT NULL,
    target_final_gravity DOUBLE PRECISION,
    initial_volume_l    DOUBLE PRECISION,
    status              fermentation_status NOT NULL DEFAULT 'pitched',
    notes               TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX fermentation_runs_mash_idx ON fermentation_runs (mash_run_id);
CREATE INDEX fermentation_runs_tenant_status_idx ON fermentation_runs (tenant_id, status);

CREATE TRIGGER fermentation_runs_updated_at
    BEFORE UPDATE ON fermentation_runs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE fermentation_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE fermentation_runs FORCE  ROW LEVEL SECURITY;
CREATE POLICY fermentation_runs_tenant ON fermentation_runs
    FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- fermentation_logs: gravity / pH / temp readings over the fermentation lifetime.
CREATE TABLE fermentation_logs (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    fermentation_run_id     UUID NOT NULL REFERENCES fermentation_runs(id) ON DELETE CASCADE,
    observed_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    specific_gravity        DOUBLE PRECISION,
    ph                      DOUBLE PRECISION,
    temperature_c           DOUBLE PRECISION,
    notes                   TEXT NOT NULL DEFAULT ''
);

CREATE INDEX fermentation_logs_run_observed_idx
    ON fermentation_logs (fermentation_run_id, observed_at);

ALTER TABLE fermentation_logs ENABLE ROW LEVEL SECURITY;
ALTER TABLE fermentation_logs FORCE  ROW LEVEL SECURITY;
CREATE POLICY fermentation_logs_tenant ON fermentation_logs
    FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));
