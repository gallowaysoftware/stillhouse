-- 000003_recipes: versioned recipes for planning projected alcohol output.
--
-- A Recipe is the long-lived identity ("Bourbon-style Rye"); each edit
-- creates a new RecipeVersion holding immutable ingredients + process
-- assumptions. recipes.current_version_id points at the active version,
-- but historical MashRuns (Stage 3) will reference RecipeVersion, not
-- Recipe, so editing a recipe never rewrites the past.

CREATE TYPE spirit_kind AS ENUM (
    'whisky',
    'canadian_whisky',
    'rye_whisky',
    'gin',
    'vodka',
    'rum',
    'brandy',
    'liqueur',
    'other'
);

CREATE TABLE recipes (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name                TEXT NOT NULL,
    spirit_kind         spirit_kind NOT NULL,
    archived            BOOLEAN NOT NULL DEFAULT FALSE,
    current_version_id  UUID,                       -- set after the first version is saved
    notes               TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);

CREATE INDEX recipes_tenant_idx ON recipes (tenant_id) WHERE NOT archived;

CREATE TRIGGER recipes_updated_at
    BEFORE UPDATE ON recipes
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE recipes ENABLE ROW LEVEL SECURITY;
CREATE POLICY recipes_tenant_isolation ON recipes
    FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

-- ------------------------------------------------------------------------
-- recipe_versions: immutable snapshot of a recipe at a point in time.
-- ------------------------------------------------------------------------
CREATE TABLE recipe_versions (
    id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id                   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    recipe_id                   UUID NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
    version_no                  INTEGER NOT NULL,
    notes                       TEXT NOT NULL DEFAULT '',
    -- Process assumptions for projection. All are fractions in (0,1].
    mash_efficiency_pct         DOUBLE PRECISION NOT NULL DEFAULT 0.85,
    ferment_efficiency_pct      DOUBLE PRECISION NOT NULL DEFAULT 0.92,
    distillation_recovery_pct   DOUBLE PRECISION NOT NULL DEFAULT 0.90,
    -- Water added to the mash, used for projecting wash volume + ABV.
    target_water_l              DOUBLE PRECISION,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (recipe_id, version_no),
    CHECK (mash_efficiency_pct       > 0 AND mash_efficiency_pct       <= 1),
    CHECK (ferment_efficiency_pct    > 0 AND ferment_efficiency_pct    <= 1),
    CHECK (distillation_recovery_pct > 0 AND distillation_recovery_pct <= 1)
);

CREATE INDEX recipe_versions_recipe_idx ON recipe_versions (recipe_id, version_no DESC);

ALTER TABLE recipe_versions ENABLE ROW LEVEL SECURITY;
CREATE POLICY recipe_versions_tenant_isolation ON recipe_versions
    FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

ALTER TABLE recipes
    ADD CONSTRAINT recipes_current_version_fk
    FOREIGN KEY (current_version_id) REFERENCES recipe_versions(id);

-- ------------------------------------------------------------------------
-- recipe_ingredients: one row per material in a recipe version.
-- ------------------------------------------------------------------------
CREATE TABLE recipe_ingredients (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    recipe_version_id   UUID NOT NULL REFERENCES recipe_versions(id) ON DELETE CASCADE,
    material_id         UUID NOT NULL REFERENCES materials(id) ON DELETE RESTRICT,
    quantity            DOUBLE PRECISION NOT NULL CHECK (quantity > 0),
    uom                 TEXT NOT NULL,
    notes               TEXT NOT NULL DEFAULT '',
    sort_order          INTEGER NOT NULL DEFAULT 0,
    UNIQUE (recipe_version_id, material_id)
);

CREATE INDEX recipe_ingredients_version_idx ON recipe_ingredients (recipe_version_id, sort_order);

ALTER TABLE recipe_ingredients ENABLE ROW LEVEL SECURITY;
CREATE POLICY recipe_ingredients_tenant_isolation ON recipe_ingredients
    FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));
