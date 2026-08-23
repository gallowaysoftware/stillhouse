-- name: SaveTaxRate :one
INSERT INTO tax_rates (
    tenant_id, jurisdiction, name, rate, effective_from,
    registration_no, provenance, authority, notes, created_by
) VALUES (
    $1, $2, $3, $4, $5, $6,
    sqlc.arg(provenance)::requirement_provenance, $7, $8, $9
)
ON CONFLICT (tenant_id, jurisdiction, name, effective_from) DO UPDATE
SET rate            = EXCLUDED.rate,
    registration_no = EXCLUDED.registration_no,
    provenance      = EXCLUDED.provenance,
    authority       = EXCLUDED.authority,
    notes           = EXCLUDED.notes
RETURNING *;

-- name: ListTaxRates :many
SELECT * FROM tax_rates ORDER BY jurisdiction, name, effective_from DESC;

-- name: DeleteTaxRate :exec
DELETE FROM tax_rates WHERE id = $1;

-- name: TaxRatesInForce :many
-- Every tax applying to a jurisdiction on a date: the ones recorded for
-- that jurisdiction and the ones recorded for everywhere. Only the most
-- recent of each name is returned, so superseding a rate means adding a
-- new row rather than editing the old one — an invoice already issued
-- keeps the rate it was issued at.
SELECT DISTINCT ON (name) *
FROM tax_rates
WHERE (jurisdiction = '' OR jurisdiction = sqlc.arg(jurisdiction)::text)
  AND effective_from <= sqlc.arg(on_date)::date
ORDER BY name, effective_from DESC;

-- name: NextInvoiceNo :one
SELECT COALESCE(MAX(invoice_no), 0)::INTEGER + 1 AS next
FROM invoices WHERE kind = sqlc.arg(kind)::invoice_kind;

-- name: CreateInvoice :one
INSERT INTO invoices (
    tenant_id, kind, invoice_no, customer_id, sales_order_id, shipment_id,
    credits_invoice_id, terms_days, currency, bill_to_name, bill_to_address,
    customer_reference, notes, created_by
) VALUES (
    $1, sqlc.arg(kind)::invoice_kind, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
) RETURNING *;

-- name: GetInvoice :one
SELECT i.*, c.name AS customer_name, c.jurisdiction AS customer_jurisdiction
FROM invoices i JOIN customers c ON c.id = i.customer_id
WHERE i.id = $1;

-- name: GetInvoiceForUpdate :one
SELECT * FROM invoices WHERE id = $1 FOR UPDATE;

-- name: ListInvoices :many
SELECT i.*, c.name AS customer_name, c.jurisdiction AS customer_jurisdiction,
       COALESCE(t.total, 0)::numeric  AS total,
       COALESCE(p.paid, 0)::numeric   AS paid
FROM invoices i
JOIN customers c ON c.id = i.customer_id
LEFT JOIN LATERAL (
    SELECT SUM(l.line_total + l.tax_amount) AS total
    FROM invoice_lines l WHERE l.invoice_id = i.id
) t ON TRUE
LEFT JOIN LATERAL (
    SELECT SUM(pay.amount) AS paid
    FROM invoice_payments pay WHERE pay.invoice_id = i.id
) p ON TRUE
WHERE (NOT sqlc.arg(open_only)::boolean OR i.status IN ('draft', 'issued', 'part_paid'))
ORDER BY i.issue_date DESC NULLS FIRST, i.invoice_no DESC;

