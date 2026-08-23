-- Roles are cluster-wide; this migration is not.
--
-- The original down half dropped the stillhouse_app role outright. Two
-- things wrong with that, both found by the migration round-trip test in
-- internal/dbmigrate (stage 156), which walks every migration down to
-- nothing and back:
--
--   * It fails. DROP OWNED BY only covers the database you are connected
--     to, so any *other* Stillhouse database in the same cluster — a
--     scratch one, a restore drill, a second install — still holds
--     grants, and the DROP ROLE is refused with 2BP01.
--   * If it had succeeded it would have been worse. Dropping a
--     cluster-wide role to roll one database back takes the app role out
--     from under every other database in the cluster, which is a much
--     larger outage than the one being fixed.
--
-- So this revokes what this database granted and leaves the role in
-- place. An unused role with no grants is inert, and the up half is
-- already written to adopt an existing one (IF NOT EXISTS). The same
-- reasoning applies to stillhouse_auth in 000033.
--
-- To remove the role from a cluster for good, after the last Stillhouse
-- database is gone: DROP OWNED BY stillhouse_app in each remaining
-- database, then DROP ROLE stillhouse_app. That is a cluster-level
-- operation and does not belong in a per-database migration.
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
    END IF;
END $$;
