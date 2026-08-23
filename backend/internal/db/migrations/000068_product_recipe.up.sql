-- 000068_product_recipe: which recipe a product is made from.
--
-- Stillhouse has been able to say what a bottling run drew from since the
-- traceability work, by walking back through the container it was filled
-- from. That is the right answer for a run that happened. It is no answer
-- at all for a run that has not: planning next month's production needs
-- to know, before anything is made, that the gin comes from this bill.
--
-- The link is nullable and operator-supplied, and unset refuses rather
-- than guessing. Inferring it from the last run that produced this
-- product would be right most of the time and wrong exactly when a
-- distillery has changed a recipe — which is the moment somebody is most
-- likely to be planning.

ALTER TABLE products ADD COLUMN recipe_version_id UUID
    REFERENCES recipe_versions(id) ON DELETE SET NULL;

CREATE INDEX products_recipe_idx ON products (recipe_version_id)
    WHERE recipe_version_id IS NOT NULL;

COMMENT ON COLUMN products.recipe_version_id IS
    'The recipe this product is planned from. NULL means unstated, and material requirements are refused rather than inferred from past runs.';
