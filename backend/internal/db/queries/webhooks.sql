-- name: ListWebhookEndpoints :many
-- kinds comes back as text[] rather than the enum array: pgx has no
-- encode plan for a custom enum array without registering the type on
-- every pool, and a driver-level registration that one code path forgets
-- fails at run time rather than at compile time. The cast keeps the enum
-- doing its job in the column while the wire stays ordinary strings.
SELECT id, tenant_id, url, secret_sealed, CAST(kinds AS text[]) AS kinds,
       enabled, description, created_at, updated_at
FROM webhook_endpoints ORDER BY created_at;

-- name: GetWebhookEndpoint :one
SELECT id, tenant_id, url, secret_sealed, CAST(kinds AS text[]) AS kinds,
       enabled, description, created_at, updated_at
FROM webhook_endpoints WHERE id = $1;

-- name: CreateWebhookEndpoint :one
-- The double cast is what validates: text[] is what pgx can send,
-- webhook_event_kind[] is what the column accepts, and an unknown string
-- is refused by the cast rather than stored.
INSERT INTO webhook_endpoints (tenant_id, url, secret_sealed, kinds, description)
VALUES ($1, $2, $3, CAST(CAST(sqlc.arg(kinds) AS text[]) AS webhook_event_kind[]), $4)
RETURNING id, tenant_id, url, secret_sealed, CAST(kinds AS text[]) AS kinds,
          enabled, description, created_at, updated_at;

-- name: SetWebhookEndpointEnabled :one
UPDATE webhook_endpoints SET enabled = $2 WHERE id = $1
RETURNING id, tenant_id, url, secret_sealed, CAST(kinds AS text[]) AS kinds,
          enabled, description, created_at, updated_at;

-- name: DeleteWebhookEndpoint :exec
DELETE FROM webhook_endpoints WHERE id = $1;

-- name: EnqueueWebhookDelivery :exec
-- Written in the same transaction as the event it describes. See 000062:
-- an outbox row that only appears after commit has a window where the
-- process dies and the event is silently never delivered.
--
-- One row per subscribed endpoint, selected here rather than by the
-- caller so a new endpoint cannot be missed by a write path that forgot
-- to check.
INSERT INTO webhook_deliveries (tenant_id, endpoint_id, kind, payload)
SELECT e.tenant_id, e.id, CAST(CAST(sqlc.arg(kind) AS text) AS webhook_event_kind), @payload::jsonb
FROM webhook_endpoints e
WHERE e.enabled
  AND CAST(CAST(sqlc.arg(kind) AS text) AS webhook_event_kind) = ANY(e.kinds);

-- name: ListWebhookDeliveries :many
-- What the operator looks at when something did not arrive.
SELECT d.*, e.url
FROM webhook_deliveries d
JOIN webhook_endpoints e ON e.id = d.endpoint_id
ORDER BY d.created_at DESC
LIMIT @row_limit::int;

-- name: ClaimDueWebhookDeliveries :many
-- Through the keyhole; see 000062. The worker has no tenant context
-- because it is not acting for a tenant.
--
-- Columns are cast rather than star-selected: sqlc cannot infer the shape
-- of a set-returning function and produces []interface{} for one, which
-- moves every type error from compile time to run time.
SELECT c.id::uuid                    AS id,
       c.tenant_id::uuid             AS tenant_id,
       c.endpoint_id::uuid           AS endpoint_id,
       c.kind::text                  AS kind,
       c.payload::jsonb              AS payload,
       c.attempts::int               AS attempts,
       c.url::text                   AS url,
       c.secret_sealed::bytea        AS secret_sealed
FROM webhook_claim_due(@row_limit::int) c;

-- name: RecordWebhookResult :exec
SELECT webhook_record_result(@id::uuid, @ok::boolean, @code::int, @err::text, @retry_after::interval);
