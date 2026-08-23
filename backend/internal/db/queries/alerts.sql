-- name: UpsertAlert :one
-- Idempotent by (tenant, kind, subject). An evaluation that finds the
-- same condition still true bumps last_seen_at and refreshes the text —
-- "four days of cover" becomes "two days of cover" without becoming a
-- second alert — but never moves opened_at, because how long a thing has
-- been true is the most useful fact about it.
INSERT INTO alerts (
    tenant_id, kind, severity, subject_key, title, detail, entity_type, entity_id
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (tenant_id, kind, subject_key) WHERE resolved_at IS NULL
DO UPDATE SET
    severity     = EXCLUDED.severity,
    title        = EXCLUDED.title,
    detail       = EXCLUDED.detail,
    entity_type  = EXCLUDED.entity_type,
    entity_id    = EXCLUDED.entity_id,
    last_seen_at = NOW()
RETURNING *;

-- name: ResolveStaleAlerts :many
-- The other half of the life cycle. Anything of these kinds that the
-- evaluation just ran did NOT touch is no longer true, so it closes
-- itself. Scoped to the kinds actually evaluated, so a failure in one
-- rule cannot silently resolve another rule's alerts.
--
-- "This sweep" is NOW(), not a timestamp the caller passes in, and that
-- matters. Inside a transaction NOW() is the transaction's start time
-- and is constant, so every alert this sweep upserted carries exactly
-- it and every alert it did not carries something earlier — the
-- comparison is precise rather than approximate. Passing a Go timestamp
-- instead compares the application's clock against the database's, and
-- any skew either resolves alerts that are still true or leaves ones
-- that are not. This must run in the same transaction as the upserts.
UPDATE alerts
SET resolved_at = NOW()
WHERE resolved_at IS NULL
  -- Compared as text rather than as alert_kind[]: pgx has no encode
  -- plan for an array of a custom enum without registering the type, and
  -- registering one type here to save a cast is the sort of thing that
  -- breaks silently on the next connection pool.
  AND kind::TEXT = ANY(sqlc.arg(kinds)::TEXT[])
  AND last_seen_at < NOW()
RETURNING *;

-- name: ListOpenAlerts :many
SELECT a.*, COALESCE(u.display_name, '') AS acknowledged_by_name
FROM alerts a
LEFT JOIN users u ON u.id = a.acknowledged_by
WHERE a.resolved_at IS NULL
ORDER BY
    CASE a.severity WHEN 'critical' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END,
    a.opened_at DESC;

-- name: ListRecentAlerts :many
SELECT a.*, COALESCE(u.display_name, '') AS acknowledged_by_name
FROM alerts a
LEFT JOIN users u ON u.id = a.acknowledged_by
ORDER BY a.opened_at DESC
LIMIT $1;

-- name: AcknowledgeAlert :one
-- Says a human has seen it. Deliberately not the same as resolving:
-- resolution is a claim about the world and only the evaluator can make
-- it. An acknowledged alert stays open while its condition holds.
UPDATE alerts
SET acknowledged_at = NOW(), acknowledged_by = $2
WHERE id = $1 AND resolved_at IS NULL
RETURNING *;

-- name: ListAlertsNeedingNotification :many
-- Open, unnotified, and worth an email. Info-level alerts never mail;
-- they are for the dashboard, and a system that emails about everything
-- gets filtered.
SELECT * FROM alerts
WHERE resolved_at IS NULL
  AND notified_at IS NULL
  AND severity IN ('warning', 'critical');

-- name: MarkAlertNotified :exec
UPDATE alerts SET notified_at = NOW() WHERE id = $1;

-- name: ListAlertEmailRecipients :many
-- Who hears about it. Viewers are excluded: an alert is a call to act,
-- and someone who cannot act on anything should not be paged about it.
SELECT * FROM users
WHERE alert_email = TRUE
  AND role <> 'viewer'
ORDER BY email;

-- name: SetUserAlertEmail :one
UPDATE users SET alert_email = $2 WHERE id = $1 RETURNING *;

-- name: AlertStaleFermentations :many
-- A live fermentation whose most recent log is older than the cutoff.
-- Both live statuses count: a ferment pitched three days ago with no
-- readings at all is exactly as unattended as one that stopped being
-- read. Either it finished and nobody recorded it, or it is stuck, and
-- both want a person.
SELECT fr.id, fr.fermenter_label, fr.pitch_at,
       MAX(fl.observed_at)::TIMESTAMPTZ AS last_reading_at
FROM fermentation_runs fr
LEFT JOIN fermentation_logs fl ON fl.fermentation_run_id = fr.id
WHERE fr.status IN ('pitched', 'active')
GROUP BY fr.id, fr.fermenter_label, fr.pitch_at
HAVING COALESCE(MAX(fl.observed_at), fr.pitch_at) < sqlc.arg(cutoff)::TIMESTAMPTZ;

-- name: AlertUnmeasuredBarrels :many
-- A filled cask whose last gauge — fill or regauge — predates the cutoff.
-- This is a records question before it is an operational one: a balance
-- you have not measured in over a year is a balance you cannot evidence.
SELECT bc.id, bc.name, bc.current_laa,
       MAX(be.event_date)::TIMESTAMPTZ AS last_measured_at
FROM bulk_containers bc
JOIN barrel_attributes ba ON ba.container_id = bc.id
LEFT JOIN barrel_events be
       ON be.container_id = bc.id
      AND be.voided_at IS NULL
      AND be.kind IN ('fill', 'regauge')
WHERE bc.archived = FALSE
  AND bc.current_volume_l > 0
GROUP BY bc.id, bc.name, bc.current_laa
HAVING MAX(be.event_date) IS NULL OR MAX(be.event_date) < sqlc.arg(cutoff)::TIMESTAMPTZ;
