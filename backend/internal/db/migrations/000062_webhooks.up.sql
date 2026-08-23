-- 000062_webhooks: outbound webhooks, so another system can react to
-- what happens here. PLAN G6.
--
-- Two tables and the reason for each.
--
-- webhook_endpoints is the operator's registration. The secret is sealed
-- at rest like every other secret in this schema, because a webhook
-- secret is a signing key: anyone holding it can forge a delivery that
-- the receiver will accept as ours.
--
-- webhook_deliveries is a transactional outbox, not a queue the sender
-- writes to after the fact. The row is written in the SAME transaction as
-- the event it describes, so a bottling run that rolls back cannot leave
-- a webhook saying it happened. The alternative — fire after commit —
-- has a window where the process dies between the two and the event is
-- silently never delivered, and a "reliable" notification that is
-- occasionally not delivered is worse than none, because nobody builds a
-- reconciliation for it.

CREATE TYPE webhook_event_kind AS ENUM (
    -- Deliberately a small set, and all of them things that already have
    -- a defensible meaning elsewhere in the schema. An event kind that
    -- exists only to be webhooked is one nobody can explain later.
    'b266_period_submitted',
    'bottling_run_recorded',
    'removal_recorded',
    'production_gauge_recorded',
    'loss_recorded'
);

CREATE TYPE webhook_delivery_status AS ENUM (
    'pending',
    'delivered',
    -- Retries exhausted. Kept rather than deleted: the operator needs to
    -- be able to see what was not delivered, which is the whole reason
    -- they subscribed.
    'failed'
);

CREATE TABLE webhook_endpoints (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    url           TEXT NOT NULL,
    -- HMAC-SHA256 signing key, sealed. See internal/secrets.
    secret_sealed BYTEA NOT NULL,
    kinds         webhook_event_kind[] NOT NULL DEFAULT '{}',
    enabled       BOOLEAN NOT NULL DEFAULT TRUE,
    description   TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Same URL twice is a mistake, not a use case.
    UNIQUE (tenant_id, url)
);

CREATE INDEX webhook_endpoints_tenant_idx ON webhook_endpoints (tenant_id) WHERE enabled;

CREATE TRIGGER webhook_endpoints_updated_at
    BEFORE UPDATE ON webhook_endpoints
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE webhook_endpoints ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_endpoints FORCE  ROW LEVEL SECURITY;
CREATE POLICY webhook_endpoints_tenant ON webhook_endpoints FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

CREATE TABLE webhook_deliveries (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    endpoint_id   UUID NOT NULL REFERENCES webhook_endpoints(id) ON DELETE CASCADE,
    kind          webhook_event_kind NOT NULL,
    -- The body as it will be sent. Frozen at enqueue time on purpose: a
    -- delivery retried tomorrow must describe what happened today, not
    -- what the row looks like after somebody edited it.
    payload       JSONB NOT NULL,
    status        webhook_delivery_status NOT NULL DEFAULT 'pending',
    attempts      INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_status_code INT,
    last_error    TEXT NOT NULL DEFAULT '',
    delivered_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The claim index. Partial on pending so the worker's scan does not walk
-- a growing history of delivered rows.
CREATE INDEX webhook_deliveries_due_idx
    ON webhook_deliveries (next_attempt_at)
    WHERE status = 'pending';
CREATE INDEX webhook_deliveries_tenant_idx ON webhook_deliveries (tenant_id, created_at DESC);

ALTER TABLE webhook_deliveries ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_deliveries FORCE  ROW LEVEL SECURITY;
CREATE POLICY webhook_deliveries_tenant ON webhook_deliveries FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- ------------------------------------------------------------------------
-- The delivery worker's keyhole.
--
-- The worker is one loop for the whole install: it has to see pending
-- deliveries across every tenant, and there is no tenant context to set
-- because it is not acting for anyone. The obvious answer — run it on the
-- admin pool — throws away the RLS story for the sake of one goroutine.
--
-- So it goes through the same keyhole 000033 built for the bearer-auth
-- lookup: a NOLOGIN BYPASSRLS role owns two SECURITY DEFINER functions,
-- stillhouse_app is granted EXECUTE on those and nothing else, and the
-- app role's own reach into these tables stays bounded by the policy
-- above. A bug in the management RPCs still cannot read another tenant's
-- endpoints.
--
-- Note what the keyhole deliberately does NOT expose: the endpoint
-- secret. The worker needs it to sign, so it comes back from the claim —
-- but sealed, exactly as stored. Unsealing happens in Go, in the process
-- that already holds the key.
-- ------------------------------------------------------------------------
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'stillhouse_webhook') THEN
        CREATE ROLE stillhouse_webhook NOLOGIN BYPASSRLS;
    END IF;
