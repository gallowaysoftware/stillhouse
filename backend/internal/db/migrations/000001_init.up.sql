-- 000001_init: bootstrap schema for Stillhouse.
--
-- Conventions:
--   * UUID primary keys generated server-side via pgcrypto.gen_random_uuid().
--   * TIMESTAMPTZ for all time columns.
--   * Tenant-scoped business tables use Postgres row-level security keyed off
--     the per-request GUC `app.current_tenant_id`. The auth tables here
--     (tenants, users, sessions) are not under RLS — login lookups by email
--     have to work before a tenant context exists, and tenants is the
--     authority on what a tenant id even is.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE OR REPLACE FUNCTION set_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$;

-- -----------------------------------------------------------------------
-- tenants: one row per distillery (one CRA spirits licence).
-- -----------------------------------------------------------------------
CREATE TABLE tenants (
    id                              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                            TEXT NOT NULL,
    cra_spirits_licence_number      TEXT NOT NULL UNIQUE,
    excise_warehouse_licence_number TEXT,
    default_jurisdiction            TEXT NOT NULL,
    created_at                      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TRIGGER tenants_updated_at
    BEFORE UPDATE ON tenants
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- -----------------------------------------------------------------------
-- users: an account for one human, scoped to one tenant.
-- -----------------------------------------------------------------------
CREATE TYPE user_role AS ENUM ('owner', 'operator', 'viewer');

CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    email           TEXT NOT NULL UNIQUE,
    password_hash   TEXT NOT NULL,
    display_name    TEXT NOT NULL,
    role            user_role NOT NULL DEFAULT 'operator',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX users_tenant_id_idx ON users (tenant_id);

CREATE TRIGGER users_updated_at
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- -----------------------------------------------------------------------
-- sessions: backing table for alexedwards/scs/pgxstore.
-- The schema below is exactly what that package expects.
-- -----------------------------------------------------------------------
CREATE TABLE sessions (
    token   TEXT PRIMARY KEY,
    data    BYTEA NOT NULL,
    expiry  TIMESTAMPTZ NOT NULL
);

CREATE INDEX sessions_expiry_idx ON sessions (expiry);

-- -----------------------------------------------------------------------
-- audit_events: append-only record of meaningful actions.
-- RLS-isolated per tenant.
-- -----------------------------------------------------------------------
CREATE TYPE audit_action AS ENUM (
    'create', 'update', 'delete', 'sign', 'login', 'logout'
);

CREATE TABLE audit_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id         UUID REFERENCES users(id) ON DELETE SET NULL,
    entity_type     TEXT NOT NULL,
    entity_id       TEXT NOT NULL,
    action          audit_action NOT NULL,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    payload         JSONB
);

CREATE INDEX audit_events_tenant_occurred_idx
    ON audit_events (tenant_id, occurred_at DESC);

ALTER TABLE audit_events ENABLE ROW LEVEL SECURITY;

-- Fails closed when the GUC is not set: NULL comparison yields NULL, treated as
-- false, so no rows leak when a request hasn't established tenant context.
CREATE POLICY audit_events_tenant_isolation ON audit_events
    FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));
