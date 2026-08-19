-- Mash bench: cereal species on materials, plus a measured wash volume.
--
-- Gelatinisation temperature is a property of the starch granule, so it
-- varies by species — barley is done by 62 °C, maize needs 80 °C. material
-- kind ('grain' / 'malt') doesn't carry that, so guidance couldn't be
-- derived from a grain bill. This adds the species.
--
-- Left nullable rather than defaulted: an unset cereal means "we don't
-- know", and the bench reports that instead of guessing a range.

CREATE TYPE cereal AS ENUM (
    'barley',
    'wheat',
    'rye',
    'maize',
    'rice',
    'oat',
    'other'
);

ALTER TABLE materials ADD COLUMN cereal cereal;

COMMENT ON COLUMN materials.cereal IS
    'Grain species, for gelatinisation guidance. NULL = unknown; the mash bench reports unknown rather than assuming a range.';

-- Distilling washes are not lautered and boiled, so the wash volume is
-- close to water + grain displacement — but close is not measured, and
-- conversion efficiency is computed against it.
ALTER TYPE mash_metric_kind ADD VALUE IF NOT EXISTS 'wash_volume_l';
