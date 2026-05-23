DROP INDEX IF EXISTS fermentation_runs_yeast_lot_idx;
ALTER TABLE fermentation_runs DROP COLUMN IF EXISTS yeast_lot_id;
