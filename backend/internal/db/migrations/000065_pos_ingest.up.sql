-- 000065_pos_ingest: tasting-room and web sales, arriving as data rather
-- than as typing. PLAN G4.
--
-- The item calls this "a compliance feature wearing a sales costume", and
-- that is exactly right: every sale keyed by hand is a chance to
-- under-report. But automating it introduces the opposite failure, and
-- the opposite failure is the one this schema is shaped around.
--
-- A POS webhook is delivered at least once. Squares, Shopifys and
-- Lightspeeds all retry, and a retry that creates a second removal
-- reports duty twice and takes stock off the shelf that is still on it.
-- Under-reporting is a penalty; over-reporting is a penalty AND a stock
-- figure nobody can reconcile. So the unique constraint on
-- (tenant, source, external_id) is the single most load-bearing line in
-- this file: it is what makes redelivery harmless.
--
-- The other half is the SKU. Stillhouse cannot know that "GIN-750-DRY" is
-- a particular product, and guessing would post a removal against the
-- wrong lot — wrong duty, wrong stock, on a filed return. So the mapping
-- is operator-supplied, and a sale whose SKU is unmapped is REJECTED and
-- kept, not dropped. A sale that vanishes because nobody had mapped its
-- SKU is the under-reporting this feature exists to prevent, arriving
-- through the door it opened.

CREATE TYPE pos_sale_status AS ENUM (
    -- Received, not yet turned into a removal.
    'pending',
    -- A removal exists for it.
    'posted',
    -- Could not be posted, with a reason. Kept so somebody can fix the
    -- mapping and post it, rather than discovering the gap on a return.
    'rejected',
    -- Deliberately not posted: a test sale, a comp, a correction. An
    -- operator's decision, recorded as one.
    'ignored'
);

-- SKU → product. Per source, because two systems may use the same SKU
-- for different things and neither is wrong.
CREATE TABLE pos_product_map (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    source      TEXT NOT NULL,
    external_sku TEXT NOT NULL,
    product_id  UUID NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, source, external_sku)
);

ALTER TABLE pos_product_map ENABLE ROW LEVEL SECURITY;
ALTER TABLE pos_product_map FORCE  ROW LEVEL SECURITY;
CREATE POLICY pos_product_map_tenant ON pos_product_map FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

CREATE TABLE pos_sales (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    source       TEXT NOT NULL,
    -- The POS's own id for the line. The whole idempotency story.
    external_id  TEXT NOT NULL,
    external_sku TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    quantity     INTEGER NOT NULL CHECK (quantity > 0),
    unit_price_cad NUMERIC(12,2),
    sold_at      TIMESTAMPTZ NOT NULL,
    status       pos_sale_status NOT NULL DEFAULT 'pending',
    -- What it became, once posted.
    removal_id   UUID REFERENCES packaging_removals(id) ON DELETE SET NULL,
    -- Why it did not, when it did not.
    reject_reason TEXT NOT NULL DEFAULT '',
    received_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    posted_at    TIMESTAMPTZ,
    -- Redelivery is normal, not exceptional. This is what makes it
    -- harmless: the second delivery of the same line collides here and
    -- is dropped, instead of becoming a second removal.
    UNIQUE (tenant_id, source, external_id)
);

CREATE INDEX pos_sales_tenant_status_idx ON pos_sales (tenant_id, status, sold_at DESC);

ALTER TABLE pos_sales ENABLE ROW LEVEL SECURITY;
ALTER TABLE pos_sales FORCE  ROW LEVEL SECURITY;
CREATE POLICY pos_sales_tenant ON pos_sales FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));
