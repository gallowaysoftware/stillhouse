-- 000010_app_role: introduce a non-superuser role for the application to
-- connect as, so the tenant-isolation RLS policies (000001-000007) actually
-- enforce. Until this migration the dev `stillhouse` user is a superuser
-- and bypasses RLS entirely — see memory `project-rls-superuser-gap`.
--
-- Migrations continue to run as `stillhouse` (the superuser owner); the
-- Go server + seed command should connect as `stillhouse_app`. The
-- Makefile splits the DSNs: ADMIN_DATABASE_URL for migrate/seed,
-- DATABASE_URL for the server.

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'stillhouse_app') THEN
        -- Password is the dev default; override in production by setting the
        -- role's password out of band (psql ALTER ROLE ... PASSWORD 'xyz').
        CREATE ROLE stillhouse_app LOGIN NOSUPERUSER NOBYPASSRLS PASSWORD 'stillhouse_app';
    END IF;
END $$;

GRANT USAGE ON SCHEMA public TO stillhouse_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO stillhouse_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO stillhouse_app;

-- Make sure future tables created by the migration owner are automatically
-- accessible to the app role.
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO stillhouse_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public
    GRANT USAGE, SELECT ON SEQUENCES TO stillhouse_app;
