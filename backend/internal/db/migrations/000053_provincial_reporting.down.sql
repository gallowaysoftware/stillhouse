-- alert_kind keeps 'provincial_filing_due' and 'provincial_filing_overdue'.
-- Postgres cannot remove an enum value without rewriting the type and
-- every column using it, and a kind nothing raises costs nothing.
DROP TABLE IF EXISTS provincial_report_periods;
DROP TABLE IF EXISTS provincial_report_definitions;
DROP TABLE IF EXISTS provincial_registrations;
DROP TYPE IF EXISTS requirement_provenance;
DROP TYPE IF EXISTS reporting_cadence;