-- name: AddInvoiceLine :one
INSERT INTO invoice_lines (
    tenant_id, invoice_id, product_id, description, quantity,
    unit_price, line_total, tax_name, tax_rate, tax_amount, sort_order
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: DeleteInvoiceLine :exec
DELETE FROM invoice_lines WHERE id = $1;

-- name: DeleteInvoiceLines :exec
DELETE FROM invoice_lines WHERE invoice_id = $1;

-- name: ListInvoiceLines :many
SELECT * FROM invoice_lines WHERE invoice_id = $1 ORDER BY sort_order, created_at;

-- name: IssueInvoice :one
UPDATE invoices
SET status     = 'issued',
    issue_date = sqlc.arg(issue_date)::date,
    due_date   = sqlc.arg(due_date)::date,
    issued_at  = NOW(),
    issued_by  = sqlc.narg(issued_by)::uuid,
    bill_to_name    = sqlc.arg(bill_to_name)::text,
    bill_to_address = sqlc.arg(bill_to_address)::text,
    terms_days      = sqlc.arg(terms_days)::int,
    updated_at = NOW()
WHERE id = sqlc.arg(id) AND status = 'draft'
RETURNING *;

-- name: SetInvoicePaymentStatus :one
UPDATE invoices SET status = sqlc.arg(status)::invoice_status, updated_at = NOW()
WHERE id = sqlc.arg(id) RETURNING *;

-- name: VoidInvoice :one
UPDATE invoices
SET status = 'void', voided_at = NOW(), void_reason = $2, updated_at = NOW()
WHERE id = $1 AND status <> 'void'
RETURNING *;

-- name: RecordInvoicePayment :one
INSERT INTO invoice_payments (
    tenant_id, invoice_id, received_on, amount, method, reference, notes, recorded_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListInvoicePayments :many
SELECT * FROM invoice_payments WHERE invoice_id = $1 ORDER BY received_on, created_at;

-- name: InvoiceTotals :one
SELECT
    COALESCE((SELECT SUM(l.line_total) FROM invoice_lines l WHERE l.invoice_id = $1), 0)::numeric AS subtotal,
    COALESCE((SELECT SUM(l.tax_amount) FROM invoice_lines l WHERE l.invoice_id = $1), 0)::numeric AS tax,
    COALESCE((SELECT SUM(p.amount) FROM invoice_payments p WHERE p.invoice_id = $1), 0)::numeric AS paid;

-- name: ARAgeing :many
-- What is owed, by how long it has been owed for.
--
-- Buckets are measured from the due date, not the issue date: an invoice
-- on 60-day terms issued 45 days ago is not overdue, and a report that
-- says it is trains people to ignore the report.
--
-- Credit notes carry negative totals through the same buckets, so a
-- customer with an outstanding credit reads as owing less rather than
-- appearing twice.
SELECT c.id   AS customer_id,
       c.name AS customer_name,
       COALESCE(SUM(o.outstanding), 0)::numeric AS total,
       COALESCE(SUM(o.outstanding) FILTER (WHERE o.days_late <= 0), 0)::numeric  AS current,
       COALESCE(SUM(o.outstanding) FILTER (WHERE o.days_late BETWEEN 1 AND 30), 0)::numeric  AS d1_30,
       COALESCE(SUM(o.outstanding) FILTER (WHERE o.days_late BETWEEN 31 AND 60), 0)::numeric AS d31_60,
       COALESCE(SUM(o.outstanding) FILTER (WHERE o.days_late BETWEEN 61 AND 90), 0)::numeric AS d61_90,
       COALESCE(SUM(o.outstanding) FILTER (WHERE o.days_late > 90), 0)::numeric   AS d90_plus,
       COUNT(*)::int AS invoices
FROM invoices i
JOIN customers c ON c.id = i.customer_id
JOIN LATERAL (
    SELECT
        (CASE WHEN i.kind = 'credit_note' THEN -1 ELSE 1 END
         * COALESCE((SELECT SUM(l.line_total + l.tax_amount)
                       FROM invoice_lines l WHERE l.invoice_id = i.id), 0)
         - COALESCE((SELECT SUM(p.amount)
                       FROM invoice_payments p WHERE p.invoice_id = i.id), 0)
        ) AS outstanding,
        COALESCE(CURRENT_DATE - i.due_date, 0) AS days_late
) o ON TRUE
WHERE i.status IN ('issued', 'part_paid')
GROUP BY c.id, c.name
HAVING COALESCE(SUM(o.outstanding), 0) <> 0
ORDER BY c.name;

-- name: OverdueInvoices :many
-- Issued invoices past their due date with money still on them, for the
-- alert evaluator.
SELECT i.id, i.invoice_no, i.due_date, c.name AS customer_name,
       (COALESCE((SELECT SUM(l.line_total + l.tax_amount)
                    FROM invoice_lines l WHERE l.invoice_id = i.id), 0)
        - COALESCE((SELECT SUM(p.amount)
                      FROM invoice_payments p WHERE p.invoice_id = i.id), 0)
       )::numeric AS outstanding
FROM invoices i
JOIN customers c ON c.id = i.customer_id
WHERE i.kind = 'invoice'
  AND i.status IN ('issued', 'part_paid')
  AND i.due_date IS NOT NULL
  AND i.due_date < CURRENT_DATE
ORDER BY i.due_date;

-- name: ShipmentLinesForInvoicing :many
-- What a shipment actually delivered, priced from the order line it
-- satisfied where there is one. A pick with no order line behind it has
-- no agreed price, and comes back with a null so the caller can say so
-- rather than invoicing at zero.
SELECT sl.id, sl.bottles, p.id AS product_id, p.name AS product_name,
       p.bottle_size_ml, p.target_abv_pct, pi.lot_code,
       sol.unit_price
FROM shipment_lines sl
JOIN packaged_inventory pi ON pi.id = sl.packaged_inventory_id
JOIN products p            ON p.id = pi.product_id
LEFT JOIN sales_order_lines sol ON sol.id = sl.sales_order_line_id
WHERE sl.shipment_id = $1
ORDER BY p.name, pi.lot_code;

-- name: GetInvoiceLineOwner :one
SELECT invoice_id FROM invoice_lines WHERE id = $1;
