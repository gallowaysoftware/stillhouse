DROP INDEX IF EXISTS users_email_idx;
ALTER TABLE users DROP CONSTRAINT users_tenant_email_key;
-- Fails if the same address is in use at more than one tenant by now,
-- which is the correct outcome: rolling back cannot invent a rule the
-- data no longer satisfies.
ALTER TABLE users ADD CONSTRAINT users_email_key UNIQUE (email);
