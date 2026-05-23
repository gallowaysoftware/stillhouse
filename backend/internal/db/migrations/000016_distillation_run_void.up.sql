-- Voidable distillation runs. Mirrors stages 44 (removal void) and 48
-- (bottling run void). Required so misrecorded production gauges can be
-- corrected without orphaning the run record.
ALTER TABLE distillation_runs
    ADD COLUMN voided_at     TIMESTAMPTZ,
    ADD COLUMN voided_by     UUID REFERENCES users(id),
    ADD COLUMN voided_reason TEXT NOT NULL DEFAULT '';

CREATE INDEX distillation_runs_active_idx
    ON distillation_runs (tenant_id, run_date DESC)
    WHERE voided_at IS NULL;
