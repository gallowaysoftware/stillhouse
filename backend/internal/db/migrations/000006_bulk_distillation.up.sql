-- 000006_bulk_distillation: the bridge from production to the compliance
-- spine. The production gauge of a distillation run is the moment alcohol
-- first exists for tracking purposes; everything after this stage measures
-- in LAA and feeds (eventually) CRA Form B266.
--
-- Two ledgers:
--   * bulk_containers — current per-tank inventory (vol L, ABV %, LAA),
--     maintained by the application inside each tx.
--   * bulk_movements  — the append-only ledger of every alcohol movement.
--
-- Stills are modelled as a free-text label on distillation_runs for v1; a
-- proper Still entity comes later when there's enough scheduling/cleaning
-- workflow to justify it.

CREATE TYPE bulk_container_kind AS ENUM (
    'spirit_receiver',
    'tank',
    'ibc',
    'tote',
    'blend_tank',
    'bottling_tank',
    'other'
);

CREATE TABLE bulk_containers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    kind            bulk_container_kind NOT NULL DEFAULT 'tank',
    capacity_l      DOUBLE PRECISION,
    location        TEXT NOT NULL DEFAULT '',
    notes           TEXT NOT NULL DEFAULT '',
    archived        BOOLEAN NOT NULL DEFAULT FALSE,
    -- Running balance maintained by the application on every movement.
    current_volume_l DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (current_volume_l >= 0),
    current_abv_pct  DOUBLE PRECISION,                              -- NULL when empty
    current_laa      DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (current_laa >= 0),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);

CREATE INDEX bulk_containers_tenant_idx ON bulk_containers (tenant_id) WHERE NOT archived;

CREATE TRIGGER bulk_containers_updated_at
    BEFORE UPDATE ON bulk_containers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE bulk_containers ENABLE ROW LEVEL SECURITY;
ALTER TABLE bulk_containers FORCE  ROW LEVEL SECURITY;
CREATE POLICY bulk_containers_tenant ON bulk_containers FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- bulk_movements: append-only ledger.
-- src/dst nullability:
--   production_gauge / transfer_in_bond / receipt   -> src NULL, dst set
--   inter_tank_transfer / blend                     -> both set
--   loss_evaporation / loss_unaccounted / transfer_out_in_bond / destruction
--                                                   -> src set, dst NULL
--   regauge_correction                              -> dst set, src NULL (positive)
--                                                       OR src set, dst NULL (negative)
CREATE TYPE bulk_movement_reason AS ENUM (
    'production_gauge',
    'inter_tank_transfer',
    'blend',
    'transfer_in_bond',
    'transfer_out_in_bond',
    'transfer_to_packaging',
    'loss_evaporation',
    'loss_unaccounted',
    'regauge_correction',
    'destruction'
);

CREATE TABLE bulk_movements (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id                   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    source_container_id         UUID REFERENCES bulk_containers(id),
    destination_container_id    UUID REFERENCES bulk_containers(id),
    volume_l                    DOUBLE PRECISION NOT NULL CHECK (volume_l > 0),
    abv_pct                     DOUBLE PRECISION NOT NULL CHECK (abv_pct >= 0 AND abv_pct <= 100),
    laa                         DOUBLE PRECISION NOT NULL CHECK (laa >= 0),
    reason                      bulk_movement_reason NOT NULL,
    reference_type              TEXT NOT NULL DEFAULT '',     -- e.g. 'production_gauge', 'bottling_run'
    reference_id                UUID,
    notes                       TEXT NOT NULL DEFAULT '',
    occurred_at                 TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (source_container_id IS NOT NULL OR destination_container_id IS NOT NULL)
);

CREATE INDEX bulk_movements_tenant_occurred_idx
    ON bulk_movements (tenant_id, occurred_at DESC);
CREATE INDEX bulk_movements_src_idx ON bulk_movements (source_container_id) WHERE source_container_id IS NOT NULL;
CREATE INDEX bulk_movements_dst_idx ON bulk_movements (destination_container_id) WHERE destination_container_id IS NOT NULL;

ALTER TABLE bulk_movements ENABLE ROW LEVEL SECURITY;
ALTER TABLE bulk_movements FORCE  ROW LEVEL SECURITY;
CREATE POLICY bulk_movements_tenant ON bulk_movements FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- ------------------------------------------------------------------------
-- distillation_runs: a single still charge → cuts → production gauge.
-- ------------------------------------------------------------------------
CREATE TYPE distillation_status AS ENUM (
    'planned',
    'charging',
    'distilling',
    'gauged',         -- production gauge recorded, alcohol in bulk ledger
    'cancelled'
);

