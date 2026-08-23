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
	"github.com/gallowaysoftware/stillhouse/backend/internal/pricing"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

// CustomerService owns customers and price lists.
//
// The compliance point of a customer record, as against the free-text
// destination it replaces: a buyer's *kind* decides whether a removal to
// them is duty-paid, non-duty-paid or an export, and that is a property
// of the buyer, not of the movement. Recorded on the customer, it is
// decided once. Retyped on every removal, it is decided every time, next
// to a free-text name that never had to agree with it.
type CustomerService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewCustomerService(db *tenantdb.DB, logger *slog.Logger) *CustomerService {
	return &CustomerService{db: db, logger: logger}
}

func (s *CustomerService) CreateCustomer(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreateCustomerRequest],
) (*connect.Response[stillhousev1.CreateCustomerResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	params, err := customerParams(u.TenantID, in.GetName(), in.GetKind(), in.GetJurisdiction(),
		in.GetDefaultDestinationKind(), in.GetLicenceNumber(), in.GetAccountReference(),
		in.GetContactName(), in.GetEmail(), in.GetPhone(), in.GetAddress(),
		in.GetPaymentTermsDays(), in.GetNotes(), in.GetPriceListId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var row sqlcgen.Customer
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var err error
		row, err = q.CreateCustomer(ctx, params)
		if err != nil {
			return err
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "customer", row.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"name": row.Name, "kind": string(row.Kind),
				"default_destination_kind": row.DefaultDestinationKind,
			})
	})
	if err != nil {
		if ce := classifyWriteErr(err, "the price list no longer exists"); ce != nil {
			return nil, ce
		}
		s.logger.Error("CreateCustomer", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.CreateCustomerResponse{
		Customer: customerToProto(row, ""),
	}), nil
}

