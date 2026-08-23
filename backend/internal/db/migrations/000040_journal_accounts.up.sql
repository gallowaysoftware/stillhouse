-- 000040_journal_accounts: the seam that lets Stillhouse not become an
-- accounting package.
--
-- Stillhouse knows what happened and what it was worth: duty crystallised
-- on a bottling run, grain received at a lot cost, materials consumed
-- into a mash, bottles moved to finished goods, stock removed and sold.
-- What it does not know, and must not guess, is which account each of
-- those belongs in — that is the licensee's chart of accounts, and every
-- distillery's is different.
--
-- So the mapping is data the operator supplies, and an event with no
-- mapping is reported as unmapped rather than posted to an invented
-- account. Same discipline as an excise rate the table cannot cite:
-- refuse to produce a number rather than produce a plausible one, because
-- a journal line in the wrong account is worse than a missing one — it
-- reconciles, and nobody looks again.

-- Four kinds, and the omissions are the point.
--
-- These are the events Stillhouse can put a defensible dollar figure
-- against: duty is an exact amount it calculated itself, a material
-- receipt and a mash consumption are a recorded lot cost times a
-- recorded quantity, and cost of goods on a removal is a stated
-- weighted-average basis over runs whose costs are known.
--
-- What is deliberately absent is the work-in-progress chain — spirit
-- gauged into bulk, and a bottling run's transfer out of WIP. Valuing
-- those needs labour, overhead absorption and a WIP convention that
-- Stillhouse does not have and must not invent; that is `E4` in PLAN.md.
-- An export that posted a made-up WIP figure would reconcile, and nobody
-- would look at it again.
CREATE TYPE journal_event_kind AS ENUM (
    'duty_payable',          -- duty crystallised, wherever the duty point falls
    'material_receipt',      -- raw material in, at lot cost
    'material_consumption',  -- raw material into a mash, at its lot's cost
    'cogs_on_removal'        -- packaged stock leaving, at average material cost
);

CREATE TABLE journal_accounts (
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    kind           journal_event_kind NOT NULL,
    -- Free text on purpose. A chart of accounts is the licensee's, and
    -- Stillhouse has no business validating "5010" against anything.
    debit_account  TEXT NOT NULL DEFAULT '',
    credit_account TEXT NOT NULL DEFAULT '',
    -- What they call it, so the export is readable by whoever imports it.
    debit_name     TEXT NOT NULL DEFAULT '',
    credit_name    TEXT NOT NULL DEFAULT '',
    memo_prefix    TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, kind)
);

CREATE TRIGGER journal_accounts_updated_at
    BEFORE UPDATE ON journal_accounts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE journal_accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE journal_accounts FORCE  ROW LEVEL SECURITY;
CREATE POLICY journal_accounts_tenant ON journal_accounts FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

GRANT SELECT, INSERT, UPDATE, DELETE ON journal_accounts TO stillhouse_app;
