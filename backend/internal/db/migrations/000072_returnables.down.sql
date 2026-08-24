ALTER TABLE kegs DROP CONSTRAINT IF EXISTS kegs_kegs_have_capacity;
ALTER TABLE kegs DROP CONSTRAINT IF EXISTS kegs_capacity_positive;
DROP INDEX IF EXISTS kegs_kind_idx;
ALTER TABLE kegs DROP CONSTRAINT IF EXISTS kegs_only_kegs_are_filled;
ALTER TABLE kegs DROP CONSTRAINT IF EXISTS kegs_only_kegs_hold_spirits;
ALTER TABLE kegs DROP COLUMN IF EXISTS kind;
DROP TYPE IF EXISTS returnable_kind;
