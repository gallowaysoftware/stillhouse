-- 000002_materials: raw materials master + receipt lots.
--
-- Tenant-scoped via RLS on tenant_id. Decimal quantities are stored as
-- double precision (float8) for v1 — sufficient for grain weights, water
-- volumes, and fermentable-yield ratios. We'll move to NUMERIC when we
-- start tracking excise duty in Stage 7.

CREATE TYPE material_kind AS ENUM (
    'grain',
    'malt',
    'yeast',
    'water',
    'ngs',         -- neutral grain spirit
    'botanical',
    'packaging',
    'other'
);

CREATE TABLE materials (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    kind            material_kind NOT NULL,
    uom             TEXT NOT NULL,                  -- 'kg' | 'L' | 'each' | …
    supplier        TEXT NOT NULL DEFAULT '',
    notes           TEXT NOT NULL DEFAULT '',
    -- Fermentable-source parameters (populated for kind in ('grain','malt')).
    -- Fractions in [0,1]. Null for non-fermentable kinds.
    extract_pct     DOUBLE PRECISION,
    moisture_pct    DOUBLE PRECISION,
    archived        BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);

CREATE INDEX materials_tenant_kind_idx ON materials (tenant_id, kind) WHERE NOT archived;

CREATE TRIGGER materials_updated_at
    BEFORE UPDATE ON materials
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE materials ENABLE ROW LEVEL SECURITY;
CREATE POLICY materials_tenant_isolation ON materials
    FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- ------------------------------------------------------------------------
-- material_lots: one row per receiving event.
-- ------------------------------------------------------------------------
CREATE TABLE material_lots (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id          UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    material_id        UUID NOT NULL REFERENCES materials(id) ON DELETE RESTRICT,
    supplier_lot       TEXT NOT NULL DEFAULT '',
    quantity_received  DOUBLE PRECISION NOT NULL,
    quantity_on_hand   DOUBLE PRECISION NOT NULL,
    received_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    notes              TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (quantity_received >= 0),
    CHECK (quantity_on_hand >= 0)
);

CREATE INDEX material_lots_tenant_material_idx
    ON material_lots (tenant_id, material_id, received_at DESC);

CREATE TRIGGER material_lots_updated_at
    BEFORE UPDATE ON material_lots
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE material_lots ENABLE ROW LEVEL SECURITY;
CREATE POLICY material_lots_tenant_isolation ON material_lots
    FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));
