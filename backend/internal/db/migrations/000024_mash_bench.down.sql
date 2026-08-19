-- mash_metric_kind keeps 'wash_volume_l': Postgres cannot drop an enum
-- value, and recreating the type would require rewriting every dependent
-- column. The value is simply unused after a rollback.

ALTER TABLE materials DROP COLUMN cereal;
DROP TYPE cereal;