func (s *CustomerService) UpdateCustomer(
	ctx context.Context,
	req *connect.Request[stillhousev1.UpdateCustomerRequest],
) (*connect.Response[stillhousev1.UpdateCustomerResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	id, err := uuid.Parse(in.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	base, err := customerParams(u.TenantID, in.GetName(), in.GetKind(), in.GetJurisdiction(),
		in.GetDefaultDestinationKind(), in.GetLicenceNumber(), in.GetAccountReference(),
		in.GetContactName(), in.GetEmail(), in.GetPhone(), in.GetAddress(),
		in.GetPaymentTermsDays(), in.GetNotes(), in.GetPriceListId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var row sqlcgen.Customer
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var err error
		row, err = q.UpdateCustomer(ctx, sqlcgen.UpdateCustomerParams{
			ID:                     id,
			Name:                   base.Name,
			Kind:                   base.Kind,
			Jurisdiction:           base.Jurisdiction,
			DefaultDestinationKind: base.DefaultDestinationKind,
			LicenceNumber:          base.LicenceNumber,
			AccountReference:       base.AccountReference,
			ContactName:            base.ContactName,
			Email:                  base.Email,
			Phone:                  base.Phone,
			Address:                base.Address,
			PaymentTermsDays:       base.PaymentTermsDays,
			Notes:                  base.Notes,
			PriceListID:            base.PriceListID,
		})
		if err != nil {
			return err
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "customer", row.ID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"name": row.Name, "kind": string(row.Kind),
				"default_destination_kind": row.DefaultDestinationKind,
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("customer not found"))
		}
		if ce := classifyWriteErr(err, "the price list no longer exists"); ce != nil {
			return nil, ce
		}
		s.logger.Error("UpdateCustomer", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.UpdateCustomerResponse{
		Customer: customerToProto(row, ""),
	}), nil
}

func (s *CustomerService) ListCustomers(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListCustomersRequest],
) (*connect.Response[stillhousev1.ListCustomersResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	kindFilter := ""
	if k := req.Msg.GetKind(); k != stillhousev1.CustomerKind_CUSTOMER_KIND_UNSPECIFIED {
		dbKind, err := customerKindToDB(k)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		kindFilter = string(dbKind)
	}

	var rows []sqlcgen.ListCustomersRow
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var err error
		rows, err = q.ListCustomers(ctx, sqlcgen.ListCustomersParams{
			IncludeArchived: req.Msg.GetIncludeArchived(),
			Kind:            kindFilter,
		})
		return err
	})
	if err != nil {
		s.logger.Error("ListCustomers", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.Customer, 0, len(rows))
	for _, r := range rows {
		out = append(out, customerToProto(sqlcgen.Customer{
			ID: r.ID, TenantID: r.TenantID, Name: r.Name, Kind: r.Kind,
			Jurisdiction: r.Jurisdiction, DefaultDestinationKind: r.DefaultDestinationKind,
			LicenceNumber: r.LicenceNumber, AccountReference: r.AccountReference,
			ContactName: r.ContactName, Email: r.Email, Phone: r.Phone,
			Address: r.Address, PaymentTermsDays: r.PaymentTermsDays, Notes: r.Notes,
			ArchivedAt: r.ArchivedAt, CreatedAt: r.CreatedAt, PriceListID: r.PriceListID,
		}, r.PriceListName))
	}
	return connect.NewResponse(&stillhousev1.ListCustomersResponse{Customers: out}), nil
}

func (s *CustomerService) GetCustomer(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetCustomerRequest],
) (*connect.Response[stillhousev1.GetCustomerResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}

	var (
		row     sqlcgen.Customer
		totals  sqlcgen.CustomerRemovalTotalsRow
		listNam string
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var err error
		row, err = q.GetCustomer(ctx, id)
		if err != nil {
			return err
		}
		if row.PriceListID.Valid {
			if pl, err := q.GetPriceList(ctx, row.PriceListID.UUID); err == nil {
				listNam = pl.Name
			}
		}
		totals, err = q.CustomerRemovalTotals(ctx, uuid.NullUUID{UUID: id, Valid: true})
		return err
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("customer not found"))
		}
		s.logger.Error("GetCustomer", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.GetCustomerResponse{
		Customer:       customerToProto(row, listNam),
		RemovalCount:   totals.RemovalCount,
		BottlesRemoved: totals.BottlesRemoved,
		TotalLaa:       totals.TotalLaa,
		DutyChargedCad: totals.DutyChargedCad,
	}), nil
}

// SetCustomerArchived hides a customer without deleting it. Deleting is
// not offered: a removal points at one, and the trail behind a filed
// return has to stay resolvable years later.
func (s *CustomerService) SetCustomerArchived(
	ctx context.Context,
	req *connect.Request[stillhousev1.SetCustomerArchivedRequest],
) (*connect.Response[stillhousev1.SetCustomerArchivedResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	var row sqlcgen.Customer
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var err error
		row, err = q.SetCustomerArchived(ctx, sqlcgen.SetCustomerArchivedParams{
			ID: id, Archived: req.Msg.GetArchived(),
		})
		if err != nil {
			return err
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "customer", row.ID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"name": row.Name, "archived": req.Msg.GetArchived(),
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("customer not found"))
		}
		s.logger.Error("SetCustomerArchived", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SetCustomerArchivedResponse{
		Customer: customerToProto(row, ""),
	}), nil
}

// --- price lists -----------------------------------------------------------

