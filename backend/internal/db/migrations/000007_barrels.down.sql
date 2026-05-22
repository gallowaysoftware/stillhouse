DROP TABLE IF EXISTS barrel_events CASCADE;
DROP TABLE IF EXISTS barrel_attributes CASCADE;
DROP TYPE  IF EXISTS barrel_event_kind;
-- Note: cannot drop a value from an enum in standard PostgreSQL. The
-- 'barrel' value added to bulk_container_kind will remain harmlessly.
