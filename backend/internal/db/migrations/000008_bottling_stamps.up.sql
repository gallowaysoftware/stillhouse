-- 000008_bottling_stamps: finished products, bottling, and the
-- province-coded CRA excise stamp ledger.
--
-- Every bottle of spirits sold to the duty-paid market in Canada (since
-- 1 July 2012) must bear an excise stamp keyed to the destination
-- province. Stillhouse models stamp orders as ranges (start..end serial
-- inclusive) with a running available_count, decremented inside the
-- same tx as the bottling run that applies them.

-- Spirit kind for product compositional standard. Mirrors recipe.spirit_kind
-- but bound at the product (SKU) level — a recipe might be bottled as
-- multiple SKUs (e.g., cask strength vs. proofed-down).
-- (Re-use recipe spirit_kind enum.)

CREATE TABLE products (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    spirit_kind     spirit_kind NOT NULL,
    bottle_size_ml  INTEGER NOT NULL CHECK (bottle_size_ml > 0),
    target_abv_pct  DOUBLE PRECISION NOT NULL CHECK (target_abv_pct > 0 AND target_abv_pct <= 100),
    label_notes     TEXT NOT NULL DEFAULT '',
    archived        BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);

CREATE TRIGGER products_updated_at
    BEFORE UPDATE ON products
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE products ENABLE ROW LEVEL SECURITY;
ALTER TABLE products FORCE  ROW LEVEL SECURITY;
CREATE POLICY products_tenant ON products FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- ------------------------------------------------------------------------
-- excise_stamp_orders: orders placed with CRA / the stamp producer.
-- jurisdiction is a Canadian subdivision code (ISO 3166-2): CA-ON, CA-QC, etc.
-- ------------------------------------------------------------------------
CREATE TYPE excise_stamp_order_status AS ENUM (
    'ordered',
    'received',
    'closed'
);

CREATE TABLE excise_stamp_orders (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    jurisdiction        TEXT NOT NULL,
    ordered_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    quantity_ordered    INTEGER NOT NULL CHECK (quantity_ordered > 0),
    received_at         TIMESTAMPTZ,
    serial_start        TEXT,                  -- e.g. "ABC00001"
    serial_end          TEXT,                  -- e.g. "ABC10000"
    quantity_received   INTEGER NOT NULL DEFAULT 0 CHECK (quantity_received >= 0),
    quantity_applied    INTEGER NOT NULL DEFAULT 0 CHECK (quantity_applied >= 0),
    quantity_voided     INTEGER NOT NULL DEFAULT 0 CHECK (quantity_voided >= 0),
    status              excise_stamp_order_status NOT NULL DEFAULT 'ordered',
    notes               TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (quantity_applied + quantity_voided <= quantity_received)
);

CREATE INDEX excise_stamp_orders_jurisdiction_idx
    ON excise_stamp_orders (tenant_id, jurisdiction, status);

CREATE TRIGGER excise_stamp_orders_updated_at
    BEFORE UPDATE ON excise_stamp_orders
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE excise_stamp_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE excise_stamp_orders FORCE  ROW LEVEL SECURITY;
CREATE POLICY excise_stamp_orders_tenant ON excise_stamp_orders FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- ------------------------------------------------------------------------
-- bottling_runs: a bottling event that debits a bulk container and
-- produces packaged inventory of one product SKU into one destination
-- jurisdiction.
-- ------------------------------------------------------------------------
CREATE TABLE bottling_runs (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id                   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    run_no                      INTEGER NOT NULL,
    product_id                  UUID NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
    source_container_id         UUID NOT NULL REFERENCES bulk_containers(id),
    destination_jurisdiction    TEXT NOT NULL,
    bottling_date               DATE NOT NULL,
    bottle_count                INTEGER NOT NULL CHECK (bottle_count > 0),
    bottling_loss_l             DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (bottling_loss_l >= 0),
    lot_code                    TEXT NOT NULL,
    tank_gauge_volume_l         DOUBLE PRECISION NOT NULL,
    tank_gauge_abv_pct          DOUBLE PRECISION NOT NULL,
    tank_gauge_laa              DOUBLE PRECISION NOT NULL,
    bulk_movement_id            UUID NOT NULL REFERENCES bulk_movements(id),
    notes                       TEXT NOT NULL DEFAULT '',
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, run_no),
    UNIQUE (tenant_id, lot_code)
);

CREATE INDEX bottling_runs_product_idx ON bottling_runs (product_id, bottling_date DESC);
CREATE INDEX bottling_runs_jurisdiction_idx
    ON bottling_runs (tenant_id, destination_jurisdiction, bottling_date DESC);

CREATE TRIGGER bottling_runs_updated_at
    BEFORE UPDATE ON bottling_runs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE bottling_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE bottling_runs FORCE  ROW LEVEL SECURITY;
CREATE POLICY bottling_runs_tenant ON bottling_runs FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- bottling_run_stamp_usage: ties consumed stamps from an order to a
-- bottling run. May span multiple orders if one order is exhausted mid-run.
CREATE TABLE bottling_run_stamp_usage (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    bottling_run_id     UUID NOT NULL REFERENCES bottling_runs(id) ON DELETE CASCADE,
    stamp_order_id      UUID NOT NULL REFERENCES excise_stamp_orders(id),
    bottle_count        INTEGER NOT NULL CHECK (bottle_count > 0),
    serial_start        TEXT NOT NULL DEFAULT '',
    serial_end          TEXT NOT NULL DEFAULT '',
    voids               INTEGER NOT NULL DEFAULT 0 CHECK (voids >= 0),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX bottling_run_stamp_usage_run_idx ON bottling_run_stamp_usage (bottling_run_id);
CREATE INDEX bottling_run_stamp_usage_order_idx ON bottling_run_stamp_usage (stamp_order_id);

ALTER TABLE bottling_run_stamp_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE bottling_run_stamp_usage FORCE  ROW LEVEL SECURITY;
CREATE POLICY bottling_run_stamp_usage_tenant ON bottling_run_stamp_usage FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- ------------------------------------------------------------------------
-- packaged_inventory: bottles in the excise warehouse awaiting removal.
-- Keyed by (product, lot_code, jurisdiction). Bottling adds; future
-- removals (Stage 7) will subtract.
-- ------------------------------------------------------------------------
CREATE TABLE packaged_inventory (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    product_id          UUID NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
    lot_code            TEXT NOT NULL,
    jurisdiction        TEXT NOT NULL,
    bottling_run_id     UUID REFERENCES bottling_runs(id),
    bottles_on_hand     INTEGER NOT NULL DEFAULT 0 CHECK (bottles_on_hand >= 0),
    bottles_packaged    INTEGER NOT NULL DEFAULT 0 CHECK (bottles_packaged >= 0),
    bottles_removed     INTEGER NOT NULL DEFAULT 0 CHECK (bottles_removed >= 0),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (product_id, lot_code, jurisdiction)
);

CREATE INDEX packaged_inventory_product_idx
    ON packaged_inventory (product_id, jurisdiction);

CREATE TRIGGER packaged_inventory_updated_at
    BEFORE UPDATE ON packaged_inventory
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE packaged_inventory ENABLE ROW LEVEL SECURITY;
ALTER TABLE packaged_inventory FORCE  ROW LEVEL SECURITY;
CREATE POLICY packaged_inventory_tenant ON packaged_inventory FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));
