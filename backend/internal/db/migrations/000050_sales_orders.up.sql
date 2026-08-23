-- 000050_sales_orders: from an order to a return, without re-keying.
--
-- Stillhouse had customers (stage 157) and removals, and nothing in
-- between. An operator who shipped a pallet had to remember to record a
-- removal, get the bottle count right by hand, and pick the same
-- customer twice. Every one of those is a place a filed return quietly
-- diverges from what left the building — and the direction of the error
-- is under-reporting, because a removal nobody remembered is duty
-- nobody paid.
--
-- The chain is order → shipment → removal, and the last arrow is the
-- point of the whole thing (`D6`). What ships is what the removal says
-- left, because it is the same row read twice rather than typed twice.
--
-- Reservation is deliberately soft. A sales order line reserves stock in
-- the sense that the screen will tell you it is spoken for; it does not
-- decrement packaged_inventory, because the alcohol has not gone
-- anywhere and a B266 built on promises rather than movements would be
-- wrong. Stock leaves at the shipment, once.

CREATE TYPE sales_order_status AS ENUM (
    'draft', 'confirmed', 'partially_shipped', 'shipped', 'closed', 'cancelled'
);

CREATE TABLE sales_orders (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    customer_id    UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    order_no       INTEGER NOT NULL,
    status         sales_order_status NOT NULL DEFAULT 'draft',
    ordered_on     DATE NOT NULL DEFAULT CURRENT_DATE,
    -- When they want it, which is what a pick list is sequenced by.
    required_by    DATE,
    -- Their purchase order number. The thing that gets quoted on the
    -- phone and has to appear on the packing slip.
    customer_reference TEXT NOT NULL DEFAULT '',
    -- Which price list was used, so a line's price can be explained a
    -- year later without guessing which one was in force.
    price_list_id  UUID REFERENCES price_lists(id) ON DELETE SET NULL,
    location_id    UUID REFERENCES locations(id) ON DELETE SET NULL,
    notes          TEXT NOT NULL DEFAULT '',
    confirmed_at   TIMESTAMPTZ,
    confirmed_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    cancelled_at   TIMESTAMPTZ,
    cancel_reason  TEXT NOT NULL DEFAULT '',
    created_by     UUID NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, order_no)
);

CREATE INDEX sales_orders_open_idx
    ON sales_orders (tenant_id, required_by)
    WHERE status IN ('draft', 'confirmed', 'partially_shipped');
CREATE INDEX sales_orders_customer_idx ON sales_orders (customer_id, ordered_on DESC);

CREATE TRIGGER sales_orders_updated_at
    BEFORE UPDATE ON sales_orders
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE sales_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE sales_orders FORCE  ROW LEVEL SECURITY;
CREATE POLICY sales_orders_tenant ON sales_orders FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

