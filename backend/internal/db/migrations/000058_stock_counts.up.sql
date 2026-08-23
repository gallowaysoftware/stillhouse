-- Counting the warehouse, and what to do about the difference.
--
-- Stage 149 gave bulk containers a reason-coded adjustment — line D on
-- page 3 — but only one container at a time, reached from that
-- container's own page. Counting a warehouse is not that shape: it is a
-- sheet, worked through in order, with the book figure beside a blank,
-- and the differences dealt with afterwards rather than as they are
-- found.
--
-- The book figure is captured when the count starts, not when the line is
-- posted. A count that took a morning while somebody else was shipping
-- would otherwise measure the shipping rather than the discrepancy.

CREATE TYPE stock_count_status AS ENUM ('open', 'counted', 'posted', 'cancelled');

CREATE TYPE stock_count_scope AS ENUM ('bulk', 'packaged', 'materials', 'all');

CREATE TABLE stock_counts (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    count_no       INTEGER NOT NULL,
    name           TEXT NOT NULL DEFAULT '',
    scope          stock_count_scope NOT NULL DEFAULT 'all',
    location_id    UUID REFERENCES locations(id) ON DELETE SET NULL,
    status         stock_count_status NOT NULL DEFAULT 'open',
    -- When the book figures were taken. Everything on the sheet is "as
    -- at" this moment, which is what makes the variances mean anything.
    opened_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    counted_at     TIMESTAMPTZ,
    posted_at      TIMESTAMPTZ,
    posted_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    cancel_reason  TEXT NOT NULL DEFAULT '',
    notes          TEXT NOT NULL DEFAULT '',
    created_by     UUID NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (tenant_id, count_no)
);

-- One subject on the sheet. Exactly one of the three references is set,
-- the same shape lab_results and labour_entries use.
CREATE TABLE stock_count_lines (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    stock_count_id UUID NOT NULL REFERENCES stock_counts(id) ON DELETE CASCADE,

    bulk_container_id     UUID REFERENCES bulk_containers(id)   ON DELETE CASCADE,
    packaged_inventory_id UUID REFERENCES packaged_inventory(id) ON DELETE CASCADE,
    material_lot_id       UUID REFERENCES material_lots(id)     ON DELETE CASCADE,

    -- What the ledger said when the count was opened.
    book_quantity  DOUBLE PRECISION NOT NULL,
    -- What was found. NULL until somebody counts it, which is different
    -- from a count of zero — an uncounted line is not a line saying the
    -- shelf was empty.
    counted_quantity DOUBLE PRECISION,
    -- For bulk, the strength found; a volume without one says nothing
    -- about the alcohol.
    counted_abv_pct DOUBLE PRECISION
        CHECK (counted_abv_pct IS NULL OR (counted_abv_pct > 0 AND counted_abv_pct <= 100)),
    uom            TEXT NOT NULL DEFAULT '',

    reason         inventory_adjustment_reason,
    explanation    TEXT NOT NULL DEFAULT '',
    counted_by     TEXT NOT NULL DEFAULT '',
    -- Set when this line's variance has been written into the ledger.
    posted_at      TIMESTAMPTZ,
    -- What it produced: the bulk adjustment, or the packaged one.
    adjustment_id  UUID REFERENCES inventory_adjustments(id) ON DELETE SET NULL,
    notes          TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT stock_count_line_one_subject CHECK (
        (bulk_container_id     IS NOT NULL)::int
      + (packaged_inventory_id IS NOT NULL)::int
      + (material_lot_id       IS NOT NULL)::int = 1
    ),
    -- "other" carries a mandatory explanation, exactly as the bulk
    -- adjustment does. A variance nobody explained is a number.
    CONSTRAINT stock_count_line_other_explained CHECK (
        reason <> 'other' OR length(trim(explanation)) > 0
    )
);

-- Packaged adjustments, which had no path at all.
--
-- Bottles could only ever be created by a bottling run and removed by a
-- removal. A count that finds a case missing had nowhere to say so, and
-- editing bottles_on_hand directly would be worse than nowhere: the
-- B266's packaged balance is walked backwards from what is on hand now by
-- undoing runs and removals, so a balance that changed with nothing in
-- the ledger to undo would silently restate a period already filed —
-- the same failure a possession flag with no movement behind it would
-- have caused (stage 176).
--
-- So an adjustment is a row the walk can undo, and SumPackagedOnHandAsOf
-- learns about it in the same migration.
CREATE TABLE packaged_adjustments (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    packaged_inventory_id UUID NOT NULL REFERENCES packaged_inventory(id) ON DELETE RESTRICT,
    occurred_on    DATE NOT NULL DEFAULT CURRENT_DATE,
    -- Signed: positive found, negative missing.
    bottles_delta  INTEGER NOT NULL CHECK (bottles_delta <> 0),
    book_bottles   INTEGER NOT NULL,
    counted_bottles INTEGER NOT NULL,
    -- The alcohol the difference represents, computed at the product's
    -- strength the same way every other packaged figure is.
    laa_delta      DOUBLE PRECISION NOT NULL,
    reason         inventory_adjustment_reason NOT NULL,
    explanation    TEXT NOT NULL DEFAULT '',
    stock_count_id UUID REFERENCES stock_counts(id) ON DELETE SET NULL,
    recorded_by    UUID NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT packaged_adjustment_other_explained CHECK (
        reason <> 'other' OR length(trim(explanation)) > 0
    )
);

DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['stock_counts', 'stock_count_lines', 'packaged_adjustments'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY %I ON %I
            USING (tenant_id = current_setting('app.current_tenant_id')::uuid)
            WITH CHECK (tenant_id = current_setting('app.current_tenant_id')::uuid)$p$,
            t || '_tenant_isolation', t);
        EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO stillhouse_app', t);
    END LOOP;
END $$;

CREATE INDEX stock_count_lines_count_idx ON stock_count_lines (stock_count_id);
CREATE INDEX stock_counts_open_idx ON stock_counts (status) WHERE status = 'open';
CREATE INDEX packaged_adjustments_lot_idx ON packaged_adjustments (packaged_inventory_id, occurred_on DESC);
CREATE INDEX packaged_adjustments_period_idx ON packaged_adjustments (occurred_on);
