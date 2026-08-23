-- 000046_fraction_naming: a name that says which scale it is on.
--
-- Stillhouse holds two kinds of proportion and calls them the same
-- thing. abv_pct is 0–100. extract_pct, moisture_pct and the three
-- recipe efficiencies are fractions in [0,1]. Same suffix, same product,
-- a hundredfold apart — and mash_efficiency_pct (a fraction) sat beside
-- a computed MashEfficiency.Pct (a percentage), the same concept at two
-- scales, three letters apart.
--
-- The CHECK constraints catch one direction and only one. An efficiency
-- of 78 is obviously a percentage in a fraction's slot and is rejected.
-- A strength of 0.40 is a legal percentage — a very weak beer — so
-- nothing rejects it, and that is the direction that understates duty:
-- forty percent read as four tenths of one percent prices a bottle at a
-- hundredth of what it owes.
--
-- Types close that in Go (internal/units, stage 169). This closes it in
-- the name a human reads, which is where the mistake is actually made:
-- in a query somebody writes at 11pm against a column called _pct.
--
-- Renaming rather than adding: two columns for one figure is how the
-- ambiguity becomes a divergence.

ALTER TABLE materials      RENAME COLUMN extract_pct  TO extract_fraction;
ALTER TABLE materials      RENAME COLUMN moisture_pct TO moisture_fraction;

ALTER TABLE recipe_versions RENAME COLUMN mash_efficiency_pct       TO mash_efficiency_fraction;
ALTER TABLE recipe_versions RENAME COLUMN ferment_efficiency_pct    TO ferment_efficiency_fraction;
ALTER TABLE recipe_versions RENAME COLUMN distillation_recovery_pct TO distillation_recovery_fraction;

-- The CHECK constraints carry the old names in their own names, which
-- would make a future reader hunt for a column that no longer exists.
ALTER TABLE recipe_versions
    DROP CONSTRAINT IF EXISTS recipe_versions_mash_efficiency_pct_check,
    DROP CONSTRAINT IF EXISTS recipe_versions_ferment_efficiency_pct_check,
    DROP CONSTRAINT IF EXISTS recipe_versions_distillation_recovery_pct_check;

-- Re-stated under names that match their columns. Still the only
-- automatic protection against a percentage in a fraction's slot.
ALTER TABLE recipe_versions
    ADD CONSTRAINT recipe_versions_mash_efficiency_fraction_check
        CHECK (mash_efficiency_fraction > 0 AND mash_efficiency_fraction <= 1),
    ADD CONSTRAINT recipe_versions_ferment_efficiency_fraction_check
        CHECK (ferment_efficiency_fraction > 0 AND ferment_efficiency_fraction <= 1),
    ADD CONSTRAINT recipe_versions_distillation_recovery_fraction_check
        CHECK (distillation_recovery_fraction > 0 AND distillation_recovery_fraction <= 1);

COMMENT ON COLUMN materials.extract_fraction IS
    'Fermentable extract as a proportion of mass, in [0,1]. NOT a percentage.';
COMMENT ON COLUMN materials.moisture_fraction IS
    'Moisture as a proportion of mass, in [0,1]. NOT a percentage.';
COMMENT ON COLUMN recipe_versions.mash_efficiency_fraction IS
    'Proportion of extract freed in the mash, in [0,1]. NOT a percentage.';
