-- 000071_consignment: our stock, at somebody else's premises. PLAN D7.
--
-- Stage 198 built the return path — product coming BACK from the
-- duty-paid market. This is the other half of that item and a different
-- question: stock that went out and is still ours.
--
-- The excise question is the whole of it, and it has an answer that has
-- to be stated rather than assumed.
--
-- Stillhouse treats a consignment as NOT a removal. The stock stays on
-- hand, marked as being at a customer, and a removal is recorded when it
-- sells. That is the ordinary accounting treatment and it is the safe one
-- here for a specific reason: at an at-packaging duty point (stage 145)
-- duty has already crystallised and nothing about consignment changes it,
-- while at an at-removal duty point recording the removal LATER errs
-- toward reporting duty later rather than never — and a return that
-- reports duty a month late is a correctable mistake, where one that
-- never reports it is not.
--
-- If a licensee's own arrangement treats the shipment itself as the
-- removal, they record a removal when it ships and do not use this. The
-- UI says so; the alternative is a feature that quietly picks one
-- treatment for everybody.
--
-- What consignment stock is NOT is available. It is ours and it is a
-- hundred kilometres away, so it must not appear as stock that can be
-- picked onto somebody else's order — which is exactly the mistake
-- bulk_possession was added to prevent for casks, arriving for bottles.

CREATE TYPE consignment_status AS ENUM (
    'out',        -- at the customer, unsold
    'settled',    -- sold through; a removal exists
    'recalled'    -- came back unsold; not a return, because it never sold
);

CREATE TABLE consignments (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    consignment_no        INTEGER NOT NULL,
    packaged_inventory_id UUID NOT NULL REFERENCES packaged_inventory(id) ON DELETE RESTRICT,
    customer_id           UUID NOT NULL REFERENCES customers(id) ON DELETE RESTRICT,
    bottles               INTEGER NOT NULL CHECK (bottles > 0),
    -- How many of those have sold through and how many came back. The
    -- three always reconcile: bottles = settled + recalled + still out.
    bottles_settled       INTEGER NOT NULL DEFAULT 0 CHECK (bottles_settled >= 0),
    bottles_recalled      INTEGER NOT NULL DEFAULT 0 CHECK (bottles_recalled >= 0),
    status                consignment_status NOT NULL DEFAULT 'out',
    sent_on               DATE NOT NULL,
    settled_on            DATE,
    notes                 TEXT NOT NULL DEFAULT '',
    created_by            UUID REFERENCES users(id),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, consignment_no),
    -- Nothing can settle or come back that never went.
    CHECK (bottles_settled + bottles_recalled <= bottles)
);

CREATE INDEX consignments_tenant_status_idx ON consignments (tenant_id, status, sent_on DESC);
CREATE INDEX consignments_lot_idx ON consignments (packaged_inventory_id);
CREATE INDEX consignments_customer_idx ON consignments (customer_id);

CREATE TRIGGER consignments_updated_at
    BEFORE UPDATE ON consignments
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE consignments ENABLE ROW LEVEL SECURITY;
ALTER TABLE consignments FORCE  ROW LEVEL SECURITY;
CREATE POLICY consignments_tenant ON consignments FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));
