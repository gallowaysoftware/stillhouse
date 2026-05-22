-- 000004_force_rls: Postgres exempts table owners from row-level security
-- policies by default; FORCE ROW LEVEL SECURITY makes the policies apply
-- to the owner too. Without this, the application role — which is the
-- table owner in our single-role v1 setup — silently bypasses the
-- tenant isolation policies defined in 000001..000003.
--
-- Going forward, every new tenant-scoped table should enable AND force RLS
-- in the same migration that creates it.

ALTER TABLE audit_events       FORCE ROW LEVEL SECURITY;
ALTER TABLE materials          FORCE ROW LEVEL SECURITY;
ALTER TABLE material_lots      FORCE ROW LEVEL SECURITY;
ALTER TABLE recipes            FORCE ROW LEVEL SECURITY;
ALTER TABLE recipe_versions    FORCE ROW LEVEL SECURITY;
ALTER TABLE recipe_ingredients FORCE ROW LEVEL SECURITY;
