package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/money"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

// moneyScale is the stored scale for amounts and quantities. Four digits
// rather than two, because a unit price of $0.5525 a kilogram is an
// ordinary thing and rounding it to the cent at rest would round it away.
// Totals are still presented at two.
const moneyScale = 4

type InvoicingService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewInvoicingService(db *tenantdb.DB, logger *slog.Logger) *InvoicingService {
	return &InvoicingService{db: db, logger: logger}
}

func (s *InvoicingService) fail(op string, err error) error {
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return connectErr
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return connect.NewError(connect.CodeNotFound, errors.New("not found"))
	}
	s.logger.Error(op, "err", err)
	return connect.NewError(connect.CodeInternal, errors.New("internal error"))
}

// ----- tax rates ------------------------------------------------------------

func (s *InvoicingService) SaveTaxRate(
	ctx context.Context,
	req *connect.Request[stillhousev1.SaveTaxRateRequest],
) (*connect.Response[stillhousev1.SaveTaxRateResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	jur := strings.ToUpper(strings.TrimSpace(in.GetJurisdiction()))
	if jur != "" && !validJurisdiction(jur) {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("jurisdiction must be a code like CA-ON, or blank for one "+
				"that applies everywhere"))
	}
	if strings.TrimSpace(in.GetName()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("name the tax as it appears on the invoice — GST, HST, QST"))
	}
	rate, err := money.Parse(in.GetRate())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	// A rate entered as 13 instead of 0.13 would multiply every invoice by
	// fourteen. The column's CHECK catches it; this is the version with a
	// sentence attached, because the mistake is easy and the failure is
	// large.
	if rate.Sign() < 0 || rate.Cmp(money.MustParse("1")) > 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("the rate is a fraction — 0.13 for thirteen percent, not 13"))
	}
	prov, err := requirementProvenanceToDB(in.GetProvenance())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if prov == sqlcgen.RequirementProvenanceSourced &&
		strings.TrimSpace(in.GetAuthority()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("a sourced rate has to cite its source"))
	}
	effective, err := parseDateOrToday(in.GetEffectiveFrom())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rateNum, err := rate.Numeric(6)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var out sqlcgen.TaxRate
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		out, e = q.SaveTaxRate(ctx, sqlcgen.SaveTaxRateParams{
			TenantID: u.TenantID, Jurisdiction: jur, Name: in.GetName(),
			Rate: rateNum, EffectiveFrom: effective,
			RegistrationNo: in.GetRegistrationNo(), Provenance: prov,
			Authority: in.GetAuthority(), Notes: in.GetNotes(), CreatedBy: u.ID,
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "tax_rate", out.ID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"jurisdiction": jur, "name": in.GetName(),
				"rate": rate.String(6), "provenance": string(prov),
			})
	})
	if err != nil {
		return nil, s.fail("SaveTaxRate", err)
	}
	return connect.NewResponse(&stillhousev1.SaveTaxRateResponse{
		Rate: taxRateToProto(out),
	}), nil
}

func (s *InvoicingService) ListTaxRates(
	ctx context.Context,
	_ *connect.Request[stillhousev1.ListTaxRatesRequest],
) (*connect.Response[stillhousev1.ListTaxRatesResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.TaxRate
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListTaxRates(ctx)
		return e
	}); err != nil {
		return nil, s.fail("ListTaxRates", err)
	}
	out := make([]*stillhousev1.TaxRate, 0, len(rows))
	for _, r := range rows {
		out = append(out, taxRateToProto(r))
	}
	return connect.NewResponse(&stillhousev1.ListTaxRatesResponse{Rates: out}), nil
}

func (s *InvoicingService) DeleteTaxRate(
	ctx context.Context,
	req *connect.Request[stillhousev1.DeleteTaxRateRequest],
) (*connect.Response[stillhousev1.DeleteTaxRateResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := q.DeleteTaxRate(ctx, id); e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "tax_rate", id.String(),
			sqlcgen.AuditActionDelete, map[string]any{})
	}); err != nil {
		return nil, s.fail("DeleteTaxRate", err)
	}
	return connect.NewResponse(&stillhousev1.DeleteTaxRateResponse{}), nil
}

// ----- invoices -------------------------------------------------------------

