REVOKE SELECT, INSERT, UPDATE ON invite_codes FROM stillhouse_app;
ALTER TABLE users DROP COLUMN IF EXISTS email_verified_at;
DROP TABLE IF EXISTS invite_codes;
