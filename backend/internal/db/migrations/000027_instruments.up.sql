-- ------------------------------------------------------------------------
-- Instrument register.
--
-- EDM3-1-1 para 24 and EDM1-1-5: volume and absolute alcohol content must
-- be determined using CRA-approved instruments, and EACH INDIVIDUAL
-- INSTRUMENT must itself be approved — approval attaches to the serial
-- number in front of the operator, not to the model.
--
-- Stillhouse already records HOW a figure was determined (strength_source:
-- uncorrected / table_density / table_strength) and nothing about WHAT
-- determined it, so the audit chain runs: quantity → movement →
-- determination → ...nothing. This closes the last link.
-- ------------------------------------------------------------------------

CREATE TYPE instrument_kind AS ENUM (
    'thermometer',
    'hydrometer',
    'density_meter',
    'mass_flow_meter',
    'scale',
    'volumetric_measure',
    'other'
);

-- active     — in service and usable for a determination.
-- retired    — withdrawn. Kept, never deleted: a determination made years
--              ago has to keep pointing at the instrument that made it.
-- suspended  — temporarily out of service (failed a check, away for
--              repair). Distinct from retired so the reason survives.
CREATE TYPE instrument_status AS ENUM ('active', 'suspended', 'retired');

CREATE TABLE instruments (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    kind                instrument_kind NOT NULL,
    -- What the operator calls it on the floor ("Still house hydro #2").
    label               TEXT NOT NULL,
    manufacturer        TEXT NOT NULL DEFAULT '',
    model               TEXT NOT NULL DEFAULT '',
    -- The serial is the identity CRA approves. Unique per tenant so two
    -- rows cannot claim the same physical instrument.
    serial_no           TEXT NOT NULL,

    -- CRA approval. Blank approval_reference means "no approval on file",
    -- which is the state a newly registered instrument starts in and the
    -- state that makes a duty-relevant determination refuse.
    approval_reference  TEXT NOT NULL DEFAULT '',
    approval_date       DATE,
    -- Approvals can lapse. NULL means no stated expiry.
    approval_expires_on DATE,

    status              instrument_status NOT NULL DEFAULT 'active',
    -- Set when status becomes suspended or retired, so the trail says why.
    status_reason       TEXT NOT NULL DEFAULT '',

    -- How often this instrument is meant to be checked. NULL means no
    -- interval is set, and calibration is then never reported as overdue —
    -- an interval nobody chose is not a deadline anybody missed.
    calibration_interval_days INTEGER
        CHECK (calibration_interval_days IS NULL OR calibration_interval_days > 0),

    notes               TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, serial_no)
);

CREATE INDEX instruments_tenant_kind_idx ON instruments (tenant_id, kind, status);

CREATE TRIGGER instruments_updated_at
    BEFORE UPDATE ON instruments
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE instruments ENABLE ROW LEVEL SECURITY;
ALTER TABLE instruments FORCE  ROW LEVEL SECURITY;
CREATE POLICY instruments_tenant ON instruments FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- ------------------------------------------------------------------------
-- Calibration history. Append-only in practice: a calibration record is
-- evidence, and evidence that can be edited is not evidence.
-- ------------------------------------------------------------------------
CREATE TABLE instrument_calibrations (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    instrument_id       UUID NOT NULL REFERENCES instruments(id) ON DELETE CASCADE,
    calibrated_on       DATE NOT NULL,
    -- Who did it: an internal check, or a named external laboratory.
    performed_by        TEXT NOT NULL DEFAULT '',
    -- The certificate an auditor would ask to see.
    certificate_ref     TEXT NOT NULL DEFAULT '',
    passed              BOOLEAN NOT NULL DEFAULT TRUE,
    notes               TEXT NOT NULL DEFAULT '',
    recorded_by         UUID REFERENCES users(id),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX instrument_calibrations_instrument_idx
    ON instrument_calibrations (instrument_id, calibrated_on DESC);

ALTER TABLE instrument_calibrations ENABLE ROW LEVEL SECURITY;
ALTER TABLE instrument_calibrations FORCE  ROW LEVEL SECURITY;
CREATE POLICY instrument_calibrations_tenant ON instrument_calibrations FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- ------------------------------------------------------------------------
-- The link from a determination to the instruments that made it.
--
-- Three roles, because a gauge is three measurements and they are not
-- normally taken with one device: the volume, the strength, and the
-- temperature the other two were read at. Nullable, because the register
-- starts empty and a determination with no instrument named is recorded
-- honestly as such rather than refused — see the handler, which refuses
-- an instrument that IS named but is not approved.
--
-- ON DELETE RESTRICT: an instrument that has determined a figure on a
-- filed return cannot be deleted out from under it. Retire it instead.
-- ------------------------------------------------------------------------
ALTER TABLE production_gauges
    ADD COLUMN volume_instrument_id      UUID REFERENCES instruments(id) ON DELETE RESTRICT,
    ADD COLUMN strength_instrument_id    UUID REFERENCES instruments(id) ON DELETE RESTRICT,
    ADD COLUMN temperature_instrument_id UUID REFERENCES instruments(id) ON DELETE RESTRICT;

ALTER TABLE barrel_events
    ADD COLUMN volume_instrument_id      UUID REFERENCES instruments(id) ON DELETE RESTRICT,
    ADD COLUMN strength_instrument_id    UUID REFERENCES instruments(id) ON DELETE RESTRICT,
    ADD COLUMN temperature_instrument_id UUID REFERENCES instruments(id) ON DELETE RESTRICT;

CREATE INDEX production_gauges_instruments_idx
    ON production_gauges (strength_instrument_id)
    WHERE strength_instrument_id IS NOT NULL;
CREATE INDEX barrel_events_instruments_idx
    ON barrel_events (strength_instrument_id)
    WHERE strength_instrument_id IS NOT NULL;