func (s *InvoicingService) CreateInvoice(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreateInvoiceRequest],
) (*connect.Response[stillhousev1.CreateInvoiceResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	shipmentID, err := parseOptionalUUID(in.GetShipmentId(), "shipment_id")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	orderID, err := parseOptionalUUID(in.GetSalesOrderId(), "sales_order_id")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	var customerID uuid.UUID
	if v := strings.TrimSpace(in.GetCustomerId()); v != "" {
		if customerID, err = uuid.Parse(v); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid customer_id"))
		}
	} else if !shipmentID.Valid {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("name a customer, or a shipment to take one from"))
	}

	var out *stillhousev1.Invoice
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var lines []sqlcgen.ShipmentLinesForInvoicingRow
		if shipmentID.Valid {
			sh, e := q.GetShipment(ctx, shipmentID.UUID)
			if e != nil {
				if errors.Is(e, pgx.ErrNoRows) {
					return connect.NewError(connect.CodeNotFound, errors.New("shipment not found"))
				}
				return e
			}
			if sh.Status != sqlcgen.ShipmentStatusShipped {
				return connect.NewError(connect.CodeFailedPrecondition,
					errors.New("that shipment has not gone yet — invoice it once it ships"))
			}
			customerID = sh.CustomerID
			if !orderID.Valid {
				orderID = sh.SalesOrderID
			}
			lines, e = q.ShipmentLinesForInvoicing(ctx, shipmentID.UUID)
			if e != nil {
				return e
			}
		}
		cust, e := q.GetCustomer(ctx, customerID)
		if e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return connect.NewError(connect.CodeNotFound, errors.New("customer not found"))
			}
			return e
		}
		if e := q.LockDocumentSequence(ctx, "invoices"); e != nil {
			return e
		}
		nextNo, e := q.NextInvoiceNo(ctx, sqlcgen.InvoiceKindInvoice)
		if e != nil {
			return e
		}
		terms := int32(0)
		if cust.PaymentTermsDays.Valid && cust.PaymentTermsDays.Int32 > 0 {
			terms = cust.PaymentTermsDays.Int32
		}
		inv, e := q.CreateInvoice(ctx, sqlcgen.CreateInvoiceParams{
			TenantID: u.TenantID, Kind: sqlcgen.InvoiceKindInvoice, InvoiceNo: nextNo,
			CustomerID: customerID, SalesOrderID: orderID, ShipmentID: shipmentID,
			TermsDays: terms, Currency: "CAD",
			BillToName: cust.Name, BillToAddress: cust.Address,
			CustomerReference: in.GetCustomerReference(), Notes: in.GetNotes(),
			CreatedBy: u.ID,
		})
		if e != nil {
			return e
		}
		for i, l := range lines {
			price := money.FromNumeric(l.UnitPrice)
			desc := fmt.Sprintf("%s — %d mL, %.1f %% (lot %s)",
				l.ProductName, l.BottleSizeMl, l.TargetAbvPct, l.LotCode)
			if e := s.addLine(ctx, q, u.TenantID, inv.ID,
				uuid.NullUUID{UUID: l.ProductID, Valid: true}, desc,
				money.MustParse(fmt.Sprintf("%d", l.Bottles)), price, int32(i)); e != nil {
				return e
			}
		}
		if e := audit.Write(ctx, q, u.TenantID, u.ID, "invoice", inv.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"invoice_no": inv.InvoiceNo, "customer": cust.Name,
				"from_shipment": nullUUIDString(shipmentID),
			}); e != nil {
			return e
		}
		out, e = s.hydrate(ctx, q, inv.ID)
		return e
	})
	if err != nil {
		return nil, s.fail("CreateInvoice", err)
	}
	return connect.NewResponse(&stillhousev1.CreateInvoiceResponse{Invoice: out}), nil
}

// addLine writes one line, computing its total exactly. Tax is left off
// here and resolved at issue: a rate is a fact about the issue date, and
// a draft sitting for a fortnight across a rate change must not carry the
// old one.
func (s *InvoicingService) addLine(
	ctx context.Context, q *sqlcgen.Queries, tenantID, invoiceID uuid.UUID,
	productID uuid.NullUUID, description string,
	quantity, unitPrice money.Amount, sort int32,
) error {
	total := quantity.Mul(unitPrice).RoundTo(moneyScale)
	qn, err := quantity.Numeric(moneyScale)
	if err != nil {
		return err
	}
	pn, err := unitPrice.Numeric(moneyScale)
	if err != nil {
		return err
	}
	tn, err := total.Numeric(moneyScale)
	if err != nil {
		return err
	}
	zero, err := money.Zero().Numeric(moneyScale)
	if err != nil {
		return err
	}
	rateZero, err := money.Zero().Numeric(6)
	if err != nil {
		return err
	}
	_, err = q.AddInvoiceLine(ctx, sqlcgen.AddInvoiceLineParams{
		TenantID: tenantID, InvoiceID: invoiceID, ProductID: productID,
		Description: description, Quantity: qn, UnitPrice: pn, LineTotal: tn,
		TaxName: "", TaxRate: rateZero, TaxAmount: zero, SortOrder: sort,
	})
	return err
}

