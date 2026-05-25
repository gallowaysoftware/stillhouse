-- Gin recipe development support.
--
-- Whisky recipes ride a mash → ferment → distill spine where the recipe
-- version's per-stage efficiency fractions drive the LAA projection.
-- Gin starts from neutral grain spirit (NGS) and the recipe IS the
-- botanical bill — there's no mash, no ferment, and the projection is
-- just "how much LAA do I get back from redistilling X litres of NGS
-- with these botanicals?"
--
-- This migration adds the per-version fields gin needs without
-- breaking the whisky math: every new column is nullable or has a
-- defensive default. SaveRecipeVersion will accept them for any
-- spirit_kind; the gin-specific projection branch reads them when
-- spirit_kind = GIN.

-- Per-version gin process knobs. All nullable so a whisky recipe just
-- leaves them blank.
ALTER TABLE recipe_versions
    ADD COLUMN tasting_notes          TEXT NOT NULL DEFAULT '',
    ADD COLUMN distillation_method    TEXT NOT NULL DEFAULT '',   -- '', 'pot', 'vapor', 'combined'
    ADD COLUMN maceration_hours       DOUBLE PRECISION,
    ADD COLUMN gin_ngs_input_l        DOUBLE PRECISION,
    ADD COLUMN gin_ngs_input_abv_pct  DOUBLE PRECISION,
    ADD CONSTRAINT recipe_versions_distillation_method_chk
        CHECK (distillation_method IN ('', 'pot', 'vapor', 'combined')),
    ADD CONSTRAINT recipe_versions_maceration_hours_chk
        CHECK (maceration_hours IS NULL OR maceration_hours >= 0),
    ADD CONSTRAINT recipe_versions_ngs_input_l_chk
        CHECK (gin_ngs_input_l IS NULL OR gin_ngs_input_l > 0),
    ADD CONSTRAINT recipe_versions_ngs_input_abv_chk
        CHECK (gin_ngs_input_abv_pct IS NULL OR (gin_ngs_input_abv_pct > 0 AND gin_ngs_input_abv_pct <= 100));

-- Botanical role hints what each ingredient contributes to the recipe.
-- Empty string is the default for non-botanical ingredients (e.g. NGS,
-- grain on a whisky recipe).
ALTER TABLE recipe_ingredients
    ADD COLUMN botanical_role TEXT NOT NULL DEFAULT '',
    ADD CONSTRAINT recipe_ingredients_botanical_role_chk
        CHECK (botanical_role IN ('', 'juniper', 'citrus', 'herbal', 'spice', 'root', 'floral', 'other'));

-- Sensory scores per version — the bench tool for iterating a gin
-- recipe. 0-10 scale on each axis; NULL means "not tasted on this
-- axis." One row per version, separate from recipe_versions to keep
-- that table narrow.
CREATE TABLE recipe_version_sensory (
    recipe_version_id UUID PRIMARY KEY REFERENCES recipe_versions(id) ON DELETE CASCADE,
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    juniper           SMALLINT,
    citrus            SMALLINT,
    herbal            SMALLINT,
    spice             SMALLINT,
    floral            SMALLINT,
    earth             SMALLINT,
    body              SMALLINT,
    heat              SMALLINT,
    balance           SMALLINT,
    overall           SMALLINT,
    tasting_panel     TEXT NOT NULL DEFAULT '',     -- "self", "Kyle + Jane", etc.
    tasted_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT recipe_sensory_juniper_chk CHECK (juniper IS NULL OR juniper BETWEEN 0 AND 10),
    CONSTRAINT recipe_sensory_citrus_chk  CHECK (citrus  IS NULL OR citrus  BETWEEN 0 AND 10),
    CONSTRAINT recipe_sensory_herbal_chk  CHECK (herbal  IS NULL OR herbal  BETWEEN 0 AND 10),
    CONSTRAINT recipe_sensory_spice_chk   CHECK (spice   IS NULL OR spice   BETWEEN 0 AND 10),
    CONSTRAINT recipe_sensory_floral_chk  CHECK (floral  IS NULL OR floral  BETWEEN 0 AND 10),
    CONSTRAINT recipe_sensory_earth_chk   CHECK (earth   IS NULL OR earth   BETWEEN 0 AND 10),
    CONSTRAINT recipe_sensory_body_chk    CHECK (body    IS NULL OR body    BETWEEN 0 AND 10),
    CONSTRAINT recipe_sensory_heat_chk    CHECK (heat    IS NULL OR heat    BETWEEN 0 AND 10),
    CONSTRAINT recipe_sensory_balance_chk CHECK (balance IS NULL OR balance BETWEEN 0 AND 10),
    CONSTRAINT recipe_sensory_overall_chk CHECK (overall IS NULL OR overall BETWEEN 0 AND 10)
);

ALTER TABLE recipe_version_sensory ENABLE ROW LEVEL SECURITY;
CREATE POLICY recipe_version_sensory_tenant_isolation ON recipe_version_sensory
    FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

GRANT SELECT, INSERT, UPDATE, DELETE ON recipe_version_sensory TO stillhouse_app;
