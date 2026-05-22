-- Voidable bottling runs. Companion to packaging_removals.voided_at (000011);
-- same idea: the original row stays for audit, queries skip voided rows,
-- and the handler reverses the downstream side-effects (stamps, packaged
-- inventory, bulk container balance) so the system stays consistent.
ALTER TABLE bottling_runs
    ADD COLUMN voided_at     TIMESTAMPTZ,
    ADD COLUMN voided_by     UUID REFERENCES users(id),
    ADD COLUMN voided_reason TEXT NOT NULL DEFAULT '';

CREATE INDEX bottling_runs_active_idx
    ON bottling_runs (tenant_id, bottling_date DESC)
    WHERE voided_at IS NULL;
