-- 000036_customers: who the alcohol went to.
--
-- Stillhouse had no customer concept at all. A removal named its
-- destination in a free-text field, so the same provincial board was
-- "LCBO", "L.C.B.O." and "lcbo" across three months of returns, nothing
-- could be totalled by buyer, and the destination *kind* — which decides
-- whether duty is charged and which B266 line the movement lands on —
-- was re-chosen by hand every time from a dropdown, next to the free
-- text that did not have to agree with it.
--
-- A customer record fixes the classification at the place the decision
-- actually belongs: the LCBO is a provincial board and always will be,
-- so a removal to them cannot be typed as an export by accident.

-- ------------------------------------------------------------------------
-- customer_kind: the routes a bottle takes to a buyer. Deliberately the
-- buyer's *nature*, not the sales channel — a licensee buying at
-- wholesale and a private store buying at wholesale price the same and
-- report differently.
-- ------------------------------------------------------------------------
CREATE TYPE customer_kind AS ENUM (
    'provincial_board',   -- LCBO, BCLDB, AGLC, SAQ, NSLC…
    'licensee',           -- a bar or restaurant holding a liquor licence
    'private_retail',     -- a private liquor store, where the province has them
    'spirits_licensee',   -- another excise licensee: a non-duty-paid movement
    'export',             -- leaving Canada
    'on_site_retail',     -- the distillery's own shop or tasting room
    'other'
);

CREATE TABLE customers (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name                 TEXT NOT NULL,
    kind                 customer_kind NOT NULL,

    -- Province or territory code (CA-ON, CA-BC…), matching
    -- internal/pricing/jurisdictions.go. Empty for an export customer
    -- outside Canada, where no provincial board is involved.
    jurisdiction         TEXT NOT NULL DEFAULT '',

    -- What a removal to this customer is, for excise purposes. Stored as
    -- the same vocabulary the removal already uses, so choosing a
    -- customer fills the field in rather than adding a second one that
    -- can disagree with it. 'duty_paid_customer' for a board or a
    -- licensee; 'transfer_out_in_bond' for another spirits licensee;
    -- 'export' for export.
    default_destination_kind TEXT NOT NULL DEFAULT 'duty_paid_customer',

    -- Their own excise licence, when they hold one. EDM10-1-7 page 3
    -- names the counterparty on every bulk movement between licensees,
    -- so this is the field that makes those lines fillable.
    licence_number       TEXT NOT NULL DEFAULT '',

    -- Their number for us, or ours for them — a board vendor number, a
    -- customer account code. Whatever has to appear on the paperwork.
    account_reference    TEXT NOT NULL DEFAULT '',

    contact_name         TEXT NOT NULL DEFAULT '',
    email                TEXT NOT NULL DEFAULT '',
    phone                TEXT NOT NULL DEFAULT '',
    address              TEXT NOT NULL DEFAULT '',

    -- Net terms in days. NULL means none recorded; 0 means due on
    -- receipt, which is a different statement.
    payment_terms_days   INTEGER CHECK (payment_terms_days IS NULL OR payment_terms_days >= 0),

    notes                TEXT NOT NULL DEFAULT '',
    archived_at          TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);

CREATE INDEX customers_tenant_kind_idx ON customers (tenant_id, kind);

CREATE TRIGGER customers_updated_at
    BEFORE UPDATE ON customers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE customers ENABLE ROW LEVEL SECURITY;
ALTER TABLE customers FORCE  ROW LEVEL SECURITY;
CREATE POLICY customers_tenant ON customers FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- ------------------------------------------------------------------------
-- price_lists: what a product costs down a given route.
--
-- Dated rather than mutable, because a price that changed in March must
-- not silently restate what was quoted in February. A list with no
-- effective_to is the one in force.
-- ------------------------------------------------------------------------
CREATE TABLE price_lists (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    -- Which pricing route this list represents, matching SalesChannel in
    -- pricing.proto: wholesale, on-site retail, export.
    channel         TEXT NOT NULL DEFAULT 'wholesale',
    jurisdiction    TEXT NOT NULL DEFAULT '',
    currency        TEXT NOT NULL DEFAULT 'CAD',
    effective_from  DATE NOT NULL,
    effective_to    DATE,
    notes           TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name),
    CONSTRAINT price_lists_dates_chk
        CHECK (effective_to IS NULL OR effective_to >= effective_from)
);

CREATE INDEX price_lists_tenant_effective_idx
    ON price_lists (tenant_id, effective_from DESC);

CREATE TRIGGER price_lists_updated_at
    BEFORE UPDATE ON price_lists
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE price_lists ENABLE ROW LEVEL SECURITY;
ALTER TABLE price_lists FORCE  ROW LEVEL SECURITY;
CREATE POLICY price_lists_tenant ON price_lists FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

CREATE TABLE price_list_entries (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    price_list_id  UUID NOT NULL REFERENCES price_lists(id) ON DELETE CASCADE,
    product_id     UUID NOT NULL REFERENCES products(id)    ON DELETE CASCADE,
    -- Per bottle, in the list's currency, before sales tax. Stored as
    -- NUMERIC rather than double: this is money somebody invoices, and
    -- an IEEE-754 cent is not a thing.
    unit_price     NUMERIC(12, 4) NOT NULL CHECK (unit_price >= 0),
    -- Bottles per case, when the list is quoted by the case. NULL means
    -- the list does not say.
    case_size      INTEGER CHECK (case_size IS NULL OR case_size > 0),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (price_list_id, product_id)
);

CREATE INDEX price_list_entries_product_idx ON price_list_entries (product_id);

CREATE TRIGGER price_list_entries_updated_at
    BEFORE UPDATE ON price_list_entries
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE price_list_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE price_list_entries FORCE  ROW LEVEL SECURITY;
CREATE POLICY price_list_entries_tenant ON price_list_entries FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- A customer's default list. Nullable: plenty of customers are quoted
-- ad hoc, and pretending otherwise would mean inventing a list.
ALTER TABLE customers
    ADD COLUMN price_list_id UUID REFERENCES price_lists(id) ON DELETE SET NULL;

-- ------------------------------------------------------------------------
-- The point of all of it: a removal names a buyer.
--
-- destination_name stays. It is what every removal recorded before today
-- has, it is still the right thing for a one-off, and rewriting history
-- to point at customers that did not exist when the movement happened
-- would be inventing records. New removals that name a customer copy the
-- name across, so the B266 and the audit trail read identically either
-- way and no report has to know which era a row came from.
-- ------------------------------------------------------------------------
ALTER TABLE packaging_removals
    ADD COLUMN customer_id UUID REFERENCES customers(id) ON DELETE RESTRICT;

CREATE INDEX packaging_removals_customer_idx
    ON packaging_removals (customer_id, removal_date DESC);

GRANT SELECT, INSERT, UPDATE, DELETE ON customers          TO stillhouse_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON price_lists        TO stillhouse_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON price_list_entries TO stillhouse_app;
