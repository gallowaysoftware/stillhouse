package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type POSService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewPOSService(db *tenantdb.DB, logger *slog.Logger) *POSService {
	return &POSService{db: db, logger: logger}
}

// IngestPOSSales accepts a batch of till lines. PLAN G4.
//
// Idempotent on (source, external_id), which is the point rather than a
// nicety: every POS worth integrating with delivers at least once, and a
// retry that created a second removal would report duty twice and take
// stock off the shelf that is still on it. Under-reporting is a penalty;
// over-reporting is a penalty AND a stock figure nobody can reconcile.
//
// Posting is off by default. An operator connecting a till for the first
// time should see what arrived before it becomes removals on a return.
func (s *POSService) IngestPOSSales(
	ctx context.Context,
	req *connect.Request[stillhousev1.IngestPOSSalesRequest],
) (*connect.Response[stillhousev1.IngestPOSSalesResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	source := strings.TrimSpace(req.Msg.GetSource())
	if source == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say which system these came from — the SKU mapping and the duplicate check are both per source"))
	}
	if len(req.Msg.GetLines()) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("no lines"))
	}

	out := &stillhousev1.IngestPOSSalesResponse{}
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var fresh []sqlcgen.PosSale
		for _, l := range req.Msg.GetLines() {
			if strings.TrimSpace(l.GetExternalId()) == "" {
				return connect.NewError(connect.CodeInvalidArgument, errors.New(
					"every line needs the POS's own id — it is what makes redelivery harmless"))
			}
			if l.GetQuantity() <= 0 {
				return connect.NewError(connect.CodeInvalidArgument,
					fmt.Errorf("line %s has quantity %d", l.GetExternalId(), l.GetQuantity()))
			}
			soldAt, e := time.Parse(time.RFC3339, l.GetSoldAt())
			if e != nil {
				return connect.NewError(connect.CodeInvalidArgument,
					fmt.Errorf("line %s: sold_at must be RFC3339", l.GetExternalId()))
			}
			var price pgtype.Numeric
			if l.GetUnitPriceSet() {
				_ = price.Scan(strconv.FormatFloat(l.GetUnitPriceCad(), 'f', 2, 64))
			}

			row, e := q.InsertPOSSale(ctx, sqlcgen.InsertPOSSaleParams{
				TenantID: u.TenantID, Source: source,
				ExternalID: l.GetExternalId(), ExternalSku: l.GetExternalSku(),
				Description: l.GetDescription(), Quantity: l.GetQuantity(),
				UnitPriceCad: price,
				SoldAt:       pgtype.Timestamptz{Valid: true, Time: soldAt},
			})
			if e != nil {
				if errors.Is(e, pgx.ErrNoRows) {
					// The ON CONFLICT DO NOTHING path: already seen.
					// Normal, and counted rather than reported as an error.
					out.Duplicates++
					continue
				}
				return e
			}
			out.Received++
			fresh = append(fresh, row)
		}

		if !req.Msg.GetPostImmediately() {
			return nil
		}
		posted, rejected, msgs, e := postPOSSales(ctx, q, u, fresh)
		if e != nil {
			return e
		}
		out.Posted, out.Rejected = posted, rejected
		out.Rejections = msgs
		return nil
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		s.logger.Error("IngestPOSSales", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(out), nil
}

