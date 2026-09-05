-- distillation_readings: what the instruments at the still said at a moment
-- during a run — the run's log, and the data a still is calibrated from.
-- Every measure is nullable: a reading records what was taken.
CREATE TABLE distillation_readings (
    id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id                 UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    distillation_run_id       UUID NOT NULL REFERENCES distillation_runs(id) ON DELETE CASCADE,
    observed_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    distillate_abv_pct        DOUBLE PRECISION CHECK (distillate_abv_pct >= 0 AND distillate_abv_pct <= 100),
    distillate_temperature_c  DOUBLE PRECISION,
    heat_setting_pct          DOUBLE PRECISION CHECK (heat_setting_pct >= 0 AND heat_setting_pct <= 100),
    pot_temperature_c         DOUBLE PRECISION,
    vapour_temperature_c      DOUBLE PRECISION,
    notes                     TEXT NOT NULL DEFAULT ''
);

CREATE INDEX distillation_readings_run_observed_idx
    ON distillation_readings (distillation_run_id, observed_at);

ALTER TABLE distillation_readings ENABLE ROW LEVEL SECURITY;
ALTER TABLE distillation_readings FORCE  ROW LEVEL SECURITY;
CREATE POLICY distillation_readings_tenant ON distillation_readings
    FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));
