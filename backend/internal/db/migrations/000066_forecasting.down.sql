DROP TABLE IF EXISTS demand_forecasts;
ALTER TABLE tenants DROP COLUMN IF EXISTS forecast_trailing_months;
ALTER TABLE tenants DROP COLUMN IF EXISTS forecast_method;
DROP TYPE IF EXISTS forecast_method;