func (s *CustomerService) CreatePriceList(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreatePriceListRequest],
) (*connect.Response[stillhousev1.CreatePriceListResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	name := strings.TrimSpace(in.GetName())
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}
	from, err := parseDateOrToday(in.GetEffectiveFrom())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("effective_from must be YYYY-MM-DD"))
	}
	var to pgtype.Date
	if v := in.GetEffectiveTo(); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				errors.New("effective_to must be YYYY-MM-DD"))
		}
		to = pgtype.Date{Valid: true, Time: t}
	}
	currency := strings.ToUpper(strings.TrimSpace(in.GetCurrency()))
	if currency == "" {
		currency = "CAD"
	}
	if jur := in.GetJurisdiction(); jur != "" && !pricing.Known(jur) {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("unknown jurisdiction %q", jur))
	}

	var row sqlcgen.PriceList
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var err error
		row, err = q.CreatePriceList(ctx, sqlcgen.CreatePriceListParams{
			TenantID:      u.TenantID,
			Name:          name,
			Channel:       priceListChannelToDB(in.GetChannel()),
			Jurisdiction:  in.GetJurisdiction(),
			Currency:      currency,
			EffectiveFrom: from,
			EffectiveTo:   to,
			Notes:         in.GetNotes(),
		})
		return err
	})
	if err != nil {
		if ce := classifyWriteErr(err, "the tenant no longer exists"); ce != nil {
			return nil, ce
		}
		s.logger.Error("CreatePriceList", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.CreatePriceListResponse{
		PriceList: priceListToProto(row, nil),
	}), nil
}

func (s *CustomerService) ListPriceLists(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListPriceListsRequest],
) (*connect.Response[stillhousev1.ListPriceListsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var asOf pgtype.Date
	if v := req.Msg.GetAsOf(); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				errors.New("as_of must be YYYY-MM-DD"))
		}
		asOf = pgtype.Date{Valid: true, Time: t}
	}
	var rows []sqlcgen.PriceList
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var err error
		rows, err = q.ListPriceLists(ctx, asOf)
		return err
	})
	if err != nil {
		s.logger.Error("ListPriceLists", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.PriceList, 0, len(rows))
	for _, r := range rows {
		out = append(out, priceListToProto(r, nil))
	}
	return connect.NewResponse(&stillhousev1.ListPriceListsResponse{PriceLists: out}), nil
}

func (s *CustomerService) GetPriceList(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetPriceListRequest],
) (*connect.Response[stillhousev1.GetPriceListResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	list, entries, err := s.readPriceList(ctx, u.TenantID, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("price list not found"))
		}
		s.logger.Error("GetPriceList", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.GetPriceListResponse{
		PriceList: priceListToProto(list, entries),
	}), nil
}

