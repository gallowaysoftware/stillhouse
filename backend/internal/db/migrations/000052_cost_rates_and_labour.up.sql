-- What a batch cost, beyond the grain.
--
-- BottlingRunCost has been direct materials only, and said so on every
-- line. That is honest but it is not a cost: a distillery whose cost of
-- sales is the price of barley is one that thinks its whisky costs eight
-- dollars a bottle. Stage 161 left the two WIP journal kinds unemitted
-- rather than invent a valuation, and migration 000040 wrote down why —
-- valuing them needs labour, overhead absorption and a WIP convention.
-- This is those three.
--
-- The rates are the licensee's own policy, not a figure Stillhouse knows.
-- Nothing here has a default: a rate that has not been set makes the
-- component unavailable and says so, rather than absorbing zero overhead
-- and reporting a full cost that is really a partial one.

-- How overhead is spread over what was made. Three conventions, and the
-- licensee picks one — there is no correct answer, only a stated one.
CREATE TYPE overhead_basis AS ENUM (
    -- A percentage of direct materials. Crude, and the easiest to keep
    -- current when nobody is tracking hours.
    'per_material_dollar',
    -- A rate per labour hour recorded against the batch. The textbook
    -- one, and it needs hours to actually be recorded.
    'per_labour_hour',
    -- A rate per litre of absolute alcohol produced. Fits a distillery
    -- whose overhead is mostly the still: energy and water scale with
    -- what came off it, not with who was standing there.
    'per_laa'
);

-- Effective-dated, not columns on the tenant.
--
-- A rate is a fact about a period. Changing a bare column would restate
-- every batch ever costed, including those inside a filed return and
-- those an accountant has already taken into a set of books — the same
-- failure as a possession flag with no movement behind it (stage 176):
-- internally consistent, silently different from what was signed.
CREATE TABLE cost_rates (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    effective_from  DATE NOT NULL,
    -- CAD per hour. NULL means the licensee has not set one, which is
    -- different from zero and is reported differently.
    labour_rate_cad_per_hour NUMERIC(12, 4) CHECK (labour_rate_cad_per_hour >= 0),
    overhead_basis  overhead_basis,
    -- Read against the basis: a fraction of direct materials, CAD per
    -- labour hour, or CAD per LAA.
    overhead_rate   NUMERIC(12, 6) CHECK (overhead_rate >= 0),
    notes           TEXT NOT NULL DEFAULT '',
    created_by      UUID NOT NULL REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- A basis without a rate, or a rate without a basis, is half a policy
    -- and would silently absorb nothing.
    CONSTRAINT cost_rates_overhead_complete CHECK (
        (overhead_basis IS NULL AND overhead_rate IS NULL)
     OR (overhead_basis IS NOT NULL AND overhead_rate IS NOT NULL)
    ),
    UNIQUE (tenant_id, effective_from)
);

ALTER TABLE cost_rates ENABLE ROW LEVEL SECURITY;
ALTER TABLE cost_rates FORCE ROW LEVEL SECURITY;
CREATE POLICY cost_rates_tenant_isolation ON cost_rates
    USING (tenant_id = current_setting('app.current_tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.current_tenant_id')::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON cost_rates TO stillhouse_app;

CREATE INDEX cost_rates_effective_idx ON cost_rates (tenant_id, effective_from DESC);

-- Hours worked on a batch.
--
-- Elapsed time is not effort — a fermentation runs for five days and
-- nobody is standing over it — so hours are recorded rather than derived
-- from a run's start and end. Recorded against whichever thing the work
-- was actually on, which is why the subject is a set of nullable
-- references with exactly one filled, the same shape as lab_results.
CREATE TABLE labour_entries (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,

    mash_run_id         UUID REFERENCES mash_runs(id)         ON DELETE CASCADE,
    distillation_run_id UUID REFERENCES distillation_runs(id) ON DELETE CASCADE,
    bottling_run_id     UUID REFERENCES bottling_runs(id)     ON DELETE CASCADE,
    work_order_id       UUID REFERENCES work_orders(id)       ON DELETE CASCADE,

    worked_on      DATE NOT NULL DEFAULT CURRENT_DATE,
    hours          NUMERIC(8, 2) NOT NULL CHECK (hours > 0 AND hours <= 24),
    -- Who did it. Not necessarily who typed it.
    worked_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    worked_by_name TEXT NOT NULL DEFAULT '',
    -- An override for work that is not at the standard rate — a
    -- contractor, an apprentice, a Sunday. NULL uses the rate in force on
    -- worked_on.
    rate_cad_per_hour NUMERIC(12, 4) CHECK (rate_cad_per_hour >= 0),
    notes          TEXT NOT NULL DEFAULT '',
    recorded_by    UUID NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT labour_entries_one_subject CHECK (
        (mash_run_id         IS NOT NULL)::int
      + (distillation_run_id IS NOT NULL)::int
      + (bottling_run_id     IS NOT NULL)::int
      + (work_order_id       IS NOT NULL)::int = 1
    )
);

ALTER TABLE labour_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE labour_entries FORCE ROW LEVEL SECURITY;
CREATE POLICY labour_entries_tenant_isolation ON labour_entries
    USING (tenant_id = current_setting('app.current_tenant_id')::uuid)
    WITH CHECK (tenant_id = current_setting('app.current_tenant_id')::uuid);
GRANT SELECT, INSERT, UPDATE, DELETE ON labour_entries TO stillhouse_app;

CREATE INDEX labour_entries_mash_idx     ON labour_entries (mash_run_id)         WHERE mash_run_id IS NOT NULL;
CREATE INDEX labour_entries_dist_idx     ON labour_entries (distillation_run_id) WHERE distillation_run_id IS NOT NULL;
CREATE INDEX labour_entries_bottling_idx ON labour_entries (bottling_run_id)     WHERE bottling_run_id IS NOT NULL;
CREATE INDEX labour_entries_wo_idx       ON labour_entries (work_order_id)       WHERE work_order_id IS NOT NULL;

-- One of the two journal kinds migration 000040 left out, now that there
-- is something behind it: bottling moves alcohol out of work in progress
-- at the full cost of the run that drew it, which is a figure this
-- migration makes computable.
--
-- Its twin — spirit gauged into WIP at production — is deliberately still
-- absent. Valuing that means walking forward from the mashes to each
-- gauge and apportioning a mash that fed several of them, which is a
-- convention Stillhouse does not have and must not invent. 000040's
-- argument stands for it: an export that posted a made-up WIP figure
-- would reconcile, and nobody would look at it again. Adding an enum
-- value nothing emits would be the first half of doing it anyway. See
-- PLAN E7.
ALTER TYPE journal_event_kind ADD VALUE IF NOT EXISTS 'wip_to_finished_goods';
