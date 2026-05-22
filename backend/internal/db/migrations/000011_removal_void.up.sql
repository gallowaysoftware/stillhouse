-- Voidable removals. Operators occasionally record the wrong bottle count or
-- the wrong removal; rather than mutating the original row (which would
-- corrupt audit history) we mark it voided and let queries filter it out.
ALTER TABLE packaging_removals
    ADD COLUMN voided_at     TIMESTAMPTZ,
    ADD COLUMN voided_by     UUID REFERENCES users(id),
    ADD COLUMN voided_reason TEXT NOT NULL DEFAULT '';

-- Partial index so the common "show active removals" query stays fast.
CREATE INDEX packaging_removals_active_idx
    ON packaging_removals (tenant_id, removal_date DESC)
    WHERE voided_at IS NULL;
