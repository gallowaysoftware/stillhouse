-- alert_kind keeps 'invoice_overdue'; Postgres cannot remove an enum
-- value without rewriting the type, and a kind nothing raises costs
-- nothing.
DROP TABLE IF EXISTS invoice_payments;
DROP TABLE IF EXISTS invoice_lines;
DROP TABLE IF EXISTS invoices;
DROP TABLE IF EXISTS tax_rates;
DROP TYPE IF EXISTS invoice_status;
DROP TYPE IF EXISTS invoice_kind;