CREATE TABLE sales_order_lines (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    sales_order_id UUID NOT NULL REFERENCES sales_orders(id) ON DELETE CASCADE,
    product_id     UUID NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
    bottles_ordered INTEGER NOT NULL CHECK (bottles_ordered > 0),
    -- Running total actually shipped. Denormalised so "what is still
    -- owed" is a column, maintained inside the shipping transaction.
    bottles_shipped INTEGER NOT NULL DEFAULT 0 CHECK (bottles_shipped >= 0),
    -- Per bottle, in CAD, at the moment of ordering. NUMERIC because it
    -- is money on a document, and copied rather than joined because a
    -- price list that changes in March must not restate February's
    -- order.
    unit_price     NUMERIC(12, 4) NOT NULL DEFAULT 0 CHECK (unit_price >= 0),
    notes          TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX sales_order_lines_order_idx ON sales_order_lines (sales_order_id);
CREATE INDEX sales_order_lines_product_idx ON sales_order_lines (product_id);

CREATE TRIGGER sales_order_lines_updated_at
    BEFORE UPDATE ON sales_order_lines
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE sales_order_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE sales_order_lines FORCE  ROW LEVEL SECURITY;
CREATE POLICY sales_order_lines_tenant ON sales_order_lines FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- ------------------------------------------------------------------------
-- Shipments.
--
-- A shipment is what physically left, and it is the thing that produces
-- the removal. Its lines name the *lot* as well as the product, because
-- a removal is against a lot — that is what carries the jurisdiction,
-- the stamps and the duty basis — and picking is where the lot gets
-- chosen.
-- ------------------------------------------------------------------------
CREATE TYPE shipment_status AS ENUM ('picking', 'shipped', 'cancelled');

CREATE TABLE shipments (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    sales_order_id UUID REFERENCES sales_orders(id) ON DELETE RESTRICT,
    -- Denormalised from the order, and required, because a shipment can
    -- exist without one — a sample, a replacement — and still has to
    -- name who it went to.
    customer_id    UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    shipment_no    INTEGER NOT NULL,
    status         shipment_status NOT NULL DEFAULT 'picking',
    location_id    UUID REFERENCES locations(id) ON DELETE SET NULL,
    ship_date      DATE,
    carrier        TEXT NOT NULL DEFAULT '',
    tracking_ref   TEXT NOT NULL DEFAULT '',
    -- The bill of lading number, which is the document a driver signs.
    bol_reference  TEXT NOT NULL DEFAULT '',
    notes          TEXT NOT NULL DEFAULT '',
    shipped_at     TIMESTAMPTZ,
    shipped_by     UUID REFERENCES users(id) ON DELETE SET NULL,
    cancelled_at   TIMESTAMPTZ,
    cancel_reason  TEXT NOT NULL DEFAULT '',
    created_by     UUID NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, shipment_no)
);

CREATE INDEX shipments_open_idx ON shipments (tenant_id, status, ship_date);
CREATE INDEX shipments_order_idx ON shipments (sales_order_id)
    WHERE sales_order_id IS NOT NULL;

CREATE TRIGGER shipments_updated_at
    BEFORE UPDATE ON shipments
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE shipments ENABLE ROW LEVEL SECURITY;
ALTER TABLE shipments FORCE  ROW LEVEL SECURITY;
CREATE POLICY shipments_tenant ON shipments FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

CREATE TABLE shipment_lines (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    shipment_id    UUID NOT NULL REFERENCES shipments(id) ON DELETE CASCADE,
    -- Which order line this satisfies, when there is one.
    sales_order_line_id UUID REFERENCES sales_order_lines(id) ON DELETE SET NULL,
    -- The lot. This is the load-bearing field: a removal is against a
    -- lot, because that is what carries the jurisdiction, the stamps and
    -- the duty basis.
    packaged_inventory_id UUID NOT NULL REFERENCES packaged_inventory(id) ON DELETE RESTRICT,
    bottles        INTEGER NOT NULL CHECK (bottles > 0),
    -- The removal this line produced. Written when the shipment ships,
    -- and the reason the loop closes with no re-keying: what shipped and
    -- what the return says left are the same row read twice.
    packaging_removal_id UUID REFERENCES packaging_removals(id) ON DELETE SET NULL,
    notes          TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX shipment_lines_shipment_idx ON shipment_lines (shipment_id);
CREATE INDEX shipment_lines_lot_idx ON shipment_lines (packaged_inventory_id);

ALTER TABLE shipment_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE shipment_lines FORCE  ROW LEVEL SECURITY;
CREATE POLICY shipment_lines_tenant ON shipment_lines FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- The other direction of the D6 link, so a removal can say which
-- shipment produced it without a scan of shipment_lines.
ALTER TABLE packaging_removals
    ADD COLUMN shipment_id UUID REFERENCES shipments(id) ON DELETE SET NULL;

CREATE INDEX packaging_removals_shipment_idx ON packaging_removals (shipment_id)
    WHERE shipment_id IS NOT NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON sales_orders       TO stillhouse_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON sales_order_lines  TO stillhouse_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON shipments          TO stillhouse_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON shipment_lines     TO stillhouse_app;
