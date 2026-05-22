-- The role may be referenced by active sessions; refuse to drop unless
-- you've shut down the server. REASSIGN/DROP OWNED is required before
-- the role itself can be dropped.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'stillhouse_app') THEN
        REVOKE ALL ON ALL TABLES IN SCHEMA public FROM stillhouse_app;
        REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM stillhouse_app;
        REVOKE USAGE ON SCHEMA public FROM stillhouse_app;
        ALTER DEFAULT PRIVILEGES IN SCHEMA public
            REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM stillhouse_app;
        ALTER DEFAULT PRIVILEGES IN SCHEMA public
            REVOKE USAGE, SELECT ON SEQUENCES FROM stillhouse_app;
        DROP OWNED BY stillhouse_app;
        DROP ROLE stillhouse_app;
    END IF;
END $$;
