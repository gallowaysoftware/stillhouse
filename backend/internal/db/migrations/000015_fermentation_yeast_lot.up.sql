-- Mirror migration 000013: link fermentations to a specific yeast lot for
-- end-to-end input traceability. Optional FK; existing rows stay valid.
ALTER TABLE fermentation_runs
    ADD COLUMN yeast_lot_id UUID REFERENCES material_lots(id) ON DELETE RESTRICT;

CREATE INDEX fermentation_runs_yeast_lot_idx ON fermentation_runs (yeast_lot_id)
    WHERE yeast_lot_id IS NOT NULL;
