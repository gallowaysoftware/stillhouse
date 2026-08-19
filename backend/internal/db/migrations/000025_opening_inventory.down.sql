-- Postgres cannot drop an enum value, and recreating bulk_movement_reason
-- would mean rewriting every dependent column. The value is simply unused
-- after a rollback.
SELECT 1;