CREATE TYPE distillation_cut_kind AS ENUM (
    'foreshots',
    'heads',
    'hearts',
    'tails',
    'feints_saved'
);

CREATE TABLE distillation_runs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    run_no          INTEGER NOT NULL,
    still_label     TEXT NOT NULL DEFAULT '',
    run_date        DATE NOT NULL,
    status          distillation_status NOT NULL DEFAULT 'planned',
    notes           TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, run_no)
);

CREATE INDEX distillation_runs_tenant_date_idx ON distillation_runs (tenant_id, run_date DESC);

CREATE TRIGGER distillation_runs_updated_at
    BEFORE UPDATE ON distillation_runs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE distillation_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE distillation_runs FORCE  ROW LEVEL SECURITY;
CREATE POLICY distillation_runs_tenant ON distillation_runs FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- distillation_charges: a distillation run can be charged from multiple
-- fermentation runs (e.g., combining two ferments to fill the still).
CREATE TABLE distillation_charges (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    distillation_run_id     UUID NOT NULL REFERENCES distillation_runs(id) ON DELETE CASCADE,
    fermentation_run_id     UUID NOT NULL REFERENCES fermentation_runs(id) ON DELETE RESTRICT,
    volume_charged_l        DOUBLE PRECISION NOT NULL CHECK (volume_charged_l > 0),
    abv_pct                 DOUBLE PRECISION NOT NULL CHECK (abv_pct >= 0 AND abv_pct <= 100),
    charge_order            INTEGER NOT NULL DEFAULT 1,
    notes                   TEXT NOT NULL DEFAULT '',
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (distillation_run_id, fermentation_run_id)
);

CREATE INDEX distillation_charges_run_idx ON distillation_charges (distillation_run_id, charge_order);

ALTER TABLE distillation_charges ENABLE ROW LEVEL SECURITY;
ALTER TABLE distillation_charges FORCE  ROW LEVEL SECURITY;
CREATE POLICY distillation_charges_tenant ON distillation_charges FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- distillation_cuts: the cut sequence within a run.
CREATE TABLE distillation_cuts (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id               UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    distillation_run_id     UUID NOT NULL REFERENCES distillation_runs(id) ON DELETE CASCADE,
    kind                    distillation_cut_kind NOT NULL,
    volume_l                DOUBLE PRECISION NOT NULL CHECK (volume_l >= 0),
    abv_pct                 DOUBLE PRECISION NOT NULL CHECK (abv_pct >= 0 AND abv_pct <= 100),
    laa                     DOUBLE PRECISION GENERATED ALWAYS AS (volume_l * abv_pct / 100) STORED,
    cut_order               INTEGER NOT NULL DEFAULT 1,
    observed_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    notes                   TEXT NOT NULL DEFAULT ''
);

CREATE INDEX distillation_cuts_run_order_idx ON distillation_cuts (distillation_run_id, cut_order);

ALTER TABLE distillation_cuts ENABLE ROW LEVEL SECURITY;
ALTER TABLE distillation_cuts FORCE  ROW LEVEL SECURITY;
CREATE POLICY distillation_cuts_tenant ON distillation_cuts FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- production_gauges: the formal volume/ABV/LAA measurement that puts new
-- alcohol into the bulk ledger. Each run has at most one gauge (enforced
-- by application logic, since a single distillation produces one new-make
-- output even if multiple cuts feed it).
CREATE TABLE production_gauges (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id                   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    distillation_run_id         UUID NOT NULL UNIQUE REFERENCES distillation_runs(id) ON DELETE CASCADE,
    destination_container_id    UUID NOT NULL REFERENCES bulk_containers(id),
    bulk_movement_id            UUID NOT NULL REFERENCES bulk_movements(id),  -- the resulting ledger row
    gauge_date                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    volume_l                    DOUBLE PRECISION NOT NULL CHECK (volume_l > 0),
    abv_pct                     DOUBLE PRECISION NOT NULL CHECK (abv_pct >= 0 AND abv_pct <= 100),
    temperature_c               DOUBLE PRECISION,
    laa                         DOUBLE PRECISION GENERATED ALWAYS AS (volume_l * abv_pct / 100) STORED,
    gauger_user_id              UUID NOT NULL REFERENCES users(id),
    notes                       TEXT NOT NULL DEFAULT '',
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX production_gauges_tenant_date_idx ON production_gauges (tenant_id, gauge_date DESC);

ALTER TABLE production_gauges ENABLE ROW LEVEL SECURITY;
ALTER TABLE production_gauges FORCE  ROW LEVEL SECURITY;
CREATE POLICY production_gauges_tenant ON production_gauges FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));
