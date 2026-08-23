-- Postgres cannot remove a value from an enum type; the three alert
-- kinds stay and are inert once nothing raises them.
DROP TABLE IF EXISTS excise_licences;
DROP TYPE IF EXISTS excise_licence_kind;
