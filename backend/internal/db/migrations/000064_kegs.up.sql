-- 000064_kegs: the keg register. PLAN D5.
--
-- The distinction this migration exists to hold is between the VESSEL and
-- its CONTENTS, and getting it wrong would put alcohol on a filed return
-- twice.
--
-- A keg with spirits in it is ALREADY recorded somewhere, and a register
-- that also recorded what was in it would be a second copy of the same
-- alcohol. The obvious implementation — put volume_l and abv_pct on the
-- keg — is exactly that mistake, and it would put the same LAA on a filed
-- return twice.
--
-- Which table holds the contents depends on the keg's size, and the
-- threshold is not ours. A marked special container under EDM3-8-1 is
-- 100 to 1500 litres (the CHECK on marked_special_containers says so).
-- Spirits in anything smaller are packaged, exactly as a bottle is — a
-- 50 L keg is a large bottle as far as the Act is concerned, and its
-- contents live in packaged_inventory.
--
-- So a keg points at one or the other, and which one is decided by its
-- capacity rather than by whoever is filling it.
--
-- So a keg here holds no alcohol figures at all. It is an asset with a
-- serial number, a deposit, and a place it currently is. When it is full
-- it points at the marked container row that holds the spirits, and the
-- spirits are counted there and only there.
--
-- What the register adds that nothing else has: where the physical asset
-- is, what deposit is outstanding on it, and how long its contents have
-- been sitting.

CREATE TYPE keg_status AS ENUM (
    -- Ours, empty, clean, ready to fill.
    'available',
    -- Ours, full. The contents are a marked special container.
    'filled',
    -- Gone out. The deposit is outstanding.
    'at_customer',
    -- Back from a customer and not yet cleaned. Distinct from available
    -- on purpose: a keg nobody has cleaned is not ready to fill, and a
    -- register that could not tell the difference would let one be
    -- filled dirty.
    'returned_dirty',
    -- Damaged, condemned, or withdrawn.
    'out_of_service',
    -- Not coming back. The deposit is forfeit and the asset is written
    -- off; kept as a row because "we lost eleven kegs last year" is a
    -- number somebody needs.
    'lost'
);

CREATE TYPE keg_event_kind AS ENUM (
    'acquired',
    'filled',
    'shipped',
    'returned',
    'cleaned',
    'condemned',
    'lost'
);

CREATE TABLE kegs (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    -- The number stamped or welded on the keg. The register is useless
    -- without one, so it is required and unique.
    serial            TEXT NOT NULL,
    capacity_l        DOUBLE PRECISION NOT NULL CHECK (capacity_l > 0),
    material          TEXT NOT NULL DEFAULT '',
    -- What it cost and what a customer puts down against it. The deposit
    -- is a liability while the keg is out, which is the only money figure
    -- this table is responsible for.
    purchase_cost_cad NUMERIC(12,2),
    deposit_cad       NUMERIC(12,2),
    purchased_on      DATE,
    status            keg_status NOT NULL DEFAULT 'available',
    -- Where it is. Both nullable: a keg on our own floor has neither.
    current_customer_id UUID REFERENCES customers(id) ON DELETE SET NULL,
    current_location_id UUID REFERENCES locations(id) ON DELETE SET NULL,
    -- What is in it, when it is full. Deliberately references and NOT
    -- copies: volume, strength, LAA and duty live on the row pointed at,
    -- and reach the B266 from there. See the header for which applies.
    marked_container_id   UUID REFERENCES marked_special_containers(id) ON DELETE SET NULL,
    packaged_inventory_id UUID REFERENCES packaged_inventory(id) ON DELETE SET NULL,
    last_filled_on    DATE,
    last_returned_on  DATE,
    notes             TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, serial),
    -- A keg cannot be full of nothing, and an empty one cannot hold
    -- contents. The register's whole value is knowing which kegs are
    -- full, so the two must not drift.
    CHECK ((status IN ('filled','at_customer'))
           = (marked_container_id IS NOT NULL OR packaged_inventory_id IS NOT NULL)),
    -- And never both: the contents are in one place or the other, and a
    -- keg claiming both would be two lots of spirits in one vessel.
    CHECK (NOT (marked_container_id IS NOT NULL AND packaged_inventory_id IS NOT NULL)),
    -- The Act's threshold, enforced rather than trusted to the caller. A
    -- keg below 100 L cannot hold a marked special container because it
    -- is not one; a keg at or above it cannot hold packaged spirits for
    -- the same reason in reverse.
    CHECK (marked_container_id IS NULL OR capacity_l >= 100),
    CHECK (packaged_inventory_id IS NULL OR capacity_l < 100)
);

CREATE INDEX kegs_tenant_status_idx ON kegs (tenant_id, status);
CREATE INDEX kegs_customer_idx ON kegs (current_customer_id) WHERE current_customer_id IS NOT NULL;

CREATE TRIGGER kegs_updated_at
    BEFORE UPDATE ON kegs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE kegs ENABLE ROW LEVEL SECURITY;
ALTER TABLE kegs FORCE  ROW LEVEL SECURITY;
CREATE POLICY kegs_tenant ON kegs FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- The cycle, as it happened. The kegs row says where a keg is now; this
-- says how it got there, which is what an argument about a missing keg is
-- settled with.
CREATE TABLE keg_events (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    keg_id            UUID NOT NULL REFERENCES kegs(id) ON DELETE CASCADE,
    kind              keg_event_kind NOT NULL,
    occurred_on       DATE NOT NULL,
    customer_id       UUID REFERENCES customers(id) ON DELETE SET NULL,
    marked_container_id   UUID REFERENCES marked_special_containers(id) ON DELETE SET NULL,
    packaged_inventory_id UUID REFERENCES packaged_inventory(id) ON DELETE SET NULL,
    -- Signed: positive when a deposit is taken, negative when refunded.
    -- The outstanding liability is the running sum, which is why it is a
    -- delta rather than a balance.
    deposit_delta_cad NUMERIC(12,2),
    notes             TEXT NOT NULL DEFAULT '',
    user_id           UUID REFERENCES users(id),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX keg_events_keg_idx ON keg_events (keg_id, occurred_on DESC);
CREATE INDEX keg_events_tenant_date_idx ON keg_events (tenant_id, occurred_on DESC);

ALTER TABLE keg_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE keg_events FORCE  ROW LEVEL SECURITY;
CREATE POLICY keg_events_tenant ON keg_events FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));
