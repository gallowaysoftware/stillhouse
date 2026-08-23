-- 000039_alerts: a dashboard nobody opens on a Tuesday is not an alert.
--
-- Stillhouse computes plenty of things worth knowing before they become
-- problems — a return due in nine days, stamps with four days of cover
-- left, a fermentation that stopped reporting, a cask nobody has put a
-- dipstick in since last spring. All of it was reachable only by someone
-- going to look, which is the same as not having it on the week it
-- matters.
--
-- An alert is a fact the system noticed, with a life cycle: it opens
-- when the condition becomes true, stays open while it remains true, and
-- resolves on its own when it stops. Nothing here is dismissed by
-- clicking; acknowledging says a human has seen it, which is a different
-- claim from the condition having gone away.

CREATE TYPE alert_kind AS ENUM (
    'filing_due',            -- a return is due soon and is not submitted
    'filing_overdue',        -- its due date has passed
    'stamps_low',            -- excise stamps below a week of cover
    'fermentation_stalled',  -- an active ferment with no recent reading
    'barrel_unmeasured'      -- a cask with no regauge in over a year
);

CREATE TYPE alert_severity AS ENUM ('info', 'warning', 'critical');

CREATE TABLE alerts (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    kind           alert_kind NOT NULL,
    severity       alert_severity NOT NULL,

    -- What this alert is *about*, within its kind: a period id, a barrel
    -- id, a jurisdiction code. Re-evaluating must update the existing
    -- alert rather than pile up a new one every fifteen minutes, and
    -- this is the key that makes that possible.
    subject_key    TEXT NOT NULL DEFAULT '',

    title          TEXT NOT NULL,
    detail         TEXT NOT NULL,

    -- Where to send someone who wants to act on it.
    entity_type    TEXT NOT NULL DEFAULT '',
    entity_id      UUID,

    opened_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Bumped on every evaluation that still finds the condition true.
    -- An open alert whose last_seen_at has gone stale means the
    -- evaluator stopped running, which is worth being able to see.
    last_seen_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Set by the evaluator when the condition stops being true. Alerts
    -- resolve themselves; a person cannot mark one resolved, because
    -- that would be asserting something about the world rather than
    -- about their own attention.
    resolved_at    TIMESTAMPTZ,
    acknowledged_at TIMESTAMPTZ,
    acknowledged_by UUID REFERENCES users(id) ON DELETE SET NULL,
    -- When the email went out, so a restart doesn't re-send.
    notified_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One open alert per subject. Partial, so the history of resolved ones
-- stays — a fermentation that stalled three times is worth being able to
-- see three times.
CREATE UNIQUE INDEX alerts_open_subject_idx
    ON alerts (tenant_id, kind, subject_key)
    WHERE resolved_at IS NULL;

CREATE INDEX alerts_tenant_open_idx
    ON alerts (tenant_id, resolved_at, severity, opened_at DESC);

ALTER TABLE alerts ENABLE ROW LEVEL SECURITY;
ALTER TABLE alerts FORCE  ROW LEVEL SECURITY;
CREATE POLICY alerts_tenant ON alerts FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- Per-person, because the operator who wants to know a ferment stalled
-- is not always the person who wants to know a return is due, and one
-- shared switch means somebody turns the whole thing off.
ALTER TABLE users
    ADD COLUMN alert_email BOOLEAN NOT NULL DEFAULT TRUE;

GRANT SELECT, INSERT, UPDATE, DELETE ON alerts TO stillhouse_app;
