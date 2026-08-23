-- 000041_licence_register: what the licensee actually holds.
--
-- The tenant carried two free-text licence numbers — spirits, and
-- optionally excise warehouse — and nothing else. No effective date, no
-- expiry, no premises, no security. That is enough to print a number on
-- a return and not enough for anything else:
--
--   * Which returns exist is a function of which licences are held. A
--     licensee with more than one files a separate return for each
--     (EDM3-1-1 ¶49), which is `B2`.
--   * Where the duty point falls follows from whether an excise
--     warehouse licence is held — stage 143 made that a stored, dated
--     decision, and this is the record it should be reading.
--   * Licences run two years and must be renewed 30 days before expiry.
--     A date nobody holds is a renewal nobody gets reminded about, which
--     is how a licence lapses.
--   * The spirits licence carries a security requirement (s.23), which
--     has its own expiry and its own consequences for letting it lapse.
--
-- Existing tenants are backfilled from the two columns they had, so
-- nothing that reads a licence number changes behaviour on the day this
-- lands. The columns stay for now: the B266 header reads the spirits
-- number and retiring it is a separate change from introducing the
-- register.

CREATE TYPE excise_licence_kind AS ENUM (
    'spirits',           -- L63A — to produce or package spirits
    'excise_warehouse',  -- L63W — to store non-duty-paid spirits
    'users',             -- user's licence — to use spirits in manufacture
    'wine',              -- wine licence
    'other'
);

CREATE TABLE excise_licences (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    kind           excise_licence_kind NOT NULL,
    licence_number TEXT NOT NULL,

    -- Both dates matter and for different reasons. effective_from bounds
    -- when a movement could legally have happened under this licence;
    -- expires_on drives the renewal reminder. NULL expiry means not
    -- recorded, which is a different statement from "does not expire" —
    -- every CRA licence does.
    effective_from DATE NOT NULL DEFAULT CURRENT_DATE,
    expires_on     DATE,

    -- The premises this licence covers. An excise warehouse licence may
    -- specify several, and the 30% single-retail-store supply rule
    -- (EDM8-1-1 ¶20) is computed per premises — which is `F1`.
    premises       TEXT NOT NULL DEFAULT '',

    -- Security posted under s.23: $5,000 to $2M for a spirits licence,
    -- sufficient to cover amounts owing. NUMERIC because it is money.
    security_amount_cad NUMERIC(12, 2) CHECK (security_amount_cad IS NULL OR security_amount_cad >= 0),
    security_expires_on DATE,

    notes          TEXT NOT NULL DEFAULT '',
    -- Surrendered or revoked rather than expired. Kept, not deleted: a
    -- return filed under a licence that no longer exists still has to be
    -- explicable years later.
    ceased_on      DATE,

    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, kind, licence_number),
    CONSTRAINT excise_licences_dates_chk
        CHECK (expires_on IS NULL OR expires_on >= effective_from)
);

CREATE INDEX excise_licences_tenant_kind_idx ON excise_licences (tenant_id, kind);
CREATE INDEX excise_licences_expiry_idx ON excise_licences (expires_on)
    WHERE ceased_on IS NULL;

CREATE TRIGGER excise_licences_updated_at
    BEFORE UPDATE ON excise_licences
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE excise_licences ENABLE ROW LEVEL SECURITY;
ALTER TABLE excise_licences FORCE  ROW LEVEL SECURITY;
CREATE POLICY excise_licences_tenant ON excise_licences FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- Backfill. effective_from is the tenant's creation date rather than
-- today: the licence existed before Stillhouse did, and dating it to the
-- migration would assert something false. expires_on is left NULL —
-- unknown, and inventing a two-year window from a date we are guessing
-- at would produce a renewal reminder for the wrong day, which is worse
-- than none.
INSERT INTO excise_licences (tenant_id, kind, licence_number, effective_from, notes)
SELECT id, 'spirits', cra_spirits_licence_number, created_at::DATE,
       'Backfilled from the tenant record. Set the expiry date to get renewal reminders.'
FROM tenants
WHERE cra_spirits_licence_number <> '';

INSERT INTO excise_licences (tenant_id, kind, licence_number, effective_from, notes)
SELECT id, 'excise_warehouse', excise_warehouse_licence_number, created_at::DATE,
       'Backfilled from the tenant record. Set the expiry date to get renewal reminders.'
FROM tenants
WHERE excise_warehouse_licence_number IS NOT NULL
  AND excise_warehouse_licence_number <> '';

-- The renewal reminders the register makes possible. Adding values here
-- rather than in the alerting migration keeps the enum with the thing it
-- describes.
ALTER TYPE alert_kind ADD VALUE IF NOT EXISTS 'licence_expiring';
ALTER TYPE alert_kind ADD VALUE IF NOT EXISTS 'licence_expired';
ALTER TYPE alert_kind ADD VALUE IF NOT EXISTS 'licence_security_expiring';

GRANT SELECT, INSERT, UPDATE, DELETE ON excise_licences TO stillhouse_app;
