-- 000047_locations: where, within a licensee.
--
-- One tenant was one licence with no place inside it. bulk_containers
-- carried a free-text `location` and everything else carried nothing, so
-- a distillery with a production site and a separate rackhouse across
-- town had one undifferentiated pile of alcohol.
--
-- This is a compliance limit before it is a convenience one:
--
--   * An excise warehouse licence can specify multiple premises
--     (EDM8-1-1), and which premises a movement happened at is part of
--     the record.
--   * The 30% single-retail-store supply rule (EDM8-1-1 ¶20) is computed
--     per premises. Without a premises dimension the figure cannot be
--     computed at all — which is why `I2` needs this.
--   * A separate return per branch or division (B269) is `B5`, and needs
--     something to divide by.
--
-- The design decision worth stating: a location is where something *is*,
-- not who owns it. Row-level security stays keyed on tenant_id and
-- nothing here narrows it. A location is a dimension within a tenant,
-- and a user who can see the tenant can see all of its locations —
-- adding a second isolation boundary would double the number of ways to
-- get isolation wrong for a gain nobody has asked for.

CREATE TABLE locations (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    -- The address as it appears on the licence, which is what makes this
    -- a premises rather than a label.
    address        TEXT NOT NULL DEFAULT '',
    -- Which licence covers this premises. Nullable because a location
    -- can exist before the register is filled in, and because not every
    -- location is licensed — an office is a location.
    excise_licence_id UUID REFERENCES excise_licences(id) ON DELETE SET NULL,
    -- Whether packaged spirits may be sold to the public here. The 30%
    -- rule turns on this.
    retail_store   BOOLEAN NOT NULL DEFAULT FALSE,
    -- Exactly one location per tenant is the default: what a movement
    -- with no location named is taken to have happened at. Enforced by
    -- the partial unique index below.
    is_default     BOOLEAN NOT NULL DEFAULT FALSE,
    notes          TEXT NOT NULL DEFAULT '',
    archived_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);

CREATE UNIQUE INDEX locations_one_default_idx
    ON locations (tenant_id) WHERE is_default;

CREATE TRIGGER locations_updated_at
    BEFORE UPDATE ON locations
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE locations ENABLE ROW LEVEL SECURITY;
ALTER TABLE locations FORCE  ROW LEVEL SECURITY;
CREATE POLICY locations_tenant ON locations FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- Every tenant gets one, named from the tenant itself, and it is the
-- default. Backfilling rather than leaving them empty means nothing has
-- to handle "no location at all" as a separate case, and an install that
-- never adds a second location behaves exactly as it did before.
INSERT INTO locations (tenant_id, name, is_default, notes)
SELECT id, name, TRUE,
       'Created automatically when locations were introduced. Rename it to '
       || 'whatever you call the site, and add others if you have them.'
FROM tenants;

-- Deliberately NOT a trigger.
--
-- The obvious move is an AFTER INSERT trigger on tenants, and it was the
-- first attempt: "a tenant has exactly one default location" is an
-- invariant of the data rather than of any particular path into it. It
-- does not work, and the reason is worth recording.
--
-- Signup creates a tenant in a transaction that has no tenant context —
-- there is no id to scope by until the INSERT returns. A trigger firing
-- there writes to locations, which FORCEs row-level security, and the
-- policy refuses it. Exactly the break stage 138 fixed for audit_events,
-- and caught here by the test stage 153 un-skipped.
--
-- Making the trigger SECURITY DEFINER would fix it by punching a hole in
-- the tenant boundary to save typing in three call sites, which is the
-- trade stage 152 exists to refuse. So the insert lives in the callers,
-- after SetTenantContext, and a schema test asserts every tenant has
-- exactly one default rather than a trigger enforcing it.

-- Where a container physically is.
--
-- The existing free-text `location` column stays. It holds rack
-- positions and floor markings — "Row 4, Level 2" — which is a different
-- question from which premises the cask is at, and folding the two
-- together would lose the finer one.
ALTER TABLE bulk_containers
    ADD COLUMN location_id UUID REFERENCES locations(id) ON DELETE RESTRICT;

UPDATE bulk_containers bc
SET location_id = l.id
FROM locations l
WHERE l.tenant_id = bc.tenant_id AND l.is_default;

CREATE INDEX bulk_containers_location_idx ON bulk_containers (location_id)
    WHERE location_id IS NOT NULL;

-- Where packaged stock sits, which is what the 30% rule counts.
ALTER TABLE packaged_inventory
    ADD COLUMN location_id UUID REFERENCES locations(id) ON DELETE RESTRICT;

UPDATE packaged_inventory pi
SET location_id = l.id
FROM locations l
WHERE l.tenant_id = pi.tenant_id AND l.is_default;

CREATE INDEX packaged_inventory_location_idx ON packaged_inventory (location_id)
    WHERE location_id IS NOT NULL;

-- Which premises a removal left from. Needed by the 30% computation and
-- by any per-premises return.
ALTER TABLE packaging_removals
    ADD COLUMN location_id UUID REFERENCES locations(id) ON DELETE RESTRICT;

UPDATE packaging_removals pr
SET location_id = l.id
FROM locations l
WHERE l.tenant_id = pr.tenant_id AND l.is_default;

CREATE INDEX packaging_removals_location_idx ON packaging_removals (location_id, removal_date)
    WHERE location_id IS NOT NULL;

GRANT SELECT, INSERT, UPDATE, DELETE ON locations TO stillhouse_app;
