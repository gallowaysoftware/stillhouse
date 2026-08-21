-- ------------------------------------------------------------------------
-- The acknowledgement between "here are your figures" and a filed return.
--
-- Stage 104 got the hard part right: Stillhouse never submits to CRA and
-- says so on the screen. What was missing was the step in between — a
-- moment where a named person says they have checked the figures against
-- their own records, recorded with the date.
--
-- The exact wording is stored with it, not just a boolean. A tick box
-- whose text changed in a later release proves nothing about what someone
-- agreed to two years ago, and this row exists precisely to be read years
-- later.
-- ------------------------------------------------------------------------

ALTER TABLE b266_periods
    ADD COLUMN filing_acknowledged_at   TIMESTAMPTZ,
    ADD COLUMN filing_acknowledged_by   UUID REFERENCES users(id),
    ADD COLUMN filing_acknowledgement   TEXT NOT NULL DEFAULT '';

-- Periods submitted before this migration have no confirmation, because
-- nobody was asked for one. They are marked as exactly that rather than
-- backfilled with a statement nobody agreed to — the sentence is what an
-- auditor reads, and "nobody confirmed this" is the true answer.
UPDATE b266_periods
SET filing_acknowledged_at = COALESCE(submitted_at, updated_at),
    filing_acknowledged_by = submitted_by,
    filing_acknowledgement =
        'Submitted before Stillhouse asked for a confirmation. No statement was shown to, or agreed by, anyone for this period.'
WHERE status = 'submitted'
  AND filing_acknowledged_at IS NULL;

-- A submitted period must carry its acknowledgement. Enforced here rather
-- than only in the handler because the guarantee this row makes — somebody
-- said they had checked — is worthless if any other path can set the
-- status without it.
--
-- filing_acknowledged_by is not required: a period backfilled above may
-- have no submitted_by to attribute it to, and inventing an author would
-- be worse than admitting there isn't one. The timestamp and the wording
-- are required, because those are what the row is read for.
ALTER TABLE b266_periods
    ADD CONSTRAINT b266_periods_acknowledgement_is_complete
    CHECK (status <> 'submitted'
        OR (filing_acknowledged_at IS NOT NULL
            AND length(trim(filing_acknowledgement)) > 0));

COMMENT ON COLUMN b266_periods.filing_acknowledgement IS
    'The exact wording the person agreed to, frozen. A boolean would not survive a later change to the text.';
