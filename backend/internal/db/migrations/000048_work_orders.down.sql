-- The two alert kinds stay; Postgres cannot drop an enum value and they
-- are inert once nothing raises them.
DROP TABLE IF EXISTS work_orders;
DROP TYPE IF EXISTS work_order_status;
DROP TYPE IF EXISTS work_order_kind;
