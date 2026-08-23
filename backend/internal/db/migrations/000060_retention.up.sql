-- Records retention, as a commitment rather than an intention.
--
-- Subsection 206(1) requires records sufficient to determine compliance,
-- and six years is the working window. Stillhouse deletes almost nothing
-- already — movements, gauges, runs and removals are all append-only or
-- void-and-keep — but "almost nothing" is not a policy, and a licensee
-- asked what their retention is cannot answer from the code.
--
-- Two things here, and neither is a promise Stillhouse makes on the
-- licensee's behalf:
--
--   the policy    the window the licensee states they keep, and the
--                 notes describing their backup cadence and restore
--                 story. Stillhouse records what was decided; it does
--                 not decide it, and it does not default to six years,
--                 because a stated policy nobody stated is not one.
--
--   legal holds   a named, dated instruction that nothing be removed.
--                 While one is open the handful of paths that really do
--                 delete a row refuse — which is the whole point of a
--                 hold, and is checked in one place so a new delete
--                 cannot quietly escape it.

CREATE TABLE retention_policies (
    tenant_id      UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    -- Years. NULL means the licensee has not stated one, which the
    -- screens say rather than assuming six.
    retention_years INTEGER CHECK (retention_years IS NULL OR retention_years > 0),
    -- Their own words: how backups are taken, where they go, how often a
    -- restore is actually exercised. Stillhouse cannot know any of it.
    backup_cadence TEXT NOT NULL DEFAULT '',
    restore_notes  TEXT NOT NULL DEFAULT '',
    -- The date the policy was last reviewed. A retention policy nobody
    -- has looked at in four years is a document, not a control.
    reviewed_on    DATE,
    reviewed_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    notes          TEXT NOT NULL DEFAULT '',
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE legal_holds (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id      UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    -- What the hold is about, in the words of whoever instructed it.
    reason         TEXT NOT NULL CHECK (length(trim(reason)) > 0),
    -- Who asked for it: an auditor, counsel, CRA. Named, because a hold
    -- with no source is one nobody can lift with confidence.
    instructed_by  TEXT NOT NULL DEFAULT '',
    reference      TEXT NOT NULL DEFAULT '',
    placed_on      DATE NOT NULL DEFAULT CURRENT_DATE,
    placed_by      UUID NOT NULL REFERENCES users(id),
    released_on    DATE,
    released_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    release_reason TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT legal_hold_release_has_reason CHECK (
        released_on IS NULL OR length(trim(release_reason)) > 0
    )
);

DO $$
DECLARE t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['retention_policies', 'legal_holds'] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format($p$CREATE POLICY %I ON %I
            USING (tenant_id = current_setting('app.current_tenant_id')::uuid)
            WITH CHECK (tenant_id = current_setting('app.current_tenant_id')::uuid)$p$,
            t || '_tenant_isolation', t);
        EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO stillhouse_app', t);
    END LOOP;
END $$;

CREATE INDEX legal_holds_open_idx ON legal_holds (tenant_id) WHERE released_on IS NULL;
