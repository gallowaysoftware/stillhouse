-- Postgres cannot remove a value from an enum type. Rebuilding user_role
-- without 'accountant' would mean rewriting the users table and every
-- policy and default that references it, on a rollback path, to remove a
-- value that is inert when unused.
--
-- So this down migration reassigns anyone holding the role and stops
-- there. The enum keeps the value; nothing depends on it being absent.
UPDATE users SET role = 'viewer' WHERE role = 'accountant';
