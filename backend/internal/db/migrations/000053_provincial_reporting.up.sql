-- The shape before the content.
--
-- Every province a licensee sells into wants something reported, and no
-- two want the same thing on the same clock. What Stillhouse can build
-- without a source in front of it is the machinery: which jurisdictions
-- the licensee is registered in, what each of them expects, when it is
-- due, and what the figures are. What it must not do is ship a table of
-- other people's filing deadlines from memory — so a definition is
-- entered by the licensee (or their consultant) and carries the citation
-- it came from, exactly as the pricing rates do.
--
-- The distinction matters more here than anywhere else in Stillhouse: a
-- wrong excise figure is caught by CRA, and a wrong provincial deadline
-- is caught by a delisting.

CREATE TYPE reporting_cadence AS ENUM (
    'monthly', 'quarterly', 'semi_annual', 'annual', 'per_shipment', 'other'
);

-- Where the requirement came from. Mirrors pricing's RateProvenance,
-- because it is the same argument about the same kind of fact: a
-- deadline somebody half-remembers is worse than no deadline, since it
-- looks like one.
CREATE TYPE requirement_provenance AS ENUM (
    -- Nothing behind it. Shown, never relied on, and it says so.
    'unknown',
    -- A secondary source: a consultant's summary, an industry note.
    'indicative',
    -- The board's or the legislature's own published material.
    'sourced'
);

-- The jurisdictions this licensee is actually registered in. Not a list
-- of provinces — a list of relationships, each with a registration
-- number the board knows the licensee by.
CREATE TABLE provincial_registrations (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    -- ISO 3166-2 subdivision, the same code packaged_inventory,
    -- customers and excise stamps already carry.
    jurisdiction   TEXT NOT NULL CHECK (jurisdiction ~ '^CA-[A-Z]{2}$'),
    -- Who the counterparty is: "LCBO", "BC Liquor Distribution Branch".
    -- Free text, because the names change and a shipped enum of them
    -- would be wrong within a year.
    board_name     TEXT NOT NULL DEFAULT '',
    -- What the board calls this licensee.
    registration_no TEXT NOT NULL DEFAULT '',
    portal_url     TEXT NOT NULL DEFAULT '',
    contact        TEXT NOT NULL DEFAULT '',
    registered_on  DATE,
    ended_on       DATE,
    notes          TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (tenant_id, jurisdiction)
);

-- What a jurisdiction expects, when, and on whose authority.
CREATE TABLE provincial_report_definitions (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    registration_id UUID NOT NULL REFERENCES provincial_registrations(id) ON DELETE CASCADE,
    name           TEXT NOT NULL CHECK (length(trim(name)) > 0),
    cadence        reporting_cadence NOT NULL,
    -- Days after the period ends. -1 means the licensee has not recorded
    -- one, which is different from "due on the last day".
    due_days_after_period_end INTEGER NOT NULL DEFAULT -1
        CHECK (due_days_after_period_end >= -1),
    -- Whether the reporting period follows the calendar or the licensee's
    -- own fiscal month election — the same clock the B266 runs on. A
    -- province that wants calendar months while the licensee files excise
    -- on a fiscal month is the ordinary case, not the exception, and
    -- assuming they agree is how a period gets reported twice.
    follows_excise_clock BOOLEAN NOT NULL DEFAULT FALSE,
    provenance     requirement_provenance NOT NULL DEFAULT 'unknown',
    -- Where it came from: a URL, a policy number, a letter.
    authority      TEXT NOT NULL DEFAULT '',
    -- The ISO date the requirement was published or last confirmed.
    confirmed_on   DATE,
    notes          TEXT NOT NULL DEFAULT '',
    archived       BOOLEAN NOT NULL DEFAULT FALSE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- A sourced requirement has to say what sourced it. Without this the
    -- provenance flag becomes a self-assessment nobody can check.
    CONSTRAINT provincial_definition_sourced_cites CHECK (
        provenance <> 'sourced' OR length(trim(authority)) > 0
    )
);

-- One instance of a definition: the period, and whether it went in.
CREATE TABLE provincial_report_periods (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    definition_id  UUID NOT NULL REFERENCES provincial_report_definitions(id) ON DELETE CASCADE,
    period_start   DATE NOT NULL,
    period_end     DATE NOT NULL,
    due_on         DATE,
    filed_at       TIMESTAMPTZ,
    filed_by       UUID REFERENCES users(id) ON DELETE SET NULL,
    -- What the board gave back. The same discipline as the B266's filing
    -- acknowledgement: a period marked filed with nothing to show for it
    -- is a claim, not a record.
    acknowledgement TEXT NOT NULL DEFAULT '',
    notes          TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CHECK (period_end >= period_start),
    CONSTRAINT provincial_period_filed_has_ack CHECK (
        filed_at IS NULL OR length(trim(acknowledgement)) > 0
    ),
    UNIQUE (definition_id, period_start, period_end)
);

DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'provincial_registrations',
        'provincial_report_definitions',
        'provincial_report_periods'
    ] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY %I ON %I
            USING (tenant_id = current_setting('app.current_tenant_id')::uuid)
            WITH CHECK (tenant_id = current_setting('app.current_tenant_id')::uuid)$p$,
            t || '_tenant_isolation', t);
        EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO stillhouse_app', t);
    END LOOP;
END $$;

CREATE INDEX provincial_definitions_reg_idx ON provincial_report_definitions (registration_id)
    WHERE NOT archived;
CREATE INDEX provincial_periods_due_idx ON provincial_report_periods (due_on)
    WHERE filed_at IS NULL;

-- The alert kinds. A provincial deadline missed is a delisting, which is
-- a worse outcome than a late B266, so it gets the same treatment.
ALTER TYPE alert_kind ADD VALUE IF NOT EXISTS 'provincial_filing_due';
ALTER TYPE alert_kind ADD VALUE IF NOT EXISTS 'provincial_filing_overdue';
