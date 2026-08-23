-- The keyhole functions go first: they name webhook_event_kind in their
-- return type, so the type cannot be dropped while they exist. The round
-- trip test caught this, which is exactly the evening it exists for.
DROP FUNCTION IF EXISTS webhook_record_result(UUID, BOOLEAN, INT, TEXT, INTERVAL);
DROP FUNCTION IF EXISTS webhook_claim_due(INT);

DROP TABLE IF EXISTS webhook_deliveries;
DROP TABLE IF EXISTS webhook_endpoints;
DROP TYPE IF EXISTS webhook_delivery_status;
DROP TYPE IF EXISTS webhook_event_kind;

-- stillhouse_webhook is left in place, as 000033 leaves stillhouse_auth:
-- a NOLOGIN role owning nothing grants nothing, and dropping a role that
-- another database on the same cluster may also use is a worse failure
-- than leaving one behind.
