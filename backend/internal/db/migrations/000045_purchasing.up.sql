-- 000045_purchasing: the order behind the delivery.
--
-- RecordMaterialReceipt stood alone. Grain arrived, somebody typed the
-- quantity and, if they knew it, a unit cost — and nothing said what had
-- been ordered, from whom, at what price, or whether the delivery
-- matched. Three consequences, all of which a distillery feels:
--
--   * Nobody can answer "what is on order" without a filing cabinet.
--   * A short delivery is invisible. The lot records what arrived; the
--     difference between that and what was bought is where money goes.
--   * The unit cost is whatever somebody remembered. Freight, duty and
--     handling sit in an expense account instead of in the cost of the
--     grain, so every downstream figure — inventory value, cost of
--     sales, the price a bottle has to carry — is understated by exactly
--     the amount it cost to get the grain to the door.
--
-- The last one is `E3`, and it is why landed cost lands here rather than
-- as its own feature: a charge can only be absorbed into a unit cost at
-- the moment there is a receipt to absorb it into.

CREATE TABLE suppliers (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    account_reference TEXT NOT NULL DEFAULT '',
    contact_name   TEXT NOT NULL DEFAULT '',
    email          TEXT NOT NULL DEFAULT '',
    phone          TEXT NOT NULL DEFAULT '',
    address        TEXT NOT NULL DEFAULT '',
    -- Net terms in days. NULL means none recorded; 0 means due on
    -- receipt, which is a different statement.
    payment_terms_days INTEGER CHECK (payment_terms_days IS NULL OR payment_terms_days >= 0),
    -- Where they are, which decides whether a purchase carries import
    -- duty and cross-border freight.
    country        TEXT NOT NULL DEFAULT '',
    notes          TEXT NOT NULL DEFAULT '',
    archived_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);

CREATE TRIGGER suppliers_updated_at
    BEFORE UPDATE ON suppliers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE suppliers ENABLE ROW LEVEL SECURITY;
ALTER TABLE suppliers FORCE  ROW LEVEL SECURITY;
CREATE POLICY suppliers_tenant ON suppliers FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- draft → placed → (partially_received) → received → closed, with
-- cancelled reachable from anywhere before receipt. Deliberately few
-- states: an approval workflow with six of them is a workflow people
-- route around.
CREATE TYPE purchase_order_status AS ENUM (
    'draft', 'placed', 'partially_received', 'received', 'closed', 'cancelled'
);

CREATE TABLE purchase_orders (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    supplier_id    UUID NOT NULL REFERENCES suppliers(id) ON DELETE RESTRICT,
    -- Sequential per tenant, the way a person refers to it on the phone.
    po_no          INTEGER NOT NULL,
    status         purchase_order_status NOT NULL DEFAULT 'draft',
    ordered_on     DATE,
    expected_on    DATE,
    reference      TEXT NOT NULL DEFAULT '',
    currency       TEXT NOT NULL DEFAULT 'CAD',
    notes          TEXT NOT NULL DEFAULT '',
    placed_by      UUID REFERENCES users(id),
    placed_at      TIMESTAMPTZ,
    closed_at      TIMESTAMPTZ,
    cancelled_at   TIMESTAMPTZ,
    cancel_reason  TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, po_no)
);

CREATE INDEX purchase_orders_open_idx
    ON purchase_orders (tenant_id, status, expected_on);

CREATE TRIGGER purchase_orders_updated_at
    BEFORE UPDATE ON purchase_orders
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE purchase_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_orders FORCE  ROW LEVEL SECURITY;
CREATE POLICY purchase_orders_tenant ON purchase_orders FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

CREATE TABLE purchase_order_lines (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    purchase_order_id UUID NOT NULL REFERENCES purchase_orders(id) ON DELETE CASCADE,
    material_id    UUID NOT NULL REFERENCES materials(id) ON DELETE RESTRICT,
    quantity_ordered  DOUBLE PRECISION NOT NULL CHECK (quantity_ordered > 0),
    -- Running total of what has actually arrived against this line.
    -- Denormalised so "what is still outstanding" is a column rather
    -- than a join, and maintained inside the receiving transaction.
    quantity_received DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (quantity_received >= 0),
    -- What was agreed, per unit, in the order's currency. NUMERIC
    -- because it is money on a document somebody signs.
    unit_price     NUMERIC(12, 4) NOT NULL CHECK (unit_price >= 0),
    uom            TEXT NOT NULL DEFAULT '',
    notes          TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX purchase_order_lines_po_idx ON purchase_order_lines (purchase_order_id);
CREATE INDEX purchase_order_lines_material_idx ON purchase_order_lines (material_id);

CREATE TRIGGER purchase_order_lines_updated_at
    BEFORE UPDATE ON purchase_order_lines
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE purchase_order_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_order_lines FORCE  ROW LEVEL SECURITY;
CREATE POLICY purchase_order_lines_tenant ON purchase_order_lines FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- ------------------------------------------------------------------------
-- Landed cost, on the lot.
--
-- The point of E3: freight, duty and handling belong in the cost of the
-- grain, not in an expense account. Without that, inventory value and
-- cost of sales are understated by exactly what it cost to get the grain
-- to the door — which for a distillery importing casks or barley is not
-- a rounding error.
--
-- The components are kept apart rather than folded into one number,
-- because an accountant reconciling a freight bill needs to see the
-- freight, and because unit_cost_cad has to keep meaning what it has
-- always meant: the supplier's price per unit.
-- ------------------------------------------------------------------------
ALTER TABLE material_lots
    ADD COLUMN purchase_order_line_id UUID REFERENCES purchase_order_lines(id) ON DELETE SET NULL,
    ADD COLUMN supplier_id            UUID REFERENCES suppliers(id) ON DELETE SET NULL,
    ADD COLUMN freight_cad            DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (freight_cad >= 0),
    ADD COLUMN import_duty_cad        DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (import_duty_cad >= 0),
    ADD COLUMN handling_cad           DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (handling_cad >= 0),
    -- Goods received, not yet invoiced. Set when the delivery lands and
    -- cleared when the invoice is matched, which is the whole of GRNI as
    -- a distillery experiences it.
    ADD COLUMN invoice_reference      TEXT NOT NULL DEFAULT '',
    ADD COLUMN invoiced_at            TIMESTAMPTZ;

CREATE INDEX material_lots_po_line_idx ON material_lots (purchase_order_line_id)
    WHERE purchase_order_line_id IS NOT NULL;
CREATE INDEX material_lots_grni_idx ON material_lots (tenant_id, received_at)
    WHERE invoiced_at IS NULL;

-- landed_unit_cost_cad is the figure everything downstream should use:
-- the supplier's price plus its share of what it cost to get here. A
-- generated column rather than a value somebody maintains, so it cannot
-- drift from its components.
--
-- Charges are spread across the quantity received, which is average
-- costing and is stated as such wherever the figure surfaces. A lot with
-- no recorded unit cost stays NULL rather than becoming the freight
-- alone — a cost of "just the shipping" would be worse than none.
ALTER TABLE material_lots
    ADD COLUMN landed_unit_cost_cad DOUBLE PRECISION
    GENERATED ALWAYS AS (
        CASE
            WHEN unit_cost_cad IS NULL THEN NULL
            WHEN quantity_received <= 0 THEN unit_cost_cad
            ELSE unit_cost_cad
                 + (freight_cad + import_duty_cad + handling_cad) / quantity_received
        END
    ) STORED;

GRANT SELECT, INSERT, UPDATE, DELETE ON suppliers            TO stillhouse_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON purchase_orders      TO stillhouse_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON purchase_order_lines TO stillhouse_app;
