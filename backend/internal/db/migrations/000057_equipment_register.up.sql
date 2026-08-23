-- The stills, and everything else that has a capacity or a service
-- interval.
--
-- Bulk containers are vessels that hold alcohol and are already
-- first-class. What has never existed is the plant: the still itself, the
-- mash tun, the filler, the pumps and the chiller — things a run is
-- performed *on* rather than *into*. Without them a distillation run
-- names no still, capacity is a number in somebody's head, and
-- maintenance is a calendar reminder on a phone.
--
-- F3's scheduling cannot say anything honest about capacity until this
-- exists, which is why it is a prerequisite rather than a nicety.
--
-- Deliberately not modelled as bulk_containers with a flag. A still is
-- not a vessel that holds a balance — putting one in that table would
-- mean a row that must never appear in a LAA sum, which is the kind of
-- exception that gets forgotten exactly once.

CREATE TYPE equipment_kind AS ENUM (
    'still', 'mash_tun', 'fermenter_vessel', 'filler', 'pump',
    'chiller', 'boiler', 'condenser', 'bottling_line', 'other'
);

CREATE TYPE equipment_status AS ENUM ('in_service', 'down', 'retired');

CREATE TABLE equipment (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name           TEXT NOT NULL CHECK (length(trim(name)) > 0),
    kind           equipment_kind NOT NULL,
    status         equipment_status NOT NULL DEFAULT 'in_service',
    -- Where it is. Premises are already modelled (stage 171), and a
    -- still's location is a licensing fact rather than a convenience.
    location_id    UUID REFERENCES locations(id) ON DELETE SET NULL,

    manufacturer   TEXT NOT NULL DEFAULT '',
    model          TEXT NOT NULL DEFAULT '',
    serial_no      TEXT NOT NULL DEFAULT '',
    commissioned_on DATE,

    -- What it can do in one go. NULL means nobody has recorded it, which
    -- is different from zero and is why scheduling refuses to plan
    -- against it rather than assuming.
    capacity_l     DOUBLE PRECISION CHECK (capacity_l IS NULL OR capacity_l > 0),
    -- How long a typical run on it takes, in hours. Also nullable, and
    -- also for a reason: a made-up duration produces a schedule that
    -- looks authoritative and is fiction.
    typical_run_hours DOUBLE PRECISION
        CHECK (typical_run_hours IS NULL OR typical_run_hours > 0),
    -- Hours between servicing. NULL means no interval is recorded and
    -- nothing is ever due.
    service_interval_hours DOUBLE PRECISION
        CHECK (service_interval_hours IS NULL OR service_interval_hours > 0),
    service_interval_days INTEGER
        CHECK (service_interval_days IS NULL OR service_interval_days > 0),

    notes          TEXT NOT NULL DEFAULT '',
    retired_on     DATE,
    retired_reason TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (tenant_id, name),
    CONSTRAINT equipment_retired_has_reason CHECK (
        retired_on IS NULL OR length(trim(retired_reason)) > 0
    )
);

-- What was done to it, and when.
CREATE TABLE equipment_service_events (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    equipment_id   UUID NOT NULL REFERENCES equipment(id) ON DELETE CASCADE,
    performed_on   DATE NOT NULL DEFAULT CURRENT_DATE,
    -- Free text: a service record is a description of work, and an enum
    -- of maintenance categories is a list somebody has to keep current.
    description    TEXT NOT NULL CHECK (length(trim(description)) > 0),
    performed_by   TEXT NOT NULL DEFAULT '',
    hours_at_service DOUBLE PRECISION,
    cost_cad       NUMERIC(12, 2) CHECK (cost_cad IS NULL OR cost_cad >= 0),
    -- The work order it came off, where there was one.
    work_order_id  UUID REFERENCES work_orders(id) ON DELETE SET NULL,
    notes          TEXT NOT NULL DEFAULT '',
    recorded_by    UUID NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Runs attributed to equipment. Nullable on both sides: a run recorded
-- before the register existed names no still, and that is a gap to see
-- rather than a value to invent.
ALTER TABLE distillation_runs
    ADD COLUMN equipment_id UUID REFERENCES equipment(id) ON DELETE SET NULL;
ALTER TABLE mash_runs
    ADD COLUMN equipment_id UUID REFERENCES equipment(id) ON DELETE SET NULL;
ALTER TABLE bottling_runs
    ADD COLUMN equipment_id UUID REFERENCES equipment(id) ON DELETE SET NULL;
ALTER TABLE work_orders
    ADD COLUMN equipment_id UUID REFERENCES equipment(id) ON DELETE SET NULL;

DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['equipment', 'equipment_service_events'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY %I ON %I
            USING (tenant_id = current_setting('app.current_tenant_id')::uuid)
            WITH CHECK (tenant_id = current_setting('app.current_tenant_id')::uuid)$p$,
            t || '_tenant_isolation', t);
        EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO stillhouse_app', t);
    END LOOP;
END $$;

CREATE INDEX equipment_status_idx ON equipment (status) WHERE status <> 'retired';
CREATE INDEX equipment_service_idx ON equipment_service_events (equipment_id, performed_on DESC);
CREATE INDEX distillation_runs_equipment_idx ON distillation_runs (equipment_id)
    WHERE equipment_id IS NOT NULL;

-- Plant that is down, or overdue for service.
ALTER TYPE alert_kind ADD VALUE IF NOT EXISTS 'equipment_service_due';
ALTER TYPE alert_kind ADD VALUE IF NOT EXISTS 'equipment_down';