// PostPOSSales turns pending sales into duty-paid removals.
func (s *POSService) PostPOSSales(
	ctx context.Context,
	req *connect.Request[stillhousev1.PostPOSSalesRequest],
) (*connect.Response[stillhousev1.PostPOSSalesResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	wanted := map[string]bool{}
	for _, id := range req.Msg.GetSaleIds() {
		wanted[id] = true
	}

	out := &stillhousev1.PostPOSSalesResponse{}
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		pending, e := q.ListPendingPOSSales(ctx)
		if e != nil {
			return e
		}
		var todo []sqlcgen.PosSale
		for _, p := range pending {
			if len(wanted) == 0 || wanted[p.ID.String()] {
				todo = append(todo, p)
			}
		}
		posted, rejected, msgs, e := postPOSSales(ctx, q, u, todo)
		if e != nil {
			return e
		}
		out.Posted, out.Rejected, out.Rejections = posted, rejected, msgs
		return nil
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		s.logger.Error("PostPOSSales", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(out), nil
}

// postPOSSales is the shared body. Each sale is posted or rejected on its
// own merits: one unmapped SKU must not stop the rest of the day's takings
// reaching the return, and a sale that cannot be posted is kept with its
// reason rather than dropped — a sale that vanishes because nobody had
// mapped its SKU is the under-reporting this whole feature exists to
// prevent.
func postPOSSales(
	ctx context.Context, q *sqlcgen.Queries, u sqlcgen.User, sales []sqlcgen.PosSale,
) (posted, rejected int32, rejections []string, err error) {
	if len(sales) == 0 {
		return 0, 0, nil, nil
	}
	tenant, err := q.GetTenantByID(ctx, u.TenantID)
	if err != nil {
		return 0, 0, nil, err
	}

	for _, sale := range sales {
		reject := func(why string) error {
			rejected++
			rejections = append(rejections, sale.ExternalID+": "+why)
			return q.MarkPOSSaleRejected(ctx, sqlcgen.MarkPOSSaleRejectedParams{
				ID: sale.ID, RejectReason: why,
			})
		}

		productID, e := q.LookupPOSProduct(ctx, sqlcgen.LookupPOSProductParams{
			Source: sale.Source, ExternalSku: sale.ExternalSku,
		})
		if e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				// Not guessed. Posting against the wrong product means
				// wrong duty and wrong stock, on a filed return.
				if e := reject("SKU " + sale.ExternalSku + " is not mapped to a product. Map it under Till → SKUs, then post again."); e != nil {
					return posted, rejected, rejections, e
				}
				continue
			}
			return posted, rejected, rejections, e
		}

		lot, e := q.OldestPackagedLotForProduct(ctx, sqlcgen.OldestPackagedLotForProductParams{
			ProductID: productID, RequireRelease: tenant.RequireBatchRelease,
		})
		if e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				why := "no packaged stock on hand for that product"
				if tenant.RequireBatchRelease {
					why += " that has been released"
				}
				if e := reject(why); e != nil {
					return posted, rejected, rejections, e
				}
				continue
			}
			return posted, rejected, rejections, e
		}
		// Deliberately no stock check here. Stock is enforced atomically
		// at the write inside recordRemoval — the update that takes the
		// bottles off is conditional on their still being there — which
		// is the only place it can be enforced without a race. A check
		// here would be a second copy of the rule, a step away from the
		// first and readable a moment before it stops being true, which
		// is the drift stage 173 pulled recordRemoval out to prevent. A
		// refusal from there arrives below as this sale's rejection.

		// The same path a hand-keyed removal takes. Stage 173 pulled this
		// out so the two could not drift; this is the third caller.
		outcome, e := recordRemoval(ctx, q, u.TenantID, u.ID, removalInput{
			PackagedInventoryID: lot.ID,
			Bottles:             sale.Quantity,
			RemovalDate:         pgtype.Date{Valid: true, Time: sale.SoldAt.Time},
			DestinationKind:     sqlcgen.RemovalDestinationKindDutyPaidCustomer,
			DestinationName:     posDestinationName(sale),
			Reference:           sale.Source + ":" + sale.ExternalID,
		})
		if e != nil {
			var ce *connect.Error
			if errors.As(e, &ce) {
				// A refusal that belongs to this sale — a locked period,
				// say — is that sale's rejection, not the batch's.
				if e := reject(ce.Message()); e != nil {
					return posted, rejected, rejections, e
				}
				continue
			}
			return posted, rejected, rejections, e
		}

		if e := q.MarkPOSSalePosted(ctx, sqlcgen.MarkPOSSalePostedParams{
			ID: sale.ID, RemovalID: uuid.NullUUID{UUID: outcome.Removal.ID, Valid: true},
		}); e != nil {
			return posted, rejected, rejections, e
		}
		posted++
	}

	if err := audit.Write(ctx, q, u.TenantID, u.ID, "pos_batch", uuid.NewString(),
		sqlcgen.AuditActionCreate, map[string]any{
			"posted": posted, "rejected": rejected,
		}); err != nil {
		return posted, rejected, rejections, err
	}
	return posted, rejected, rejections, nil
}

// posDestinationName is what appears on the removal. Named for the till
// rather than left blank: a removal whose destination is empty is one an
// auditor asks about.
func posDestinationName(s sqlcgen.PosSale) string {
	if s.Description != "" {
		return s.Source + " — " + s.Description
	}
	return s.Source
}