// SetPriceListEntry upserts one product's price on a list. An empty
// unit_price removes the entry — "no price for this product on this
// list" is a real state and is not the same as zero.
func (s *CustomerService) SetPriceListEntry(
	ctx context.Context,
	req *connect.Request[stillhousev1.SetPriceListEntryRequest],
) (*connect.Response[stillhousev1.SetPriceListEntryResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	listID, err := uuid.Parse(in.GetPriceListId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid price_list_id"))
	}
	productID, err := uuid.Parse(in.GetProductId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid product_id"))
	}
	if in.GetCaseSize() < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("case_size cannot be negative"))
	}

	remove := strings.TrimSpace(in.GetUnitPrice()) == ""
	var price pgtype.Numeric
	if !remove {
		if err := price.Scan(strings.TrimSpace(in.GetUnitPrice())); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				errors.New("unit_price must be a decimal amount, e.g. 34.95"))
		}
		if price.Int != nil && price.Int.Sign() < 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				errors.New("unit_price cannot be negative"))
		}
	}

	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if remove {
			return q.DeletePriceListEntry(ctx, sqlcgen.DeletePriceListEntryParams{
				PriceListID: listID, ProductID: productID,
			})
		}
		var caseSize pgtype.Int4
		if in.GetCaseSize() > 0 {
			caseSize = pgtype.Int4{Int32: in.GetCaseSize(), Valid: true}
		}
		_, err := q.UpsertPriceListEntry(ctx, sqlcgen.UpsertPriceListEntryParams{
			TenantID: u.TenantID, PriceListID: listID, ProductID: productID,
			UnitPrice: price, CaseSize: caseSize,
		})
		return err
	})
	if err != nil {
		if ce := classifyWriteErr(err, "that price list or product no longer exists"); ce != nil {
			return nil, ce
		}
		s.logger.Error("SetPriceListEntry", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	list, entries, err := s.readPriceList(ctx, u.TenantID, listID)
	if err != nil {
		s.logger.Error("SetPriceListEntry: reread", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SetPriceListEntryResponse{
		PriceList: priceListToProto(list, entries),
	}), nil
}

func (s *CustomerService) readPriceList(ctx context.Context, tenantID, id uuid.UUID) (
	sqlcgen.PriceList, []sqlcgen.ListPriceListEntriesRow, error,
) {
	var (
		list    sqlcgen.PriceList
		entries []sqlcgen.ListPriceListEntriesRow
	)
	err := s.db.WithTenantTx(ctx, tenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var err error
		list, err = q.GetPriceList(ctx, id)
		if err != nil {
			return err
		}
		entries, err = q.ListPriceListEntries(ctx, id)
		return err
	})
	return list, entries, err
}

// --- conversions -----------------------------------------------------------

func customerParams(
	tenantID uuid.UUID, name string, kind stillhousev1.CustomerKind, jurisdiction string,
	dest stillhousev1.RemovalDestinationKind, licence, account, contact, email, phone, address string,
	terms int32, notes, priceListID string,
) (sqlcgen.CreateCustomerParams, error) {
	var zero sqlcgen.CreateCustomerParams
	name = strings.TrimSpace(name)
	if name == "" {
		return zero, errors.New("name is required")
	}
	dbKind, err := customerKindToDB(kind)
	if err != nil {
		return zero, err
	}
	if jurisdiction != "" && !pricing.Known(jurisdiction) {
		return zero, fmt.Errorf("unknown jurisdiction %q", jurisdiction)
	}
	// An unspecified destination kind takes the one implied by the
	// buyer's nature, so the common path needs no second decision.
	if dest == stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_UNSPECIFIED {
		dest = defaultDestinationForKind(kind)
	}
	dbDest, err := removalDestinationKindToDB(dest)
	if err != nil {
		return zero, err
	}
	var termsCol pgtype.Int4
	if terms >= 0 {
		termsCol = pgtype.Int4{Int32: terms, Valid: true}
	}
	var listID uuid.NullUUID
	if priceListID != "" {
		id, err := uuid.Parse(priceListID)
		if err != nil {
			return zero, errors.New("invalid price_list_id")
		}
		listID = uuid.NullUUID{UUID: id, Valid: true}
	}
	return sqlcgen.CreateCustomerParams{
		TenantID:               tenantID,
		Name:                   name,
		Kind:                   dbKind,
		Jurisdiction:           jurisdiction,
		DefaultDestinationKind: string(dbDest),
		LicenceNumber:          strings.TrimSpace(licence),
		AccountReference:       strings.TrimSpace(account),
		ContactName:            strings.TrimSpace(contact),
		Email:                  strings.TrimSpace(email),
		Phone:                  strings.TrimSpace(phone),
		Address:                address,
		PaymentTermsDays:       termsCol,
		Notes:                  notes,
		PriceListID:            listID,
	}, nil
}

// defaultDestinationForKind is the excise consequence of who the buyer
// is. A provincial board, a licensee and a private store are all the
// duty-paid market; another excise licensee is a movement in bond;
// export is export. Getting this from the buyer rather than from the
// operator's memory is the whole point of the customer record.
func defaultDestinationForKind(k stillhousev1.CustomerKind) stillhousev1.RemovalDestinationKind {
	switch k {
	case stillhousev1.CustomerKind_CUSTOMER_KIND_SPIRITS_LICENSEE:
		return stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_TRANSFER_OUT_IN_BOND
	case stillhousev1.CustomerKind_CUSTOMER_KIND_EXPORT:
		return stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_EXPORT
	case stillhousev1.CustomerKind_CUSTOMER_KIND_OTHER,
		stillhousev1.CustomerKind_CUSTOMER_KIND_UNSPECIFIED:
		return stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_OTHER
	default:
		return stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_DUTY_PAID_CUSTOMER
	}
}

func customerKindToDB(k stillhousev1.CustomerKind) (sqlcgen.CustomerKind, error) {
	switch k {
	case stillhousev1.CustomerKind_CUSTOMER_KIND_PROVINCIAL_BOARD:
		return sqlcgen.CustomerKindProvincialBoard, nil
	case stillhousev1.CustomerKind_CUSTOMER_KIND_LICENSEE:
		return sqlcgen.CustomerKindLicensee, nil
	case stillhousev1.CustomerKind_CUSTOMER_KIND_PRIVATE_RETAIL:
		return sqlcgen.CustomerKindPrivateRetail, nil
	case stillhousev1.CustomerKind_CUSTOMER_KIND_SPIRITS_LICENSEE:
		return sqlcgen.CustomerKindSpiritsLicensee, nil
	case stillhousev1.CustomerKind_CUSTOMER_KIND_EXPORT:
		return sqlcgen.CustomerKindExport, nil
	case stillhousev1.CustomerKind_CUSTOMER_KIND_ON_SITE_RETAIL:
		return sqlcgen.CustomerKindOnSiteRetail, nil
	case stillhousev1.CustomerKind_CUSTOMER_KIND_OTHER:
		return sqlcgen.CustomerKindOther, nil
	}
	return "", errors.New("kind is required")
}

func customerKindToProto(k sqlcgen.CustomerKind) stillhousev1.CustomerKind {
	switch k {
	case sqlcgen.CustomerKindProvincialBoard:
		return stillhousev1.CustomerKind_CUSTOMER_KIND_PROVINCIAL_BOARD
	case sqlcgen.CustomerKindLicensee:
		return stillhousev1.CustomerKind_CUSTOMER_KIND_LICENSEE
	case sqlcgen.CustomerKindPrivateRetail:
		return stillhousev1.CustomerKind_CUSTOMER_KIND_PRIVATE_RETAIL
	case sqlcgen.CustomerKindSpiritsLicensee:
		return stillhousev1.CustomerKind_CUSTOMER_KIND_SPIRITS_LICENSEE
	case sqlcgen.CustomerKindExport:
		return stillhousev1.CustomerKind_CUSTOMER_KIND_EXPORT
	case sqlcgen.CustomerKindOnSiteRetail:
		return stillhousev1.CustomerKind_CUSTOMER_KIND_ON_SITE_RETAIL
	case sqlcgen.CustomerKindOther:
		return stillhousev1.CustomerKind_CUSTOMER_KIND_OTHER
	}
	return stillhousev1.CustomerKind_CUSTOMER_KIND_UNSPECIFIED
}

func priceListChannelToDB(c stillhousev1.SalesChannel) string {
	switch c {
	case stillhousev1.SalesChannel_SALES_CHANNEL_ON_SITE_RETAIL:
		return "on_site_retail"
	case stillhousev1.SalesChannel_SALES_CHANNEL_EXPORT:
		return "export"
	default:
		return "wholesale"
	}
}

// priceListChannelToProto maps the price list's stored channel string.
// Named apart from salesChannelToProto in pricing.go, which converts
// from the pricing package's own Channel type.
func priceListChannelToProto(c string) stillhousev1.SalesChannel {
	switch c {
	case "on_site_retail":
		return stillhousev1.SalesChannel_SALES_CHANNEL_ON_SITE_RETAIL
	case "export":
		return stillhousev1.SalesChannel_SALES_CHANNEL_EXPORT
	default:
		return stillhousev1.SalesChannel_SALES_CHANNEL_WHOLESALE
	}
}

func customerToProto(c sqlcgen.Customer, priceListName string) *stillhousev1.Customer {
	out := &stillhousev1.Customer{
		Id:                     c.ID.String(),
		Name:                   c.Name,
		Kind:                   customerKindToProto(c.Kind),
		Jurisdiction:           c.Jurisdiction,
		DefaultDestinationKind: removalDestinationKindToProto(sqlcgen.RemovalDestinationKind(c.DefaultDestinationKind)),
		LicenceNumber:          c.LicenceNumber,
		AccountReference:       c.AccountReference,
		ContactName:            c.ContactName,
		Email:                  c.Email,
		Phone:                  c.Phone,
		Address:                c.Address,
		// -1 rather than 0: "no terms recorded" and "due on receipt" are
		// different statements and a zero would collapse them.
		PaymentTermsDays: -1,
		Notes:            c.Notes,
		PriceListName:    priceListName,
	}
	if c.PaymentTermsDays.Valid {
		out.PaymentTermsDays = c.PaymentTermsDays.Int32
	}
	if c.PriceListID.Valid {
		out.PriceListId = c.PriceListID.UUID.String()
	}
	if c.ArchivedAt.Valid {
		out.ArchivedAt = timestamppb.New(c.ArchivedAt.Time)
	}
	if c.CreatedAt.Valid {
		out.CreatedAt = timestamppb.New(c.CreatedAt.Time)
	}
	return out
}

func priceListToProto(p sqlcgen.PriceList, entries []sqlcgen.ListPriceListEntriesRow) *stillhousev1.PriceList {
	out := &stillhousev1.PriceList{
		Id:           p.ID.String(),
		Name:         p.Name,
		Channel:      priceListChannelToProto(p.Channel),
		Jurisdiction: p.Jurisdiction,
		Currency:     p.Currency,
		Notes:        p.Notes,
		Entries:      make([]*stillhousev1.PriceListEntry, 0, len(entries)),
	}
	if p.EffectiveFrom.Valid {
		out.EffectiveFrom = p.EffectiveFrom.Time.Format("2006-01-02")
	}
	if p.EffectiveTo.Valid {
		out.EffectiveTo = p.EffectiveTo.Time.Format("2006-01-02")
	}
	if p.CreatedAt.Valid {
		out.CreatedAt = timestamppb.New(p.CreatedAt.Time)
	}
	for _, e := range entries {
		entry := &stillhousev1.PriceListEntry{
			Id:           e.ID.String(),
			ProductId:    e.ProductID.String(),
			ProductName:  e.ProductName,
			BottleSizeMl: e.BottleSizeMl,
			UnitPrice:    numericToDecimalString(e.UnitPrice),
		}
		if e.CaseSize.Valid {
			entry.CaseSize = e.CaseSize.Int32
		}
		out.Entries = append(out.Entries, entry)
	}
	return out
}

// numericToDecimalString renders a NUMERIC as the decimal string it is.
// Money crosses the wire as text on purpose: rendering 34.95 as a double
// and back is how a cent goes missing, and these are amounts somebody
// invoices. Cf. H10, which is the same argument about LAA.
func numericToDecimalString(n pgtype.Numeric) string {
	if !n.Valid || n.Int == nil {
		return ""
	}
	if n.NaN {
		return ""
	}
	digits := n.Int.String()
	neg := strings.HasPrefix(digits, "-")
	digits = strings.TrimPrefix(digits, "-")

	exp := int(n.Exp)
	switch {
	case exp >= 0:
		digits += strings.Repeat("0", exp)
	default:
		scale := -exp
		if len(digits) <= scale {
			digits = strings.Repeat("0", scale-len(digits)+1) + digits
		}
		digits = digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
		digits = strings.TrimRight(digits, "0")
		digits = strings.TrimSuffix(digits, ".")
	}
	if digits == "" {
		digits = "0"
	}
	if neg {
		digits = "-" + digits
	}
	return digits
}
