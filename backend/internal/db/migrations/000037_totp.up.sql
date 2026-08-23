-- 000037_totp: a second factor.
--
-- Stillhouse authenticated with a password and nothing else, on a system
-- holding the records behind a filed excise return. Stage 154 made a
-- password change actually revoke something; this is the other half —
-- making the password on its own insufficient.
--
-- These are auth tables and carry no tenant_id, exactly like users,
-- sessions and password_reset_tokens: the check runs during login,
-- before any tenant context exists. The carve-out is stated in 000001
-- and the schema test's allow-list is keyed off the tenant_id column, so
-- a table without one is simply not in its scope.

CREATE TABLE user_totp (
    user_id        UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,

    -- The shared secret, sealed with AES-256-GCM under
    -- STILLHOUSE_SECRET_KEY (internal/secrets). A second factor exists so
    -- that a stolen password is not enough; a plaintext secret in a
    -- nightly backup hands over both at once, and the backup is the
    -- likeliest way this data leaves the building.
    secret_sealed  BYTEA NOT NULL,

    -- NULL means enrolment was started and never finished. An unconfirmed
    -- row must never gate a login: a person who scanned a QR code and
    -- then closed the tab has not set up a second factor, and locking
    -- them out for it would be the system's fault.
    confirmed_at   TIMESTAMPTZ,

    -- The last step value accepted, so a code cannot be used twice.
    -- Without it a code read over a shoulder or off a phishing page stays
    -- good for the rest of its ninety-second window, which is the one
    -- attack a second factor is meant to make expensive. RFC 6238 §5.2
    -- requires exactly this.
    last_used_step BIGINT,

    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TRIGGER user_totp_updated_at
    BEFORE UPDATE ON user_totp
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Recovery codes. The phone is the single point of failure in any TOTP
-- setup, and a distillery owner locked out of their own excise records
-- because a handset went in a mash tun is not an acceptable failure
-- mode. Stored as SHA-256 like every other credential here, so a leak
-- yields nothing usable.
CREATE TABLE user_totp_recovery_codes (
    code_hash  BYTEA PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX user_totp_recovery_codes_user_idx
    ON user_totp_recovery_codes (user_id, used_at);

GRANT SELECT, INSERT, UPDATE, DELETE ON user_totp                TO stillhouse_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON user_totp_recovery_codes TO stillhouse_app;