END $$;

GRANT SELECT, UPDATE ON webhook_deliveries TO stillhouse_webhook;
GRANT SELECT           ON webhook_endpoints TO stillhouse_webhook;

-- Claim up to p_limit deliveries that are due, marking the attempt as it
-- goes. FOR UPDATE SKIP LOCKED so two workers — or one worker and its
-- replacement during a rolling restart — never send the same delivery
-- twice at the same moment.
CREATE OR REPLACE FUNCTION webhook_claim_due(p_limit INT)
RETURNS TABLE (
    id UUID, tenant_id UUID, endpoint_id UUID, kind webhook_event_kind,
    payload JSONB, attempts INT, url TEXT, secret_sealed BYTEA
)
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
    WITH claimed AS (
        SELECT d.id
        FROM webhook_deliveries d
        JOIN webhook_endpoints e ON e.id = d.endpoint_id
        WHERE d.status = 'pending'
          AND d.next_attempt_at <= NOW()
          AND e.enabled
        ORDER BY d.next_attempt_at
        LIMIT p_limit
        FOR UPDATE OF d SKIP LOCKED
    ), bumped AS (
        UPDATE webhook_deliveries d
        SET attempts = d.attempts + 1
        FROM claimed c
        WHERE d.id = c.id
        RETURNING d.id, d.tenant_id, d.endpoint_id, d.kind, d.payload, d.attempts
    )
    SELECT b.id, b.tenant_id, b.endpoint_id, b.kind, b.payload, b.attempts,
           e.url, e.secret_sealed
    FROM bumped b
    JOIN webhook_endpoints e ON e.id = b.endpoint_id;
$$;

-- Record the outcome. p_retry_after NULL means do not retry: either it
-- succeeded, or the attempts are spent.
CREATE OR REPLACE FUNCTION webhook_record_result(
    p_id UUID, p_ok BOOLEAN, p_code INT, p_error TEXT, p_retry_after INTERVAL
)
RETURNS VOID
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog, public
AS $$
    UPDATE webhook_deliveries
    SET status = CASE
                   WHEN p_ok THEN 'delivered'::webhook_delivery_status
                   WHEN p_retry_after IS NULL THEN 'failed'::webhook_delivery_status
                   ELSE 'pending'::webhook_delivery_status
                 END,
        delivered_at     = CASE WHEN p_ok THEN NOW() ELSE delivered_at END,
        last_status_code = p_code,
        last_error       = COALESCE(p_error, ''),
        next_attempt_at  = CASE WHEN p_retry_after IS NULL THEN next_attempt_at
                                ELSE NOW() + p_retry_after END
    WHERE id = p_id;
$$;

ALTER FUNCTION webhook_claim_due(INT)                             OWNER TO stillhouse_webhook;
ALTER FUNCTION webhook_record_result(UUID, BOOLEAN, INT, TEXT, INTERVAL) OWNER TO stillhouse_webhook;

REVOKE ALL ON FUNCTION webhook_claim_due(INT)                             FROM PUBLIC;
REVOKE ALL ON FUNCTION webhook_record_result(UUID, BOOLEAN, INT, TEXT, INTERVAL) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION webhook_claim_due(INT)                             TO stillhouse_app;
GRANT EXECUTE ON FUNCTION webhook_record_result(UUID, BOOLEAN, INT, TEXT, INTERVAL) TO stillhouse_app;
