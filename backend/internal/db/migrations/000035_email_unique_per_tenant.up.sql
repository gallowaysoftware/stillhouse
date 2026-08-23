-- 000035_email_unique_per_tenant: one person, two distilleries.
--
-- users.email was UNIQUE across the whole install. That is fine for a
-- single-tenant deployment and wrong for a hosted one: the person who
-- most wants an account is the outside bookkeeper or excise consultant,
-- and they work for several distilleries. Under the old constraint the
-- second distillery to invite them got an opaque `internal` error out of
-- CreateUser, with nothing on the screen to explain it.
--
-- The constraint moves to (tenant_id, email). What that costs is that an
-- email address no longer identifies an account on its own, which
-- matters in exactly one place — login, which has to work before a
-- tenant context exists. That is handled in the Login RPC: the password
-- is verified against every account holding the address, and if more
-- than one matches, the caller is asked which distillery they meant.
--
-- tenants.cra_spirits_licence_number deliberately keeps its install-wide
-- UNIQUE. A CRA spirits licence number is globally unique in the world;
-- two tenants claiming the same one means one of them is wrong, and the
-- constraint is the only thing that would catch it. What was wrong there
-- was the error, not the rule — a collision surfaced as `internal`, and
-- now says which number is already registered.

ALTER TABLE users DROP CONSTRAINT users_email_key;
ALTER TABLE users ADD CONSTRAINT users_tenant_email_key UNIQUE (tenant_id, email);

-- Login and password reset both look up by email alone, and now get a
-- set rather than a row. Index the lookup they actually run.
CREATE INDEX users_email_idx ON users (email);