func (s *InvoicingService) AddInvoiceLine(
	ctx context.Context,
	req *connect.Request[stillhousev1.AddInvoiceLineRequest],
) (*connect.Response[stillhousev1.AddInvoiceLineResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	invID, err := uuid.Parse(in.GetInvoiceId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid invoice_id"))
	}
	if strings.TrimSpace(in.GetDescription()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say what the line is for — it is what the customer reads"))
	}
	qty, err := money.Parse(in.GetQuantity())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("quantity: %w", err))
	}
	if qty.IsZero() {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("a line of nothing is not a line"))
	}
	price, err := money.Parse(in.GetUnitPrice())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("unit price: %w", err))
	}
	productID, err := parseOptionalUUID(in.GetProductId(), "product_id")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var out *stillhousev1.Invoice
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		inv, e := q.GetInvoiceForUpdate(ctx, invID)
		if e != nil {
			return e
		}
		if e := assertInvoiceEditable(inv); e != nil {
			return e
		}
		existing, e := q.ListInvoiceLines(ctx, invID)
		if e != nil {
			return e
		}
		if e := s.addLine(ctx, q, u.TenantID, invID, productID,
			in.GetDescription(), qty, price, int32(len(existing))); e != nil {
			return e
		}
		out, e = s.hydrate(ctx, q, invID)
		return e
	})
	if err != nil {
		return nil, s.fail("AddInvoiceLine", err)
	}
	return connect.NewResponse(&stillhousev1.AddInvoiceLineResponse{Invoice: out}), nil
}

func (s *InvoicingService) RemoveInvoiceLine(
	ctx context.Context,
	req *connect.Request[stillhousev1.RemoveInvoiceLineRequest],
) (*connect.Response[stillhousev1.RemoveInvoiceLineResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	lineID, err := uuid.Parse(req.Msg.GetLineId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid line_id"))
	}
	var out *stillhousev1.Invoice
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		// The line's own row tells us the invoice; the invoice tells us
		// whether it may still be edited.
		row, e := q.GetInvoiceLineOwner(ctx, lineID)
		if e != nil {
			return e
		}
		inv, e := q.GetInvoiceForUpdate(ctx, row)
		if e != nil {
			return e
		}
		if e := assertInvoiceEditable(inv); e != nil {
			return e
		}
		if e := q.DeleteInvoiceLine(ctx, lineID); e != nil {
			return e
		}
		out, e = s.hydrate(ctx, q, inv.ID)
		return e
	})
	if err != nil {
		return nil, s.fail("RemoveInvoiceLine", err)
	}
	return connect.NewResponse(&stillhousev1.RemoveInvoiceLineResponse{Invoice: out}), nil
}

