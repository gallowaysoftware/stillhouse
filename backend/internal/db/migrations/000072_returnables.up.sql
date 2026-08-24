-- 000072_returnables: the register was never only about kegs. PLAN D5.
--
-- Stage 199 built an asset register with a deposit ledger and an enforced
-- fill/return cycle, and called the table kegs because kegs were what it
-- was for. A pallet, a crate and a CO2 cylinder are the same problem
-- minus the contents: a serial, a deposit, a place it currently is, and a
-- cycle of going out and coming back.
--
-- So this widens the register rather than building a second one. Two
-- tables holding the same shape would drift, and the deposit liability
-- would have to be summed across both by somebody who remembered.
--
-- The table keeps the name `kegs`, which is now narrower than its
-- contents. That is a wart and it is the cheaper of two: renaming it
-- means renaming the service, the proto, the page and the client, and a
-- name that is slightly wrong in one place beats a rename that touches
-- twenty. The kind column carries the real answer, and the UI says
-- "returnables".

CREATE TYPE returnable_kind AS ENUM (
    'keg',
    'pallet',
    'crate',
    'gas_cylinder',
    'other'
);

ALTER TABLE kegs ADD COLUMN kind returnable_kind NOT NULL DEFAULT 'keg';

-- Only a keg holds spirits. The existing CHECKs already tie contents to
-- status; this ties them to kind as well, because a crate that claimed a
-- marked special container would put alcohol in a stack of wood.
ALTER TABLE kegs ADD CONSTRAINT kegs_only_kegs_hold_spirits
    CHECK (kind = 'keg'
           OR (marked_container_id IS NULL AND packaged_inventory_id IS NULL));

-- And a non-keg cannot be 'filled', which is a state about contents.
-- 'at_customer' still applies: a pallet goes out and comes back exactly
-- as a keg does, which is the whole reason this is one register.
ALTER TABLE kegs ADD CONSTRAINT kegs_only_kegs_are_filled
    CHECK (kind = 'keg' OR status <> 'filled');

-- Capacity was NOT NULL and > 0, which is right for a keg and meaningless
-- for a pallet. It becomes optional, still positive when given, and still
-- required for a keg — because a keg's capacity is what decides whether
-- its contents are a marked special container or packaged spirits
-- (EDM3-8-1's 100 L threshold, stage 199), so a keg without one cannot
-- have its contents recorded anywhere.
ALTER TABLE kegs ALTER COLUMN capacity_l DROP NOT NULL;
ALTER TABLE kegs DROP CONSTRAINT IF EXISTS kegs_capacity_l_check;
ALTER TABLE kegs ADD CONSTRAINT kegs_capacity_positive
    CHECK (capacity_l IS NULL OR capacity_l > 0);
ALTER TABLE kegs ADD CONSTRAINT kegs_kegs_have_capacity
    CHECK (kind <> 'keg' OR capacity_l IS NOT NULL);

CREATE INDEX kegs_kind_idx ON kegs (tenant_id, kind);

COMMENT ON TABLE kegs IS
    'Returnable assets: kegs, pallets, crates, gas cylinders. Named for the case it was built for; see the kind column.';
