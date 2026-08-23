-- 000044_lab_and_release: what the lab found, and who signed it off.
--
-- Two things that belong together and neither of which existed.
--
-- Lab results. A distillery measures things that never touch the B266 —
-- methanol, congeners, water chemistry — and keeps them in a notebook or
-- a spreadsheet beside a system that already knows which gauge, which
-- run and which cask they belong to. Attaching them here means a result
-- is findable from the thing it was measured on, and lands in the audit
-- binder with everything else about that period.
--
-- Batch release. Nothing stopped a lot leaving before anybody said it
-- could. That is not a CRA requirement — the Excise Act does not care
-- whether your methanol came back — but it is the control every food
-- safety programme assumes exists, and it is the difference between a
-- recall you can bound and one you cannot.
--
-- The gate is deliberately opt-in per tenant. A one-person distillery
-- that signs off by looking at the bottle should not be blocked by a
-- workflow built for a QA department, and a system that forces the
-- ceremony gets the ceremony performed rather than meant.

CREATE TYPE lab_result_status AS ENUM ('pass', 'fail', 'informational');

CREATE TABLE lab_results (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    -- What it was measured on. Exactly one is set; the CHECK below
    -- enforces it. Polymorphic rather than one table per subject because
    -- the result is the same shape wherever it came from, and the
    -- question "what do we know about this cask" should not need a union
    -- across four tables.
    container_id        UUID REFERENCES bulk_containers(id) ON DELETE CASCADE,
    production_gauge_id UUID REFERENCES production_gauges(id) ON DELETE CASCADE,
    bottling_run_id     UUID REFERENCES bottling_runs(id) ON DELETE CASCADE,
    mash_run_id         UUID REFERENCES mash_runs(id) ON DELETE CASCADE,

    -- What was measured, in the lab's own words. Not an enum: the set of
    -- things a distillery measures is open, differs by programme, and
    -- constraining it here would mean the interesting ones go in the
    -- notes field.
    analyte        TEXT NOT NULL CHECK (length(trim(analyte)) > 0),
    value          DOUBLE PRECISION,
    uom            TEXT NOT NULL DEFAULT '',
    -- The limit this was judged against, where there is one, so a
    -- reader a year later does not have to know what "good" was.
    spec_min       DOUBLE PRECISION,
    spec_max       DOUBLE PRECISION,
    status         lab_result_status NOT NULL DEFAULT 'informational',

    method         TEXT NOT NULL DEFAULT '',
    laboratory     TEXT NOT NULL DEFAULT '',
    -- The lab's own reference, so a certificate can be found.
    reference      TEXT NOT NULL DEFAULT '',
    sampled_on     DATE,
    reported_on    DATE NOT NULL DEFAULT CURRENT_DATE,
    notes          TEXT NOT NULL DEFAULT '',

    recorded_by    UUID NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT lab_results_one_subject_chk CHECK (
        (container_id IS NOT NULL)::int
      + (production_gauge_id IS NOT NULL)::int
      + (bottling_run_id IS NOT NULL)::int
      + (mash_run_id IS NOT NULL)::int = 1
    ),
    CONSTRAINT lab_results_spec_chk CHECK (
        spec_min IS NULL OR spec_max IS NULL OR spec_max >= spec_min
    )
);

CREATE INDEX lab_results_container_idx ON lab_results (container_id, reported_on DESC)
    WHERE container_id IS NOT NULL;
CREATE INDEX lab_results_run_idx ON lab_results (bottling_run_id, reported_on DESC)
    WHERE bottling_run_id IS NOT NULL;
CREATE INDEX lab_results_gauge_idx ON lab_results (production_gauge_id)
    WHERE production_gauge_id IS NOT NULL;
CREATE INDEX lab_results_mash_idx ON lab_results (mash_run_id)
    WHERE mash_run_id IS NOT NULL;

CREATE TRIGGER lab_results_updated_at
    BEFORE UPDATE ON lab_results
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE lab_results ENABLE ROW LEVEL SECURITY;
ALTER TABLE lab_results FORCE  ROW LEVEL SECURITY;
CREATE POLICY lab_results_tenant ON lab_results FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- ------------------------------------------------------------------------
-- Batch release: a named person saying a lot may go.
--
-- On packaged_inventory rather than on the bottling run, because the lot
-- is what leaves. A run can produce stock for two jurisdictions and one
-- of them can be held.
-- ------------------------------------------------------------------------
ALTER TABLE packaged_inventory
    ADD COLUMN released_at     TIMESTAMPTZ,
    ADD COLUMN released_by     UUID REFERENCES users(id),
    -- Always required when releasing. "Approved" is not a record of
    -- anything; what was checked is.
    ADD COLUMN release_notes   TEXT NOT NULL DEFAULT '',
    -- A hold is the opposite act and is equally attributable. A lot can
    -- be held after release — that is a recall in its early form — so
    -- these are separate columns rather than one status.
    ADD COLUMN held_at         TIMESTAMPTZ,
    ADD COLUMN held_by         UUID REFERENCES users(id),
    ADD COLUMN hold_reason     TEXT NOT NULL DEFAULT '';

-- Whether release is enforced. Off by default: turning it on is a
-- decision about how a distillery works, and defaulting it on would
-- block every existing tenant's next removal on a workflow nobody asked
-- for.
ALTER TABLE tenants
    ADD COLUMN require_batch_release BOOLEAN NOT NULL DEFAULT FALSE;

GRANT SELECT, INSERT, UPDATE, DELETE ON lab_results TO stillhouse_app;
