-- Marked special containers.
--
-- EDM3-8-1: a container of 100 to 1,500 litres, marked rather than
-- stamped, for delivery to a registered user or to bottle-your-own
-- premises. They are packaging — the alcohol has left bulk — and they
-- have their own lines on the B266 and the B262. Stage 143 split the
-- packaging figures by duty treatment and left the third column of that
-- line, "packaged in marked special containers", with nothing that could
-- write it.
--
-- Modelled as their own thing rather than as packaged_inventory. A lot of
-- bottles is fungible and counted; a marked container is one object with
-- one mark on it, a lifecycle of its own, and a way back to bulk that
-- bottles do not have — s.156 lets a container be unmarked and its
-- contents returned to bulk, which is a movement in the ledger and not a
-- correction.
--
-- What Stillhouse does NOT do here is decide what the mark has to say.
-- The marking requirements are EDM3-8-1's and they are the licensee's to
-- satisfy; the column records what was applied so an auditor can see it,
-- and nothing is generated. For the same reason no excise stamp is
-- consumed: these containers are marked, which is what distinguishes
-- them, and a licensee whose circumstances differ will see that no stamp
-- was drawn rather than discovering a silent one.

CREATE TYPE marked_container_status AS ENUM (
    -- Filled and marked, on the premises.
    'marked',
    -- Gone to a registered user or bottle-your-own premises.
    'delivered',
    -- Unmarked under s.156 and its contents returned to bulk.
    'unmarked',
    'destroyed'
);

CREATE TABLE marked_special_containers (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    container_no   INTEGER NOT NULL,
    -- What was actually marked on it. Free text: EDM3-8-1 says what it
    -- must carry, and Stillhouse records rather than composes it.
    mark           TEXT NOT NULL CHECK (length(trim(mark)) > 0),
    -- EDM3-8-1's range. A vessel outside it is not a marked special
    -- container, whatever is written on the side.
    capacity_l     DOUBLE PRECISION NOT NULL
        CHECK (capacity_l >= 100 AND capacity_l <= 1500),
    -- Optional: a keg of a named product. A container filled with
    -- something that is not a listed product is legitimate.
    product_id     UUID REFERENCES products(id) ON DELETE SET NULL,
    description    TEXT NOT NULL DEFAULT '',

    status         marked_container_status NOT NULL DEFAULT 'marked',

    -- Contents at the fill, and the gauge behind them.
    source_container_id UUID NOT NULL REFERENCES bulk_containers(id) ON DELETE RESTRICT,
    volume_l       DOUBLE PRECISION NOT NULL CHECK (volume_l > 0),
    abv_pct        DOUBLE PRECISION NOT NULL CHECK (abv_pct > 0 AND abv_pct <= 100),
    laa            DOUBLE PRECISION NOT NULL CHECK (laa > 0),
    filled_on      DATE NOT NULL,
    filled_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    -- The transfer_to_packaging this wrote. Filling is a packaging act
    -- and the bulk side has to see it leave.
    bulk_movement_id UUID REFERENCES bulk_movements(id) ON DELETE SET NULL,

    -- Duty crystallised at the fill, when the licensee's duty point is
    -- at packaging. NULL is deliberately different from zero, exactly as
    -- it is on bottling_runs: NULL means this was not a duty event, zero
    -- would mean it was one and cost nothing.
    duty_rate_per_laa DOUBLE PRECISION,
    duty_amount_cad   DOUBLE PRECISION,
    duty_rate_source  TEXT NOT NULL DEFAULT '',

    notes          TEXT NOT NULL DEFAULT '',
    -- s.156: unmarked and returned to bulk.
    unmarked_on    DATE,
    unmarked_reason TEXT NOT NULL DEFAULT '',
    unmark_movement_id UUID REFERENCES bulk_movements(id) ON DELETE SET NULL,

    created_by     UUID NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (tenant_id, container_no),
    CONSTRAINT marked_container_unmark_has_reason CHECK (
        unmarked_on IS NULL OR length(trim(unmarked_reason)) > 0
    )
);

-- Where a marked container went.
--
-- Its own table rather than a row in packaging_removals. A removal is
-- against a lot of bottles and carries a bottle count, a bottle size and
-- a per-bottle strength; a marked container is one object with a volume.
-- Forcing them together would mean a nullable half of every removal and
-- a bottle count of zero on the return, and packaging_removals is the
-- most load-bearing table in the system.
CREATE TABLE marked_container_deliveries (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    delivery_no    INTEGER NOT NULL,
    container_id   UUID NOT NULL REFERENCES marked_special_containers(id) ON DELETE RESTRICT,
    delivery_date  DATE NOT NULL,
    -- Who it went to. The customer record decides the classification for
    -- the same reason it does on a removal (stage 157).
    customer_id    UUID REFERENCES customers(id) ON DELETE RESTRICT,
    destination_name TEXT NOT NULL DEFAULT '',
    reference      TEXT NOT NULL DEFAULT '',
    -- What left, copied at delivery so the figure cannot move afterwards.
    volume_l       DOUBLE PRECISION NOT NULL,
    abv_pct        DOUBLE PRECISION NOT NULL,
    laa            DOUBLE PRECISION NOT NULL,
    -- Duty on the delivery, when it was not taken at the fill.
    duty_rate_per_laa DOUBLE PRECISION NOT NULL DEFAULT 0,
    duty_amount_cad   DOUBLE PRECISION NOT NULL DEFAULT 0,
    notes          TEXT NOT NULL DEFAULT '',
    voided_at      TIMESTAMPTZ,
    void_reason    TEXT NOT NULL DEFAULT '',
    created_by     UUID NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (tenant_id, delivery_no),
    CONSTRAINT marked_delivery_void_has_reason CHECK (
        voided_at IS NULL OR length(trim(void_reason)) > 0
    )
);

DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['marked_special_containers', 'marked_container_deliveries'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY %I ON %I
            USING (tenant_id = current_setting('app.current_tenant_id')::uuid)
            WITH CHECK (tenant_id = current_setting('app.current_tenant_id')::uuid)$p$,
            t || '_tenant_isolation', t);
        EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO stillhouse_app', t);
    END LOOP;
END $$;

CREATE INDEX marked_containers_status_idx ON marked_special_containers (status);
CREATE INDEX marked_containers_filled_idx ON marked_special_containers (filled_on);
CREATE INDEX marked_deliveries_date_idx ON marked_container_deliveries (delivery_date)
    WHERE voided_at IS NULL;
