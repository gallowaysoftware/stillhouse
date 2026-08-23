-- 000061_wip_production: spirit gauged into work in progress.
--
-- Stage 178 emitted the transfer *out* of WIP at bottling. Its twin has
-- had no line since 000040, for a stated reason: valuing spirit gauged
-- into bulk means walking forward from the mashes to each gauge and
-- apportioning a mash that fed several of them, and that apportionment is
-- a convention Stillhouse does not have.
--
-- That argument was right about the convention and wrong about the
-- conclusion. Stillhouse's answer everywhere else it must not guess is not
-- to omit the figure — it is to make the licensee state the rule and to
-- refuse, by name, until they do. That is what journal_accounts does with
-- the chart of accounts, what the retention window does with a policy, and
-- what the rate table does with a notice it cannot cite. WIP is the same
-- shape and had been treated as a special case.
--
-- The convention turns out to be narrower than 000040 assumed, because
-- most of the walk already has a recorded basis:
--
--   mash → fermentation    fermentation_runs.initial_volume_l, when the
--                          mash fed more than one. A mash that fed exactly
--                          one fermentation needs no apportionment at all.
--   fermentation → run     distillation_charges.volume_charged_l and
--                          abv_pct, both recorded at charge time.
--   run → gauge            production_gauges is UNIQUE on
--                          distillation_run_id: one gauge per run, so
--                          nothing to apportion.
--
-- Exactly one choice is left, and it is a real one an accountant would
-- want stated: whether a fermentation's cost follows the *volume* charged
-- to a still or the *alcohol* charged to it. A low-wines run and a spirit
-- run drawing the same litres do not carry the same alcohol, and which
-- one carries the cost is the licensee's policy, not ours.

CREATE TYPE wip_charge_basis AS ENUM (
    -- Cost follows litres of wash charged to the still.
    'charged_volume',
    -- Cost follows litres of absolute alcohol charged.
    'charged_laa'
);

-- NULL means unset, and unset means refuse. Deliberately not defaulted:
-- a default here would be Stillhouse choosing the convention and then
-- reporting the result as though the licensee had. See the note above.
ALTER TABLE tenants ADD COLUMN wip_charge_basis wip_charge_basis;

COMMENT ON COLUMN tenants.wip_charge_basis IS
    'How a fermentation''s cost is apportioned across the distillation runs it was charged to. NULL means the licensee has not stated one, and WIP production value is refused rather than guessed.';

-- The event this unlocks. 000052 deliberately left it out on the grounds
-- that adding an enum value nothing emits would be the first half of doing
-- it anyway; something emits it now.
ALTER TYPE journal_event_kind ADD VALUE IF NOT EXISTS 'wip_production';
