-- The alert kind stays; Postgres cannot drop an enum value and it is
-- inert once nothing raises it.
DROP TABLE IF EXISTS redistillations;
DROP TYPE IF EXISTS redistillation_reason;
