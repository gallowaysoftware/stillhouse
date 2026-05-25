-- Whisky tasting bench — axes derived from the Scotch Whisky Research
-- Institute (SWRI) Flavour Wheel, the canonical sensory vocabulary
-- introduced in the 1970s and still the reference framework taught in
-- the IBD Diploma in Distilling (Module 2, Lesson 6 — Sensory Analysis).
--
-- SWRI primary classes (8):
--   - cereal   — porridge / husky / malt / biscuit / cracker (raw-materials origin)
--   - estery   — fruity esters: banana / pear-drop / apple / pineapple / citrus / dried fruit (fermentation origin)
--   - floral   — geranium / rose / fragrant / honey (fermentation + light cask origin)
--   - peaty    — phenolic / smoky / medicinal / iodine / bonfire (kilning origin, in peated styles)
--   - feinty   — leather / tobacco / honey-tobacco / horse / cheesy (tail-cut + maturation character)
--   - sulphury — rubbery / vegetative / sandy / gunflint / DMS / cabbage (PRIMARILY AN OFF-NOTE; low score = clean spirit)
--   - woody    — vanilla / toasted oak / resinous / coconut / sawdust (cask-derived)
--   - winey    — sherry / port / brandy / wine-soaked oak (from finishing or sherry casks)
--
-- Plus three standard panel-scorecard axes that aren't on the wheel but
-- are universally scored on a whisky tasting:
--   - body     — mouthfeel / weight / texture
--   - finish   — length / persistence / dryness
--   - overall  — gut-call quality / hedonic
--
-- All 0-10 (SWRI's published scorecards use 0-10), NULL = "not tasted
-- on this axis." For sulphury specifically a LOW score is desirable
-- (it's an off-note class) — the bench captures the intensity, not the
-- positive valence.
--
-- Only versions of a recipe whose spirit_kind ∈ (whisky, canadian_whisky,
-- rye_whisky) get rows here — enforced at the RPC layer, same as the
-- gin gate on recipe_version_sensory.
CREATE TABLE recipe_version_whisky_sensory (
    recipe_version_id UUID PRIMARY KEY REFERENCES recipe_versions(id) ON DELETE CASCADE,
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    cereal            SMALLINT,
    estery            SMALLINT,
    floral            SMALLINT,
    peaty             SMALLINT,
    feinty            SMALLINT,
    sulphury          SMALLINT,
    woody             SMALLINT,
    winey             SMALLINT,
    body              SMALLINT,
    finish            SMALLINT,
    overall           SMALLINT,
    tasting_panel     TEXT NOT NULL DEFAULT '',
    tasted_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT whisky_sensory_cereal_chk   CHECK (cereal   IS NULL OR cereal   BETWEEN 0 AND 10),
    CONSTRAINT whisky_sensory_estery_chk   CHECK (estery   IS NULL OR estery   BETWEEN 0 AND 10),
    CONSTRAINT whisky_sensory_floral_chk   CHECK (floral   IS NULL OR floral   BETWEEN 0 AND 10),
    CONSTRAINT whisky_sensory_peaty_chk    CHECK (peaty    IS NULL OR peaty    BETWEEN 0 AND 10),
    CONSTRAINT whisky_sensory_feinty_chk   CHECK (feinty   IS NULL OR feinty   BETWEEN 0 AND 10),
    CONSTRAINT whisky_sensory_sulphury_chk CHECK (sulphury IS NULL OR sulphury BETWEEN 0 AND 10),
    CONSTRAINT whisky_sensory_woody_chk    CHECK (woody    IS NULL OR woody    BETWEEN 0 AND 10),
    CONSTRAINT whisky_sensory_winey_chk    CHECK (winey    IS NULL OR winey    BETWEEN 0 AND 10),
    CONSTRAINT whisky_sensory_body_chk     CHECK (body     IS NULL OR body     BETWEEN 0 AND 10),
    CONSTRAINT whisky_sensory_finish_chk   CHECK (finish   IS NULL OR finish   BETWEEN 0 AND 10),
    CONSTRAINT whisky_sensory_overall_chk  CHECK (overall  IS NULL OR overall  BETWEEN 0 AND 10)
);

ALTER TABLE recipe_version_whisky_sensory ENABLE ROW LEVEL SECURITY;
CREATE POLICY recipe_version_whisky_sensory_tenant_isolation ON recipe_version_whisky_sensory
    FOR ALL
    USING      (tenant_id::text = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id::text = current_setting('app.current_tenant_id', true));

GRANT SELECT, INSERT, UPDATE, DELETE ON recipe_version_whisky_sensory TO stillhouse_app;
