-- ------------------------------------------------------------------------
-- The rest of B266 page 3.
--
-- Against EDM10-1-7 the bulk section was missing: imported bulk spirits;
-- bulk received from other spirits licensees and from licensed users;
-- packaged spirits returned to bulk; bulk removed for delivery to spirits
-- licensees and licensed users; spirits denatured to DA and to SDA;
-- exported bulk; and bulk returned to production.
--
-- It is worse than a missing line, though. Nothing in the application ever
-- created a transfer_in_bond, transfer_out_in_bond, destruction or
-- loss_unaccounted movement either: the report has had lines for all four
-- since it was written, and they were structurally always zero. A
-- distillery receiving spirit in bond, shipping it out in bond, destroying
-- spirit under CRA supervision or writing off an unaccounted loss had no
-- path at all, and the return quietly said none of it happened.
--
-- Marked special containers are deliberately not here: they are packaging,
-- not bulk, and they need their own model (PLAN B3).
-- ------------------------------------------------------------------------

-- Receipts — alcohol arriving on the premises from outside.
ALTER TYPE bulk_movement_reason ADD VALUE IF NOT EXISTS 'import_received';
ALTER TYPE bulk_movement_reason ADD VALUE IF NOT EXISTS 'received_from_spirits_licensee';
ALTER TYPE bulk_movement_reason ADD VALUE IF NOT EXISTS 'received_from_licensed_user';
ALTER TYPE bulk_movement_reason ADD VALUE IF NOT EXISTS 'packaged_returned_to_bulk';

-- Dispositions — alcohol leaving.
ALTER TYPE bulk_movement_reason ADD VALUE IF NOT EXISTS 'delivered_to_spirits_licensee';
ALTER TYPE bulk_movement_reason ADD VALUE IF NOT EXISTS 'delivered_to_licensed_user';
ALTER TYPE bulk_movement_reason ADD VALUE IF NOT EXISTS 'exported';
ALTER TYPE bulk_movement_reason ADD VALUE IF NOT EXISTS 'denatured_da';
ALTER TYPE bulk_movement_reason ADD VALUE IF NOT EXISTS 'denatured_sda';
ALTER TYPE bulk_movement_reason ADD VALUE IF NOT EXISTS 'returned_to_production';

-- Who the alcohol came from or went to, and the document that says so.
--
-- EDM10-1-7 does not want a bare quantity on the "received from other
-- spirits licensees" line: the counterparty and their licence number are
-- what makes the movement traceable to the other end of it, which is the
-- whole point of an in-bond transfer being reportable by both parties.
--
-- On bulk_movements rather than a side table because every reportable
-- movement can carry them, and because the ledger row is what a reviewer
-- reads when reconciling a line back to its documents.
ALTER TABLE bulk_movements
    ADD COLUMN counterparty_name      TEXT NOT NULL DEFAULT '',
    ADD COLUMN counterparty_licence_no TEXT NOT NULL DEFAULT '',
    ADD COLUMN document_reference     TEXT NOT NULL DEFAULT '';

-- The determination behind an external movement. Receiving spirit in bond
-- means gauging it, and that gauge is subject to the same instrument
-- approval as any other (EDM3-1-1 para 24) — see migration 000027.
ALTER TABLE bulk_movements
    ADD COLUMN temperature_c          DOUBLE PRECISION,
    ADD COLUMN observed_volume_l      DOUBLE PRECISION,
    ADD COLUMN observed_density_kg_m3 DOUBLE PRECISION,
    ADD COLUMN volume_factor_c        DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    ADD COLUMN strength_source        strength_source NOT NULL DEFAULT 'uncorrected',
    ADD COLUMN volume_instrument_id      UUID REFERENCES instruments(id) ON DELETE RESTRICT,
    ADD COLUMN strength_instrument_id    UUID REFERENCES instruments(id) ON DELETE RESTRICT,
    ADD COLUMN temperature_instrument_id UUID REFERENCES instruments(id) ON DELETE RESTRICT;

-- Who recorded it. Movements written as a side effect of another action
-- (a barrel fill, a bottling run) already have an author on the parent
-- row; a movement recorded directly does not, and an external disposition
-- is exactly the kind an auditor asks about.
ALTER TABLE bulk_movements
    ADD COLUMN recorded_by UUID REFERENCES users(id);

-- Packaged spirits returned to bulk decrement packaged inventory, so the
-- ledger row points at the lot it came out of.
ALTER TABLE bulk_movements
    ADD COLUMN packaged_inventory_id UUID REFERENCES packaged_inventory(id),
    ADD COLUMN bottles_unpackaged    INTEGER
        CHECK (bottles_unpackaged IS NULL OR bottles_unpackaged > 0);
