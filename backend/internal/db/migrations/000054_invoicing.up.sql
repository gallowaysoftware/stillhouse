-- Getting paid.
--
-- Stage 173 gave the distillery an order and a shipment; neither of them
-- asks anybody for money. An invoice is the document a customer pays
-- against, and the record that says whether they have.
--
-- Tax is the part worth being careful about. Rates are a fact about a
-- jurisdiction and a date, not something Stillhouse knows, and a rate
-- half-remembered produces an invoice that is wrong by exactly that
-- amount on every line. So tax rates are entered by the licensee,
-- effective-dated, and carry the provenance the pricing rates and the
-- provincial requirements already carry. An invoice with no tax rate
-- configured shows no tax and says so, rather than showing zero.

CREATE TYPE invoice_kind   AS ENUM ('invoice', 'credit_note');
CREATE TYPE invoice_status AS ENUM ('draft', 'issued', 'part_paid', 'paid', 'void');

-- What a jurisdiction charges, from when, on whose authority.
CREATE TABLE tax_rates (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    -- ISO 3166-2, or empty for one that applies everywhere (GST).
    jurisdiction   TEXT NOT NULL DEFAULT ''
        CHECK (jurisdiction = '' OR jurisdiction ~ '^CA-[A-Z]{2}$'),
    -- What it is called on the invoice: GST, HST, QST, PST.
    name           TEXT NOT NULL CHECK (length(trim(name)) > 0),
    -- A fraction: 0.05, not 5.
    rate           NUMERIC(8, 6) NOT NULL CHECK (rate >= 0 AND rate <= 1),
    effective_from DATE NOT NULL,
    -- Registration number to print, where the tax has one.
    registration_no TEXT NOT NULL DEFAULT '',
    provenance     requirement_provenance NOT NULL DEFAULT 'unknown',
    authority      TEXT NOT NULL DEFAULT '',
    notes          TEXT NOT NULL DEFAULT '',
    created_by     UUID NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT tax_rate_sourced_cites CHECK (
        provenance <> 'sourced' OR length(trim(authority)) > 0
    ),
    UNIQUE (tenant_id, jurisdiction, name, effective_from)
);

CREATE TABLE invoices (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    kind           invoice_kind NOT NULL DEFAULT 'invoice',
    invoice_no     INTEGER NOT NULL,
    customer_id    UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    -- What it was raised against. Both optional: an invoice for a
    -- service, a cask-storage fee or a corkage arrangement has neither.
    sales_order_id UUID REFERENCES sales_orders(id) ON DELETE SET NULL,
    shipment_id    UUID REFERENCES shipments(id)    ON DELETE SET NULL,
    -- A credit note says which invoice it credits. Not a foreign key to
    -- itself being optional by accident: a credit note with nothing to
    -- credit is a refund, and this is not that.
    credits_invoice_id UUID REFERENCES invoices(id) ON DELETE RESTRICT,

    status         invoice_status NOT NULL DEFAULT 'draft',
    issue_date     DATE,
    -- Copied from the customer at issue, not joined. Terms change, and a
    -- document already sent does not.
    terms_days     INTEGER NOT NULL DEFAULT 0 CHECK (terms_days >= 0),
    due_date       DATE,
    currency       TEXT NOT NULL DEFAULT 'CAD',
    -- Copied at issue for the same reason: the name and address on a
    -- document are what they were when it was sent.
    bill_to_name   TEXT NOT NULL DEFAULT '',
    bill_to_address TEXT NOT NULL DEFAULT '',
    customer_reference TEXT NOT NULL DEFAULT '',
    notes          TEXT NOT NULL DEFAULT '',
    void_reason    TEXT NOT NULL DEFAULT '',
    voided_at      TIMESTAMPTZ,
    issued_at      TIMESTAMPTZ,
    issued_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_by     UUID NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (tenant_id, kind, invoice_no),
    CONSTRAINT invoice_credit_note_credits CHECK (
        kind <> 'credit_note' OR credits_invoice_id IS NOT NULL
    ),
    CONSTRAINT invoice_issued_has_date CHECK (
        status = 'draft' OR status = 'void' OR issue_date IS NOT NULL
    ),
    CONSTRAINT invoice_void_has_reason CHECK (
        voided_at IS NULL OR length(trim(void_reason)) > 0
    )
);

CREATE TABLE invoice_lines (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    invoice_id     UUID NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    -- Optional: a freight line or a storage fee is not a product.
    product_id     UUID REFERENCES products(id) ON DELETE SET NULL,
    description    TEXT NOT NULL CHECK (length(trim(description)) > 0),
    quantity       NUMERIC(14, 4) NOT NULL CHECK (quantity <> 0),
    unit_price     NUMERIC(12, 4) NOT NULL,
    -- Stored, not derived, because a line already sent is what it was.
    -- The service computes it; the column holds it.
    line_total     NUMERIC(14, 4) NOT NULL,
    -- Which taxes applied, resolved at issue and frozen. Rates change.
    tax_name       TEXT NOT NULL DEFAULT '',
    tax_rate       NUMERIC(8, 6) NOT NULL DEFAULT 0,
    tax_amount     NUMERIC(14, 4) NOT NULL DEFAULT 0,
    sort_order     INTEGER NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE invoice_payments (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    invoice_id     UUID NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    received_on    DATE NOT NULL DEFAULT CURRENT_DATE,
    amount         NUMERIC(14, 4) NOT NULL CHECK (amount > 0),
    method         TEXT NOT NULL DEFAULT '',
    reference      TEXT NOT NULL DEFAULT '',
    notes          TEXT NOT NULL DEFAULT '',
    recorded_by    UUID NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['tax_rates', 'invoices', 'invoice_lines', 'invoice_payments'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY %I ON %I
            USING (tenant_id = current_setting('app.current_tenant_id')::uuid)
            WITH CHECK (tenant_id = current_setting('app.current_tenant_id')::uuid)$p$,
            t || '_tenant_isolation', t);
        EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO stillhouse_app', t);
    END LOOP;
END $$;

CREATE INDEX invoices_customer_idx ON invoices (customer_id);
CREATE INDEX invoices_open_idx ON invoices (due_date)
    WHERE status IN ('issued', 'part_paid');
CREATE INDEX invoice_lines_invoice_idx ON invoice_lines (invoice_id);
CREATE INDEX invoice_payments_invoice_idx ON invoice_payments (invoice_id);
CREATE INDEX tax_rates_lookup_idx ON tax_rates (tenant_id, jurisdiction, effective_from DESC);

-- An invoice past its due date with money still on it.
ALTER TYPE alert_kind ADD VALUE IF NOT EXISTS 'invoice_overdue';
