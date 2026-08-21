DROP INDEX IF EXISTS bulk_movements_unclassified_loss_idx;
ALTER TABLE bulk_movements DROP CONSTRAINT IF EXISTS bulk_movements_relief_needs_authority;
ALTER TABLE bulk_movements
    DROP COLUMN IF EXISTS loss_classified_at,
    DROP COLUMN IF EXISTS loss_classified_by,
    DROP COLUMN IF EXISTS loss_treatment_authority,
    DROP COLUMN IF EXISTS loss_duty_treatment;
DROP TYPE IF EXISTS loss_duty_treatment;