// IssueInvoice freezes the document and resolves its tax.
//
// Tax is applied here rather than at line entry because a rate is a fact
// about the issue date: a draft sitting for a fortnight across a rate
// change must carry the new rate, not the one in force when somebody
// started typing. Once issued the rate is frozen on the line, because a
// document already sent does not change.
func (s *InvoicingService) IssueInvoice(
	ctx context.Context,
	req *connect.Request[stillhousev1.IssueInvoiceRequest],
) (*connect.Response[stillhousev1.IssueInvoiceResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	issueDate, err := parseDateOrToday(req.Msg.GetIssueDate())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var out *stillhousev1.Invoice
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		inv, e := q.GetInvoiceForUpdate(ctx, id)
		if e != nil {
			return e
		}
		if inv.Status != sqlcgen.InvoiceStatusDraft {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("that invoice has already been issued"))
		}
		lines, e := q.ListInvoiceLines(ctx, id)
		if e != nil {
			return e
		}
		if len(lines) == 0 {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("an invoice with no lines asks for nothing"))
		}
		cust, e := q.GetCustomer(ctx, inv.CustomerID)
		if e != nil {
			return e
		}
		rates, e := q.TaxRatesInForce(ctx, sqlcgen.TaxRatesInForceParams{
			Jurisdiction: cust.Jurisdiction, OnDate: issueDate,
		})
		if e != nil {
			return e
		}
		// Rewrite each line with the tax that applies. Several taxes on
		// one line would need several columns; where a jurisdiction has
		// two (GST and PST), they are combined into one line entry named
		// for both, because what the customer owes is the sum and what
		// the invoice has to show is the breakdown of rates, which the
		// name carries.
		combinedRate := money.Zero()
		var names []string
		for _, r := range rates {
			combinedRate = combinedRate.Add(money.FromNumeric(r.Rate))
			names = append(names, r.Name)
		}
		taxName := strings.Join(names, " + ")

		if e := q.DeleteInvoiceLines(ctx, id); e != nil {
			return e
		}
		for i, l := range lines {
			qty := money.FromNumeric(l.Quantity)
			price := money.FromNumeric(l.UnitPrice)
			total := qty.Mul(price).RoundTo(moneyScale)
			tax := total.Mul(combinedRate).RoundTo(moneyScale)

			qn, _ := qty.Numeric(moneyScale)
			pn, _ := price.Numeric(moneyScale)
			tn, _ := total.Numeric(moneyScale)
			an, _ := tax.Numeric(moneyScale)
			rn, _ := combinedRate.Numeric(6)
			if _, ie := q.AddInvoiceLine(ctx, sqlcgen.AddInvoiceLineParams{
				TenantID: u.TenantID, InvoiceID: id, ProductID: l.ProductID,
				Description: l.Description, Quantity: qn, UnitPrice: pn,
				LineTotal: tn, TaxName: taxName, TaxRate: rn, TaxAmount: an,
				SortOrder: int32(i),
			}); ie != nil {
				return ie
			}
		}

		terms := inv.TermsDays
		if req.Msg.GetTermsDays() >= 0 {
			terms = req.Msg.GetTermsDays()
		}
		due := issueDate.Time.AddDate(0, 0, int(terms))
		issued, e := q.IssueInvoice(ctx, sqlcgen.IssueInvoiceParams{
			ID:        id,
			IssueDate: issueDate,
			DueDate:   pgtype.Date{Valid: true, Time: due},
			IssuedBy:  uuid.NullUUID{UUID: u.ID, Valid: true},
			// Frozen at issue: the name and address on a document are what
			// they were when it was sent.
			BillToName:    cust.Name,
			BillToAddress: cust.Address,
			TermsDays:     terms,
		})
		if e != nil {
			return e
		}
		if e := audit.Write(ctx, q, u.TenantID, u.ID, "invoice", id.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"invoice_no": issued.InvoiceNo,
				"issued_on":  issueDate.Time.Format("2006-01-02"),
				"due_on":     due.Format("2006-01-02"),
				"tax":        taxName,
				"tax_rate":   combinedRate.String(6),
			}); e != nil {
			return e
		}
		out, e = s.hydrate(ctx, q, id)
		return e
	})
	if err != nil {
		return nil, s.fail("IssueInvoice", err)
	}
	return connect.NewResponse(&stillhousev1.IssueInvoiceResponse{Invoice: out}), nil
}

func (s *InvoicingService) VoidInvoice(
	ctx context.Context,
	req *connect.Request[stillhousev1.VoidInvoiceRequest],
) (*connect.Response[stillhousev1.VoidInvoiceResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	reason := strings.TrimSpace(req.Msg.GetReason())
	if reason == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say why the invoice was voided"))
	}
	var out *stillhousev1.Invoice
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		inv, e := q.GetInvoiceForUpdate(ctx, id)
		if e != nil {
			return e
		}
		// Money already received against a document cannot be made not to
		// have arrived. Credit it instead, which leaves both facts on the
		// record.
		totals, e := q.InvoiceTotals(ctx, id)
		if e != nil {
			return e
		}
		if money.FromNumeric(totals.Paid).Sign() > 0 {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("payments have been recorded against this invoice — "+
					"raise a credit note rather than voiding it"))
		}
		if inv.Kind == sqlcgen.InvoiceKindCreditNote {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("a credit note is itself the correction; voiding one "+
					"leaves the invoice it credits uncorrected"))
		}
		voided, e := q.VoidInvoice(ctx, sqlcgen.VoidInvoiceParams{ID: id, VoidReason: reason})
		if errors.Is(e, pgx.ErrNoRows) {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("that invoice is already void"))
		}
		if e != nil {
			return e
		}
		if e := audit.Write(ctx, q, u.TenantID, u.ID, "invoice", id.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"invoice_no": voided.InvoiceNo, "voided": true, "reason": reason,
			}); e != nil {
			return e
		}
		out, e = s.hydrate(ctx, q, id)
		return e
	})
	if err != nil {
		return nil, s.fail("VoidInvoice", err)
	}
	return connect.NewResponse(&stillhousev1.VoidInvoiceResponse{Invoice: out}), nil
}

