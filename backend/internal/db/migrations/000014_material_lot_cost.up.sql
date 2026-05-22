-- Per-lot unit cost. NULL means "not recorded" so cost rollups gracefully
-- degrade for tenants who haven't started entering prices yet. Distillers
-- pricing bottles need the unit cost trail back to the grain receipt.
ALTER TABLE material_lots
    ADD COLUMN unit_cost_cad DOUBLE PRECISION CHECK (unit_cost_cad >= 0);
