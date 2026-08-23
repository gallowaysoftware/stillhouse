-- 000049_redistillation: what went back into the still, and what came out.
--
-- EDM3-1-1 ¶38–41. Stage 146 gave page 3 the reportable *movement* —
-- bulk returned to production, and packaged spirits unpackaged back to
-- bulk — so the B266 line has been fillable since then. What has never
-- existed is the record either side of it: the quantity taken, the
-- quantity produced after, and the losses incurred in the process.
--
-- That gap matters because a redistillation is the one operation where
-- alcohol legitimately disappears in bulk and nobody is obliged to
-- notice. Spirit leaves stock as a reportable withdrawal; some weeks
-- later a distillation run produces less than went in; and without
-- something joining the two, the difference is not a loss anybody has
-- classified — it is just a smaller number. Stage 147 made a loss say
-- whether it is relieved or duty-payable, and this is what gives a
-- redistillation loss somewhere to be said.
--
-- The record is deliberately a join rather than a second ledger. It
-- points at the withdrawal movement that already happened and at the
-- distillation run that already exists; it holds no volumes of its own
-- beyond what was taken, because the alcohol is accounted for in the
-- bulk ledger and a second copy would be a second answer.

CREATE TYPE redistillation_reason AS ENUM (
    'off_spec',        -- the spirit was not what it should have been
    'feints_recovery', -- heads and tails back through for the alcohol in them
    'reprocessing',    -- deliberate second pass, e.g. gin from NGS
    'other'
);

CREATE TABLE redistillations (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- Where it came from, and the withdrawal that took it out. The
    -- movement is the reportable event; this row is the record around it.
    source_container_id UUID NOT NULL REFERENCES bulk_containers(id) ON DELETE RESTRICT,
    bulk_movement_id    UUID REFERENCES bulk_movements(id) ON DELETE SET NULL,

    reason         redistillation_reason NOT NULL,
    taken_on       DATE NOT NULL DEFAULT CURRENT_DATE,

    -- What went in. Recorded at the moment of withdrawal, because it is
    -- the figure that stops being observable once it is in the still.
    volume_taken_l DOUBLE PRECISION NOT NULL CHECK (volume_taken_l > 0),
    abv_taken_pct  DOUBLE PRECISION NOT NULL CHECK (abv_taken_pct > 0 AND abv_taken_pct <= 100),
    laa_taken      DOUBLE PRECISION NOT NULL CHECK (laa_taken > 0),

    -- What came out. NULL until the run has been gauged — which is the
    -- ordinary state for days, and is why the loss is not computed until
    -- both halves are known rather than defaulting to zero.
    distillation_run_id UUID REFERENCES distillation_runs(id) ON DELETE SET NULL,
    laa_produced        DOUBLE PRECISION CHECK (laa_produced IS NULL OR laa_produced >= 0),
    produced_on         DATE,

    -- The figure EDM3-1-1 ¶41 asks for. Generated, so it cannot drift
    -- from the two numbers it sits between, and NULL while the run is
    -- unfinished rather than showing the whole charge as lost.
    loss_laa DOUBLE PRECISION
        GENERATED ALWAYS AS (
            CASE WHEN laa_produced IS NULL THEN NULL ELSE laa_taken - laa_produced END
        ) STORED,

    -- Set once the loss has been ruled on through the normal loss path,
    -- so a redistillation loss cannot sit unclassified forever.
    loss_classified_at TIMESTAMPTZ,

    notes          TEXT NOT NULL DEFAULT '',
    recorded_by    UUID NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT redistillations_produced_chk CHECK (
        (laa_produced IS NULL) = (produced_on IS NULL)
    )
);

CREATE INDEX redistillations_open_idx
    ON redistillations (tenant_id, taken_on)
    WHERE laa_produced IS NULL;
CREATE INDEX redistillations_run_idx ON redistillations (distillation_run_id)
    WHERE distillation_run_id IS NOT NULL;
CREATE INDEX redistillations_source_idx ON redistillations (source_container_id, taken_on DESC);

CREATE TRIGGER redistillations_updated_at
    BEFORE UPDATE ON redistillations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE redistillations ENABLE ROW LEVEL SECURITY;
ALTER TABLE redistillations FORCE  ROW LEVEL SECURITY;
CREATE POLICY redistillations_tenant ON redistillations FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- A redistillation whose output nobody has recorded is alcohol that left
-- stock and never came back on the books. Worth one line on the
-- dashboard rather than a discrepancy discovered at period end.
ALTER TYPE alert_kind ADD VALUE IF NOT EXISTS 'redistillation_open';

GRANT SELECT, INSERT, UPDATE, DELETE ON redistillations TO stillhouse_app;
