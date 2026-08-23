-- 000043_sku_registry: what a product needs to leave the building.
--
-- A product carried a name, a bottle size, a strength and free-text
-- label notes. That is enough to bottle against and not enough to sell:
-- a board wants its own product number, a distributor wants a GTIN and a
-- case configuration, a label has to carry what the Food and Drug
-- Regulations and Excise Act s.87 require, and an e-commerce listing
-- wants all of it.
--
-- The discipline here is the same as everywhere else that touches a
-- regulator: Stillhouse models the *fields* and never invents the
-- *values*. It does not decide that a spirit meets the standard of
-- identity for Canadian Whisky, or that a declaration is worded
-- correctly — those are the licensee's statements to make. What it does
-- is give them somewhere to live that is attached to the SKU, so a
-- label, a listing and a board submission are drawing on one record
-- instead of three spreadsheets.

ALTER TABLE products
    -- Trade identifiers. GTIN-12/13/14; stored as text because a leading
    -- zero is significant and a check digit is not arithmetic anybody
    -- should be doing in a spreadsheet.
    ADD COLUMN gtin                  TEXT NOT NULL DEFAULT '',
    -- The board's own number for this SKU. CSPC is the national one;
    -- provincial boards issue their own and a distillery selling into
    -- three provinces holds three.
    ADD COLUMN cspc_code             TEXT NOT NULL DEFAULT '',

    -- Case configuration. Bottles per case is what a purchase order is
    -- written in; the rest is what a pallet is planned from.
    ADD COLUMN bottles_per_case      INTEGER CHECK (bottles_per_case IS NULL OR bottles_per_case > 0),
    ADD COLUMN cases_per_layer       INTEGER CHECK (cases_per_layer IS NULL OR cases_per_layer > 0),
    ADD COLUMN layers_per_pallet     INTEGER CHECK (layers_per_pallet IS NULL OR layers_per_pallet > 0),
    ADD COLUMN case_gross_weight_kg  DOUBLE PRECISION CHECK (case_gross_weight_kg IS NULL OR case_gross_weight_kg > 0),

    -- Label content.
    --
    -- common_name is the standard-of-identity name under Division 2 of
    -- the Food and Drug Regulations — "Canadian Whisky", "Gin", "Vodka".
    -- It is deliberately separate from the SKU's marketing name and
    -- deliberately NOT derived from spirit_kind: whether a spirit
    -- qualifies for a standardised common name is the licensee's
    -- declaration, resting on how it was made and how long it sat, and
    -- Stillhouse asserting it on their behalf would be putting words in
    -- their mouth on a label.
    ADD COLUMN common_name           TEXT NOT NULL DEFAULT '',
    -- The statement of age, where one is made. Also the licensee's, and
    -- also not derived: the maturation clock knows how long a cask sat,
    -- and what a blend may claim is a different question.
    ADD COLUMN age_statement         TEXT NOT NULL DEFAULT '',
    -- Excise Act s.87 (EDM3-2-3) requires certain information on a
    -- container of packaged spirits. This is where the licensee records
    -- what theirs carries.
    ADD COLUMN container_marking     TEXT NOT NULL DEFAULT '',
    ADD COLUMN allergen_statement    TEXT NOT NULL DEFAULT '',
    ADD COLUMN country_of_origin     TEXT NOT NULL DEFAULT '',
    -- Free text, for anything a listing asks for that has no field.
    ADD COLUMN marketing_description TEXT NOT NULL DEFAULT '';

-- GTIN is unique per tenant where present. A GTIN identifies a trade
-- item; two SKUs sharing one is a data-entry error that would send the
-- wrong case to a distributor.
CREATE UNIQUE INDEX products_gtin_idx ON products (tenant_id, gtin)
    WHERE gtin <> '';
