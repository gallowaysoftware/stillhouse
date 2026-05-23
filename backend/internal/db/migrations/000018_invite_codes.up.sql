-- Invite codes for multi-tenant signup. These are NOT tenant-scoped — they
-- exist as a global gate to creating a new tenant. RLS deliberately off so
-- the public Signup RPC can look up a code without a tenant context.
-- Access control happens at the RPC layer: owner-only generate / list /
-- revoke, public read-by-code at signup time.

CREATE TABLE invite_codes (
    code              TEXT PRIMARY KEY,
    created_by_user_id UUID NOT NULL REFERENCES users(id),
    created_by_tenant_id UUID NOT NULL REFERENCES tenants(id),
    note              TEXT NOT NULL DEFAULT '',
    expires_at        TIMESTAMPTZ,
    redeemed_at       TIMESTAMPTZ,
    redeemed_email    TEXT NOT NULL DEFAULT '',
    redeemed_tenant_id UUID REFERENCES tenants(id),
    revoked_at        TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX invite_codes_creator_idx ON invite_codes (created_by_user_id, created_at DESC);

-- Email verification tracking on users — a user is verified once they sign
-- in via a freshly-redeemed invite code OR confirm via a verification link.
-- For invite-only signup the code redemption itself counts as verification:
-- an owner trusted them enough to hand over the code.
ALTER TABLE users
    ADD COLUMN email_verified_at TIMESTAMPTZ;

-- Mark every existing user verified so the rollout doesn't lock anyone out.
UPDATE users SET email_verified_at = created_at WHERE email_verified_at IS NULL;

-- Grants for the app role (invite_codes isn't RLS-protected; we still need
-- the app role to be able to insert/select/update it).
GRANT SELECT, INSERT, UPDATE ON invite_codes TO stillhouse_app;
