-- 000069_recipe_plant: which vessel a recipe is mashed in.
--
-- Stage 204 turns a forecast into batches of a recipe, and reports the
-- batch count as a decimal on purpose: 2.4 batches is three mashes on a
-- tun of a given size, and the rounding is a fact about the plant rather
-- than about the arithmetic. Stillhouse has an equipment register with
-- capacities in it and no way to say which vessel a recipe uses, so the
-- rounding could not be done.
--
-- Nullable and operator-supplied, and unset means the mash count is
-- refused rather than guessed. Picking the largest mash tun would be
-- right at a distillery with one and wrong at any distillery that has
-- reason to own two.

ALTER TABLE recipe_versions ADD COLUMN mash_equipment_id UUID
    REFERENCES equipment(id) ON DELETE SET NULL;

CREATE INDEX recipe_versions_plant_idx ON recipe_versions (mash_equipment_id)
    WHERE mash_equipment_id IS NOT NULL;

COMMENT ON COLUMN recipe_versions.mash_equipment_id IS
    'The vessel this recipe is mashed in. NULL means unstated, and the number of mashes a requirement implies is refused rather than assumed from whatever plant happens to be largest.';