func (s *InvoicingService) CreateCreditNote(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreateCreditNoteRequest],
) (*connect.Response[stillhousev1.CreateCreditNoteResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	invID, err := uuid.Parse(req.Msg.GetInvoiceId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say which invoice is being credited"))
	}
	reason := strings.TrimSpace(req.Msg.GetReason())
	if reason == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say why — a credit note with no reason is an unexplained "+
				"reduction in revenue"))
	}

	var out *stillhousev1.Invoice
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		orig, e := q.GetInvoiceForUpdate(ctx, invID)
		if e != nil {
			return e
		}
		if orig.Kind != sqlcgen.InvoiceKindInvoice {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("a credit note credits an invoice, not another credit note"))
		}
		if orig.Status == sqlcgen.InvoiceStatusDraft {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("that invoice was never issued — edit or void it instead"))
		}
		if e := q.LockDocumentSequence(ctx, "credit_notes"); e != nil {
			return e
		}
		nextNo, e := q.NextInvoiceNo(ctx, sqlcgen.InvoiceKindCreditNote)
		if e != nil {
			return e
		}
		note, e := q.CreateInvoice(ctx, sqlcgen.CreateInvoiceParams{
			TenantID: u.TenantID, Kind: sqlcgen.InvoiceKindCreditNote,
			InvoiceNo: nextNo, CustomerID: orig.CustomerID,
			SalesOrderID: orig.SalesOrderID, ShipmentID: orig.ShipmentID,
			CreditsInvoiceID: uuid.NullUUID{UUID: invID, Valid: true},
			TermsDays:        0, Currency: orig.Currency,
			BillToName: orig.BillToName, BillToAddress: orig.BillToAddress,
			CustomerReference: orig.CustomerReference,
			Notes:             reason, CreatedBy: u.ID,
		})
		if e != nil {
			return e
		}

		if len(req.Msg.GetLines()) == 0 {
			// Credit the whole thing, line for line, at the rates the
			// original carried. Re-resolving today's rates would credit a
			// different amount than was charged.
			origLines, le := q.ListInvoiceLines(ctx, invID)
			if le != nil {
				return le
			}
			for i, l := range origLines {
				qn, _ := money.FromNumeric(l.Quantity).Numeric(moneyScale)
				if _, ie := q.AddInvoiceLine(ctx, sqlcgen.AddInvoiceLineParams{
					TenantID: u.TenantID, InvoiceID: note.ID, ProductID: l.ProductID,
					Description: l.Description, Quantity: qn, UnitPrice: l.UnitPrice,
					LineTotal: l.LineTotal, TaxName: l.TaxName, TaxRate: l.TaxRate,
					TaxAmount: l.TaxAmount, SortOrder: int32(i),
				}); ie != nil {
					return ie
				}
			}
		} else {
			// A partial credit is priced at the original's tax rate, for
			// the same reason.
			origLines, le := q.ListInvoiceLines(ctx, invID)
			if le != nil {
				return le
			}
			rate := money.Zero()
			taxName := ""
			if len(origLines) > 0 {
				rate = money.FromNumeric(origLines[0].TaxRate)
				taxName = origLines[0].TaxName
			}
			for i, l := range req.Msg.GetLines() {
				qty, pe := money.Parse(l.GetQuantity())
				if pe != nil {
					return connect.NewError(connect.CodeInvalidArgument, pe)
				}
				price, pe := money.Parse(l.GetUnitPrice())
				if pe != nil {
					return connect.NewError(connect.CodeInvalidArgument, pe)
				}
				if strings.TrimSpace(l.GetDescription()) == "" {
					return connect.NewError(connect.CodeInvalidArgument,
						errors.New("every credit line needs a description"))
				}
				productID, pe := parseOptionalUUID(l.GetProductId(), "product_id")
				if pe != nil {
					return connect.NewError(connect.CodeInvalidArgument, pe)
				}
				total := qty.Mul(price).RoundTo(moneyScale)
				tax := total.Mul(rate).RoundTo(moneyScale)
				qn, _ := qty.Numeric(moneyScale)
				pn, _ := price.Numeric(moneyScale)
				tn, _ := total.Numeric(moneyScale)
				an, _ := tax.Numeric(moneyScale)
				rn, _ := rate.Numeric(6)
				if _, ie := q.AddInvoiceLine(ctx, sqlcgen.AddInvoiceLineParams{
					TenantID: u.TenantID, InvoiceID: note.ID, ProductID: productID,
					Description: l.GetDescription(), Quantity: qn, UnitPrice: pn,
					LineTotal: tn, TaxName: taxName, TaxRate: rn, TaxAmount: an,
					SortOrder: int32(i),
				}); ie != nil {
					return ie
				}
			}
		}

		// A credit note is issued the moment it is raised: there is no
		// draft state worth having for a document whose only purpose is to
		// correct one already sent.
		today, _ := parseDateOrToday("")
		if _, ie := q.IssueInvoice(ctx, sqlcgen.IssueInvoiceParams{
			ID: note.ID, IssueDate: today, DueDate: today,
			IssuedBy:      uuid.NullUUID{UUID: u.ID, Valid: true},
			BillToName:    orig.BillToName,
			BillToAddress: orig.BillToAddress,
			TermsDays:     0,
		}); ie != nil {
			return ie
		}
		if e := audit.Write(ctx, q, u.TenantID, u.ID, "credit_note", note.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"credit_note_no": note.InvoiceNo,
				"credits":        orig.InvoiceNo,
				"reason":         reason,
			}); e != nil {
			return e
		}
		out, e = s.hydrate(ctx, q, note.ID)
		return e
	})
	if err != nil {
		return nil, s.fail("CreateCreditNote", err)
	}
	return connect.NewResponse(&stillhousev1.CreateCreditNoteResponse{CreditNote: out}), nil
}

