-- 000007_barrels: cooperage + maturation.
--
-- Barrels are a special kind of bulk_container. A 1:1 barrel_attributes
-- side table holds the cooperage metadata; movements (fills, dumps) use
-- the existing bulk_movements ledger so there's only one source of truth
-- for "where does the alcohol live." Barrel-specific events that don't
-- move alcohol (regauges, samples, moves between rickhouse positions)
-- live in barrel_events.

ALTER TYPE bulk_container_kind ADD VALUE IF NOT EXISTS 'barrel';

CREATE TABLE barrel_attributes (
    container_id        UUID PRIMARY KEY REFERENCES bulk_containers(id) ON DELETE CASCADE,
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    cooperage_supplier  TEXT NOT NULL DEFAULT '',
    char_level          INTEGER CHECK (char_level BETWEEN 1 AND 4),
    wood_species        TEXT NOT NULL DEFAULT '',
    prior_use           TEXT NOT NULL DEFAULT '',
    serial_burnin       TEXT NOT NULL DEFAULT '',
    rickhouse           TEXT NOT NULL DEFAULT '',
    row_position        TEXT NOT NULL DEFAULT '',
    level_position      TEXT NOT NULL DEFAULT '',
    column_position     TEXT NOT NULL DEFAULT '',
    -- fill_date is set on the first FILL event and cleared on DUMP. The
    -- maturation clock is days(now - fill_date) for an active barrel.
    fill_date           DATE,
    days_aged_at_dump   INTEGER
);

ALTER TABLE barrel_attributes ENABLE ROW LEVEL SECURITY;
ALTER TABLE barrel_attributes FORCE  ROW LEVEL SECURITY;
CREATE POLICY barrel_attributes_tenant ON barrel_attributes FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

CREATE TYPE barrel_event_kind AS ENUM (
    'fill',
    'regauge',
    'sample',
    'dump',
    'move',
    'destroy'
);

CREATE TABLE barrel_events (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    container_id        UUID NOT NULL REFERENCES bulk_containers(id) ON DELETE CASCADE,
    kind                barrel_event_kind NOT NULL,
    event_date          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    volume_l            DOUBLE PRECISION,
    abv_pct             DOUBLE PRECISION,
    laa                 DOUBLE PRECISION,
    bulk_movement_id    UUID REFERENCES bulk_movements(id),
    location_after      TEXT NOT NULL DEFAULT '',
    notes               TEXT NOT NULL DEFAULT '',
    user_id             UUID REFERENCES users(id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX barrel_events_container_date_idx
    ON barrel_events (container_id, event_date DESC);

ALTER TABLE barrel_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE barrel_events FORCE  ROW LEVEL SECURITY;
CREATE POLICY barrel_events_tenant ON barrel_events FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));
