-- The enum value is not removed: PostgreSQL has no ALTER TYPE ... DROP
-- VALUE, and a rows-referencing-it check that passed at down-migration
-- time would say nothing about the next insert. The column goes; the
-- vocabulary stays, exactly as 000052's down does.
ALTER TABLE tenants DROP COLUMN IF EXISTS wip_charge_basis;
DROP TYPE IF EXISTS wip_charge_basis;
