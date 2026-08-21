-- ------------------------------------------------------------------------
-- Losses classified by duty treatment.
--
-- `bulk_losses_laa` was one number. Under EDM3-4-1 the treatment diverges
-- sharply: a destruction approved by CRA is relieved, while spirits that
-- simply cannot be accounted for are duty-payable and cost real money.
-- Collapsing the two produces a plausible total and the wrong duty.
--
-- Stage 145 already took the misclassified count variances out of losses —
-- a discrepancy between book and physical is a reason-coded adjustment, not
-- a loss. What is left here is genuine loss, and it still needs saying
-- which kind.
--
-- Three states, not two. `unclassified` is the honest default: Stillhouse
-- does not know whether a given evaporation loss is relieved, and the
-- barrel regauge that wrote it did not ask. Guessing either way would put
-- a number on a return that nobody chose, so an unclassified loss is
-- reported as unclassified and the return says it cannot be filed until
-- somebody decides.
-- ------------------------------------------------------------------------

CREATE TYPE loss_duty_treatment AS ENUM (
    -- Nobody has said yet. Reported on the return as outstanding.
    'unclassified',
    -- Relieved under EDM3-4-1. Relief rests on something — a CRA approval
    -- for a destruction, or the provision relied on — and the CHECK below
    -- makes naming it unavoidable rather than merely encouraged.
    'relieved',
    -- Duty is payable on these litres.
    'dutiable'
);

ALTER TABLE bulk_movements
    ADD COLUMN loss_duty_treatment      loss_duty_treatment NOT NULL DEFAULT 'unclassified',
    -- The CRA approval reference, or the basis relied on. Free text
    -- deliberately: the authority for relief is a document number in some
    -- cases and a provision in others, and forcing one shape would push
    -- operators into recording the wrong thing.
    ADD COLUMN loss_treatment_authority TEXT NOT NULL DEFAULT '',
    ADD COLUMN loss_classified_by       UUID REFERENCES users(id),
    ADD COLUMN loss_classified_at       TIMESTAMPTZ;

-- Relief that rests on nothing is not relief. Enforced in the database so
-- it holds for every path that ever writes one of these rows, including
-- the ones nobody has written yet.
ALTER TABLE bulk_movements
    ADD CONSTRAINT bulk_movements_relief_needs_authority
    CHECK (loss_duty_treatment <> 'relieved'
           OR length(trim(loss_treatment_authority)) > 0);

-- Finding what still needs classifying before a return can be filed is the
-- query an operator runs at period end, so it gets an index rather than a
-- sequential scan over the whole ledger.
CREATE INDEX bulk_movements_unclassified_loss_idx
    ON bulk_movements (tenant_id, occurred_at)
    WHERE loss_duty_treatment = 'unclassified'
      AND reason IN ('loss_evaporation', 'loss_unaccounted', 'destruction');