func (s *POSService) ListPOSSales(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListPOSSalesRequest],
) (*connect.Response[stillhousev1.ListPOSSalesResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	limit := req.Msg.GetLimit()
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	out := &stillhousev1.ListPOSSalesResponse{}
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		rows, e := q.ListPOSSales(ctx, sqlcgen.ListPOSSalesParams{
			StatusFilter: req.Msg.GetStatus(), RowLimit: limit,
		})
		if e != nil {
			return e
		}
		for _, r := range rows {
			sale := &stillhousev1.POSSale{
				Id: r.ID.String(), Source: r.Source, ExternalId: r.ExternalID,
				ExternalSku: r.ExternalSku, ProductName: r.ProductName,
				Description: r.Description, Quantity: r.Quantity,
				SoldAt: r.SoldAt.Time.Format(time.RFC3339), Status: string(r.Status),
				RejectReason: r.RejectReason,
			}
			if r.UnitPriceCad.Valid {
				sale.UnitPriceCad = numericToFloat(r.UnitPriceCad)
				sale.UnitPriceSet = true
			}
			if r.RemovalID.Valid {
				sale.RemovalId = r.RemovalID.UUID.String()
			}
			out.Sales = append(out.Sales, sale)
		}
		sum, e := q.POSSaleSummary(ctx)
		if e != nil {
			return e
		}
		out.Pending, out.Posted, out.Rejected, out.Ignored = sum.Pending, sum.Posted, sum.Rejected, sum.Ignored
		return nil
	}); err != nil {
		s.logger.Error("ListPOSSales", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(out), nil
}

// IgnorePOSSale marks a sale as deliberately not posted — a test sale, a
// comp, a correction. Recorded as a decision with a reason rather than
// deleted, so the gap between what the till took and what reached the
// return is always explained by something.
func (s *POSService) IgnorePOSSale(
	ctx context.Context,
	req *connect.Request[stillhousev1.IgnorePOSSaleRequest],
) (*connect.Response[stillhousev1.IgnorePOSSaleResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	if strings.TrimSpace(req.Msg.GetReason()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(
			"ignoring a sale needs a reason — it is the only thing explaining why the till and the return disagree"))
	}
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := q.SetPOSSaleIgnored(ctx, sqlcgen.SetPOSSaleIgnoredParams{
			ID: id, RejectReason: req.Msg.GetReason(),
		}); e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "pos_sale", id.String(),
			sqlcgen.AuditActionUpdate, map[string]any{"ignored": req.Msg.GetReason()})
	}); err != nil {
		s.logger.Error("IgnorePOSSale", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.IgnorePOSSaleResponse{}), nil
}

func (s *POSService) ListPOSProductMappings(
	ctx context.Context,
	_ *connect.Request[stillhousev1.ListPOSProductMappingsRequest],
) (*connect.Response[stillhousev1.ListPOSProductMappingsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	out := &stillhousev1.ListPOSProductMappingsResponse{}
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		rows, e := q.ListPOSProductMap(ctx)
		if e != nil {
			return e
		}
		for _, r := range rows {
			out.Mappings = append(out.Mappings, &stillhousev1.POSProductMapping{
				Id: r.ID.String(), Source: r.Source, ExternalSku: r.ExternalSku,
				ProductId: r.ProductID.String(), ProductName: r.ProductName,
			})
		}
		return nil
	}); err != nil {
		s.logger.Error("ListPOSProductMappings", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(out), nil
}

func (s *POSService) SavePOSProductMapping(
	ctx context.Context,
	req *connect.Request[stillhousev1.SavePOSProductMappingRequest],
) (*connect.Response[stillhousev1.SavePOSProductMappingResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	productID, err := uuid.Parse(req.Msg.GetProductId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid product_id"))
	}
	source := strings.TrimSpace(req.Msg.GetSource())
	sku := strings.TrimSpace(req.Msg.GetExternalSku())
	if source == "" || sku == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("source and external_sku are required"))
	}

	var row sqlcgen.PosProductMap
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		r, e := q.UpsertPOSProductMap(ctx, sqlcgen.UpsertPOSProductMapParams{
			TenantID: u.TenantID, Source: source, ExternalSku: sku, ProductID: productID,
		})
		if e != nil {
			return e
		}
		row = r
		return audit.Write(ctx, q, u.TenantID, u.ID, "pos_product_map", r.ID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{"source": source, "sku": sku})
	}); err != nil {
		s.logger.Error("SavePOSProductMapping", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SavePOSProductMappingResponse{
		Mapping: &stillhousev1.POSProductMapping{
			Id: row.ID.String(), Source: row.Source, ExternalSku: row.ExternalSku,
			ProductId: row.ProductID.String(),
		},
	}), nil
}

func (s *POSService) DeletePOSProductMapping(
	ctx context.Context,
	req *connect.Request[stillhousev1.DeletePOSProductMappingRequest],
) (*connect.Response[stillhousev1.DeletePOSProductMappingResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		return q.DeletePOSProductMap(ctx, id)
	}); err != nil {
		s.logger.Error("DeletePOSProductMapping", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.DeletePOSProductMappingResponse{}), nil
}