func (s *InvoicingService) RecordPayment(
	ctx context.Context,
	req *connect.Request[stillhousev1.RecordPaymentRequest],
) (*connect.Response[stillhousev1.RecordPaymentResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	invID, err := uuid.Parse(in.GetInvoiceId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid invoice_id"))
	}
	amount, err := money.Parse(in.GetAmount())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if amount.Sign() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("a payment is a positive amount — to reverse one, raise a credit note"))
	}
	receivedOn, err := parseDateOrToday(in.GetReceivedOn())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var out *stillhousev1.Invoice
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		// Locked, so two payments recorded at once cannot both read the
		// same outstanding balance and both leave the invoice half-paid.
		inv, e := q.GetInvoiceForUpdate(ctx, invID)
		if e != nil {
			return e
		}
		switch inv.Status {
		case sqlcgen.InvoiceStatusIssued, sqlcgen.InvoiceStatusPartPaid,
			sqlcgen.InvoiceStatusPaid:
		default:
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("that invoice has not been issued, or is void"))
		}
		amt, ae := amount.Numeric(moneyScale)
		if ae != nil {
			return ae
		}
		if _, ie := q.RecordInvoicePayment(ctx, sqlcgen.RecordInvoicePaymentParams{
			TenantID: u.TenantID, InvoiceID: invID, ReceivedOn: receivedOn,
			Amount: amt, Method: in.GetMethod(), Reference: in.GetReference(),
			Notes: in.GetNotes(), RecordedBy: u.ID,
		}); ie != nil {
			return ie
		}
		totals, te := q.InvoiceTotals(ctx, invID)
		if te != nil {
			return te
		}
		total := money.FromNumeric(totals.Subtotal).Add(money.FromNumeric(totals.Tax))
		paid := money.FromNumeric(totals.Paid)
		status := sqlcgen.InvoiceStatusPartPaid
		if paid.Cmp(total) >= 0 {
			status = sqlcgen.InvoiceStatusPaid
		}
		if _, se := q.SetInvoicePaymentStatus(ctx, sqlcgen.SetInvoicePaymentStatusParams{
			ID: invID, Status: status,
		}); se != nil {
			return se
		}
		if e := audit.Write(ctx, q, u.TenantID, u.ID, "invoice_payment", invID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"invoice_no":  inv.InvoiceNo,
				"amount":      amount.String(2),
				"received_on": receivedOn.Time.Format("2006-01-02"),
				"reference":   in.GetReference(),
				"now":         string(status),
			}); e != nil {
			return e
		}
		out, e = s.hydrate(ctx, q, invID)
		return e
	})
	if err != nil {
		return nil, s.fail("RecordPayment", err)
	}
	return connect.NewResponse(&stillhousev1.RecordPaymentResponse{Invoice: out}), nil
}

func (s *InvoicingService) ListInvoices(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListInvoicesRequest],
) (*connect.Response[stillhousev1.ListInvoicesResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.ListInvoicesRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListInvoices(ctx, req.Msg.GetOpenOnly())
		return e
	}); err != nil {
		return nil, s.fail("ListInvoices", err)
	}
	out := make([]*stillhousev1.Invoice, 0, len(rows))
	for _, r := range rows {
		inv := invoiceHeaderToProto(sqlcgen.Invoice{
			ID: r.ID, Kind: r.Kind, InvoiceNo: r.InvoiceNo, CustomerID: r.CustomerID,
			SalesOrderID: r.SalesOrderID, ShipmentID: r.ShipmentID,
			CreditsInvoiceID: r.CreditsInvoiceID, Status: r.Status,
			IssueDate: r.IssueDate, DueDate: r.DueDate, TermsDays: r.TermsDays,
			Currency: r.Currency, BillToName: r.BillToName,
			BillToAddress: r.BillToAddress, CustomerReference: r.CustomerReference,
			Notes: r.Notes, VoidReason: r.VoidReason, IssuedAt: r.IssuedAt,
			CreatedAt: r.CreatedAt,
		}, r.CustomerName)
		total := money.FromNumeric(r.Total)
		paid := money.FromNumeric(r.Paid)
		inv.Total = total.String(2)
		inv.Paid = paid.String(2)
		inv.Outstanding = total.Sub(paid).String(2)
		out = append(out, inv)
	}
	return connect.NewResponse(&stillhousev1.ListInvoicesResponse{Invoices: out}), nil
}

