-- 000063_packaged_returns: product coming back from the duty-paid market.
-- PLAN D7.
--
-- Spirits have no consignment regime the way small wine licensees do, but
-- returns still happen: a case comes back damaged, a listing is delisted,
-- a customer over-ordered. What the licensee needs is the stock put back
-- where it belongs and a credit raised, and what the return must NOT do is
-- quietly undo duty.
--
-- That is the whole judgement in this migration. Duty crystallised when
-- the stock was packaged or removed, depending on the licensee's duty
-- point, and it does not un-crystallise because the goods came back.
-- Getting it back is a refund claim under s.181/s.182 with a B256 behind
-- it — PLAN A9, which is blocked on sourcing that form's rules. So a
-- return here restocks and credits the customer, records that duty was
-- paid and remains paid, and says outright that the refund is a separate
-- claim Stillhouse cannot yet make. A return that silently reduced duty
-- payable would understate a filed return, which is the one failure this
-- product exists to prevent.

CREATE TYPE packaged_return_condition AS ENUM (
    -- Back into stock: it can be sold again.
    'saleable',
    -- Came back but cannot be sold. It does not restock. Whether its
    -- destruction is relieved is the same question EDM3-4-1 asks of any
    -- destruction, and it is answered on the destruction, not here.
    'unsaleable'
);

CREATE TABLE packaged_returns (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    return_no             INTEGER NOT NULL,
    packaged_inventory_id UUID NOT NULL REFERENCES packaged_inventory(id) ON DELETE RESTRICT,
    -- Who sent it back. Nullable because a return can arrive without a
    -- customer record — a retailer's courier turning up — and refusing to
    -- record the stock until the paperwork is right is how stock goes
    -- unrecorded.
    customer_id           UUID REFERENCES customers(id) ON DELETE SET NULL,
    -- The removal this came back from, when it is known. Not required:
    -- matching a return to the shipment that sent it is often impossible
    -- months later, and a return that cannot be matched is still a
    -- return.
    removal_id            UUID REFERENCES packaging_removals(id) ON DELETE SET NULL,
    bottles               INTEGER NOT NULL CHECK (bottles > 0),
    condition             packaged_return_condition NOT NULL,
    returned_on           DATE NOT NULL,
    reason                TEXT NOT NULL DEFAULT '',
    -- The credit raised, if any. Separate from the stock movement because
    -- they are separate decisions: stock can come back with no credit, and
    -- a credit can be issued without stock coming back.
    credit_amount_cad     NUMERIC(12,2),
    credit_note_no        TEXT NOT NULL DEFAULT '',
    -- Duty as it stood when the goods left, carried on the row so the
    -- statement "duty was paid and remains paid" can be evidenced rather
    -- than asserted. Nothing computes from it; it is the record.
    duty_paid_cad         NUMERIC(12,2),
    notes                 TEXT NOT NULL DEFAULT '',
    voided_at             TIMESTAMPTZ,
    voided_by             UUID REFERENCES users(id),
    void_reason           TEXT NOT NULL DEFAULT '',
    created_by            UUID REFERENCES users(id),
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, return_no)
);

CREATE INDEX packaged_returns_tenant_date_idx ON packaged_returns (tenant_id, returned_on DESC);
CREATE INDEX packaged_returns_lot_idx ON packaged_returns (packaged_inventory_id);

CREATE TRIGGER packaged_returns_updated_at
    BEFORE UPDATE ON packaged_returns
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE packaged_returns ENABLE ROW LEVEL SECURITY;
ALTER TABLE packaged_returns FORCE  ROW LEVEL SECURITY;
CREATE POLICY packaged_returns_tenant ON packaged_returns FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));
