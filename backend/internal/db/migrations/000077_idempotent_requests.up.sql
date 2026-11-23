-- A phone that queues a reading offline cannot tell, when the answer is lost,
-- whether it was recorded. It sends the same key again and gets the first
-- answer back instead of a second row.
CREATE TABLE idempotent_requests (
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key         TEXT NOT NULL CHECK (length(key) BETWEEN 16 AND 100),
    procedure   TEXT NOT NULL,
    -- NULL while the request is being handled.
    response    BYTEA,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, user_id, key)
);

CREATE INDEX idempotent_requests_created_idx ON idempotent_requests (created_at);

ALTER TABLE idempotent_requests ENABLE ROW LEVEL SECURITY;
ALTER TABLE idempotent_requests FORCE  ROW LEVEL SECURITY;
CREATE POLICY idempotent_requests_tenant ON idempotent_requests
    FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));