func (s *InvoicingService) GetInvoice(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetInvoiceRequest],
) (*connect.Response[stillhousev1.GetInvoiceResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	out := &stillhousev1.GetInvoiceResponse{}
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		inv, e := s.hydrate(ctx, q, id)
		if e != nil {
			return e
		}
		out.Invoice = inv
		pays, e := q.ListInvoicePayments(ctx, id)
		if e != nil {
			return e
		}
		for _, p := range pays {
			out.Payments = append(out.Payments, &stillhousev1.InvoicePayment{
				Id: p.ID.String(), ReceivedOn: formatDate(p.ReceivedOn),
				Amount: money.FromNumeric(p.Amount).String(2),
				Method: p.Method, Reference: p.Reference, Notes: p.Notes,
			})
		}
		return nil
	})
	if err != nil {
		return nil, s.fail("GetInvoice", err)
	}
	return connect.NewResponse(out), nil
}

func (s *InvoicingService) AgeingReport(
	ctx context.Context,
	_ *connect.Request[stillhousev1.AgeingReportRequest],
) (*connect.Response[stillhousev1.AgeingReportResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.ARAgeingRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ARAgeing(ctx)
		return e
	}); err != nil {
		return nil, s.fail("AgeingReport", err)
	}
	out := &stillhousev1.AgeingReportResponse{
		Basis: "Buckets run from the due date, not the issue date: an invoice on " +
			"60-day terms issued 45 days ago is not overdue, and a report that says " +
			"it is trains people to ignore the report. Credit notes carry through " +
			"as negatives, so an outstanding credit reads as owing less.",
	}
	total := money.Zero()
	for _, r := range rows {
		t := money.FromNumeric(r.Total)
		total = total.Add(t)
		out.Lines = append(out.Lines, &stillhousev1.AgeingLine{
			CustomerId: r.CustomerID.String(), CustomerName: r.CustomerName,
			Total:    t.String(2),
			Invoices: r.Invoices,
			Buckets: []*stillhousev1.AgeingBucket{
				{Label: "Not yet due", Amount: money.FromNumeric(r.Current).String(2)},
				{Label: "1–30 days", Amount: money.FromNumeric(r.D130).String(2), Overdue: true},
				{Label: "31–60 days", Amount: money.FromNumeric(r.D3160).String(2), Overdue: true},
				{Label: "61–90 days", Amount: money.FromNumeric(r.D6190).String(2), Overdue: true},
				{Label: "Over 90 days", Amount: money.FromNumeric(r.D90Plus).String(2), Overdue: true},
			},
		})
	}
	out.Total = total.String(2)
	return connect.NewResponse(out), nil
}

// --- helpers ---

func assertInvoiceEditable(inv sqlcgen.Invoice) error {
	if inv.Status == sqlcgen.InvoiceStatusDraft {
		return nil
	}
	return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
		"invoice %d has been issued — a document already sent does not change. "+
			"Raise a credit note instead.", inv.InvoiceNo))
}

func (s *InvoicingService) hydrate(
	ctx context.Context, q *sqlcgen.Queries, id uuid.UUID,
) (*stillhousev1.Invoice, error) {
	row, err := q.GetInvoice(ctx, id)
	if err != nil {
		return nil, err
	}
	out := invoiceHeaderToProto(sqlcgen.Invoice{
		ID: row.ID, Kind: row.Kind, InvoiceNo: row.InvoiceNo,
		CustomerID: row.CustomerID, SalesOrderID: row.SalesOrderID,
		ShipmentID: row.ShipmentID, CreditsInvoiceID: row.CreditsInvoiceID,
		Status: row.Status, IssueDate: row.IssueDate, DueDate: row.DueDate,
		TermsDays: row.TermsDays, Currency: row.Currency,
		BillToName: row.BillToName, BillToAddress: row.BillToAddress,
		CustomerReference: row.CustomerReference, Notes: row.Notes,
		VoidReason: row.VoidReason, IssuedAt: row.IssuedAt, CreatedAt: row.CreatedAt,
	}, row.CustomerName)

	lines, err := q.ListInvoiceLines(ctx, id)
	if err != nil {
		return nil, err
	}
	subtotal, tax := money.Zero(), money.Zero()
	for _, l := range lines {
		lt := money.FromNumeric(l.LineTotal)
		ta := money.FromNumeric(l.TaxAmount)
		subtotal = subtotal.Add(lt)
		tax = tax.Add(ta)
		out.Lines = append(out.Lines, &stillhousev1.InvoiceLine{
			Id: l.ID.String(), ProductId: nullUUIDString(l.ProductID),
			Description: l.Description,
			Quantity:    money.FromNumeric(l.Quantity).String(moneyScale),
			UnitPrice:   money.FromNumeric(l.UnitPrice).String(moneyScale),
			LineTotal:   lt.String(2),
			TaxName:     l.TaxName,
			TaxRate:     money.FromNumeric(l.TaxRate).String(6),
			TaxAmount:   ta.String(2),
		})
	}
	totals, err := q.InvoiceTotals(ctx, id)
	if err != nil {
		return nil, err
	}
	paid := money.FromNumeric(totals.Paid)
	total := subtotal.Add(tax)
	out.Subtotal = subtotal.String(2)
	out.Tax = tax.String(2)
	out.Total = total.String(2)
	out.Paid = paid.String(2)
	out.Outstanding = total.Sub(paid).String(2)

	if row.DueDate.Valid &&
		(row.Status == sqlcgen.InvoiceStatusIssued || row.Status == sqlcgen.InvoiceStatusPartPaid) {
		if late := int32(time.Since(row.DueDate.Time).Hours() / 24); late > 0 {
			out.DaysOverdue = late
		}
	}
	// An invoice quietly short by its tax is worse than one that says so.
	if row.Status != sqlcgen.InvoiceStatusDraft && tax.IsZero() && !subtotal.IsZero() {
		out.Warnings = append(out.Warnings,
			"No tax was applied. If that is wrong, it is because no tax rate is "+
				"recorded for this customer's jurisdiction on the issue date.")
	}
	for _, l := range lines {
		if money.FromNumeric(l.UnitPrice).IsZero() {
			out.Warnings = append(out.Warnings, fmt.Sprintf(
				"%q is priced at zero — the pick it came from had no order line "+
					"behind it, so there was no agreed price.", l.Description))
		}
	}
	return out, nil
}

func invoiceHeaderToProto(i sqlcgen.Invoice, customerName string) *stillhousev1.Invoice {
	out := &stillhousev1.Invoice{
		Id: i.ID.String(), Kind: invoiceKindToProto(i.Kind), InvoiceNo: i.InvoiceNo,
		CustomerId: i.CustomerID.String(), CustomerName: customerName,
		SalesOrderId:     nullUUIDString(i.SalesOrderID),
		ShipmentId:       nullUUIDString(i.ShipmentID),
		CreditsInvoiceId: nullUUIDString(i.CreditsInvoiceID),
		Status:           invoiceStatusToProto(i.Status),
		IssueDate:        formatDate(i.IssueDate), DueDate: formatDate(i.DueDate),
		TermsDays: i.TermsDays, Currency: i.Currency,
		BillToName: i.BillToName, BillToAddress: i.BillToAddress,
		CustomerReference: i.CustomerReference, Notes: i.Notes,
		VoidReason: i.VoidReason,
	}
	if i.IssuedAt.Valid {
		out.IssuedAt = timestamppb.New(i.IssuedAt.Time)
	}
	if i.CreatedAt.Valid {
		out.CreatedAt = timestamppb.New(i.CreatedAt.Time)
	}
	return out
}

func taxRateToProto(r sqlcgen.TaxRate) *stillhousev1.TaxRate {
	return &stillhousev1.TaxRate{
		Id: r.ID.String(), Jurisdiction: r.Jurisdiction, Name: r.Name,
		Rate:           money.FromNumeric(r.Rate).String(6),
		EffectiveFrom:  formatDate(r.EffectiveFrom),
		RegistrationNo: r.RegistrationNo,
		Provenance:     requirementProvenanceToProto(r.Provenance),
		Authority:      r.Authority, Notes: r.Notes,
	}
}

func invoiceKindToProto(k sqlcgen.InvoiceKind) stillhousev1.InvoiceKind {
	if k == sqlcgen.InvoiceKindCreditNote {
		return stillhousev1.InvoiceKind_INVOICE_KIND_CREDIT_NOTE
	}
	return stillhousev1.InvoiceKind_INVOICE_KIND_INVOICE
}

func invoiceStatusToProto(s sqlcgen.InvoiceStatus) stillhousev1.InvoiceStatus {
	switch s {
	case sqlcgen.InvoiceStatusDraft:
		return stillhousev1.InvoiceStatus_INVOICE_STATUS_DRAFT
	case sqlcgen.InvoiceStatusIssued:
		return stillhousev1.InvoiceStatus_INVOICE_STATUS_ISSUED
	case sqlcgen.InvoiceStatusPartPaid:
		return stillhousev1.InvoiceStatus_INVOICE_STATUS_PART_PAID
	case sqlcgen.InvoiceStatusPaid:
		return stillhousev1.InvoiceStatus_INVOICE_STATUS_PAID
	case sqlcgen.InvoiceStatusVoid:
		return stillhousev1.InvoiceStatus_INVOICE_STATUS_VOID
	default:
		return stillhousev1.InvoiceStatus_INVOICE_STATUS_UNSPECIFIED
	}
}
