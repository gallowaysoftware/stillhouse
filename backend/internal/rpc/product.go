package rpc

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type ProductService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewProductService(db *tenantdb.DB, logger *slog.Logger) *ProductService {
	return &ProductService{db: db, logger: logger}
}

func (s *ProductService) CreateProduct(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreateProductRequest],
) (*connect.Response[stillhousev1.CreateProductResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	if in.GetName() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}
	if in.GetBottleSizeMl() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bottle_size_ml must be > 0"))
	}
	if err := validateFinite("target_abv_pct", in.GetTargetAbvPct()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if in.GetTargetAbvPct() <= 0 || in.GetTargetAbvPct() > 100 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("target_abv_pct must be in (0, 100]"))
	}
	kind, err := spiritKindToDB(in.GetSpiritKind())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	var p sqlcgen.Product
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		p, e = q.CreateProduct(ctx, sqlcgen.CreateProductParams{
			TenantID:     u.TenantID,
			Name:         in.GetName(),
			SpiritKind:   kind,
			BottleSizeMl: in.GetBottleSizeMl(),
			TargetAbvPct: in.GetTargetAbvPct(),
			LabelNotes:   in.GetLabelNotes(),
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "product", p.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"name":           p.Name,
				"spirit_kind":    string(p.SpiritKind),
				"bottle_size_ml": p.BottleSizeMl,
				"target_abv_pct": p.TargetAbvPct,
			})
	})
	if err != nil {
		s.logger.Error("CreateProduct", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.CreateProductResponse{Product: productToProto(p)}), nil
}

func (s *ProductService) UpdateProduct(
	ctx context.Context,
	req *connect.Request[stillhousev1.UpdateProductRequest],
) (*connect.Response[stillhousev1.UpdateProductResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	kind, err := spiritKindToDB(req.Msg.GetSpiritKind())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	// The same guards CreateProduct applies. Editing was unvalidated, and
	// the dangerous value here is IN range: a 40% product saved as 0.40
	// crosses the 7% ABV excise threshold, so removals stop being charged
	// per litre of absolute alcohol and start being charged per litre of
	// product — about a sixteenfold understatement of duty on a 750 mL
	// bottle, landing on the B266 under the wrong line, with nothing
	// anywhere looking wrong.
	if req.Msg.GetName() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}
	if req.Msg.GetBottleSizeMl() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bottle_size_ml must be > 0"))
	}
	if err := validateFinite("target_abv_pct", req.Msg.GetTargetAbvPct()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if req.Msg.GetTargetAbvPct() <= 0 || req.Msg.GetTargetAbvPct() > 100 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("target_abv_pct must be in (0, 100]"))
	}
	var p sqlcgen.Product
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		p, e = q.UpdateProduct(ctx, sqlcgen.UpdateProductParams{
			ID:           id,
			Name:         req.Msg.GetName(),
			SpiritKind:   kind,
			BottleSizeMl: req.Msg.GetBottleSizeMl(),
			TargetAbvPct: req.Msg.GetTargetAbvPct(),
			LabelNotes:   req.Msg.GetLabelNotes(),
		})
		return e
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("product not found"))
		}
		s.logger.Error("UpdateProduct", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.UpdateProductResponse{Product: productToProto(p)}), nil
}

func (s *ProductService) ListProducts(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListProductsRequest],
) (*connect.Response[stillhousev1.ListProductsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.Product
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListProducts(ctx, req.Msg.GetIncludeArchived())
		return e
	})
	if err != nil {
		s.logger.Error("ListProducts", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.Product, 0, len(rows))
	for _, p := range rows {
		out = append(out, productToProto(p))
	}
	return connect.NewResponse(&stillhousev1.ListProductsResponse{Products: out}), nil
}

func (s *ProductService) GetProduct(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetProductRequest],
) (*connect.Response[stillhousev1.GetProductResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	var p sqlcgen.Product
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		p, e = q.GetProduct(ctx, id)
		return e
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("product not found"))
		}
		s.logger.Error("GetProduct", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.GetProductResponse{Product: productToProto(p)}), nil
}

func (s *ProductService) SetProductArchived(
	ctx context.Context,
	req *connect.Request[stillhousev1.SetProductArchivedRequest],
) (*connect.Response[stillhousev1.SetProductArchivedResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	var p sqlcgen.Product
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		p, e = q.SetProductArchived(ctx, sqlcgen.SetProductArchivedParams{ID: id, Archived: req.Msg.GetArchived()})
		return e
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("product not found"))
		}
		s.logger.Error("SetProductArchived", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SetProductArchivedResponse{Product: productToProto(p)}), nil
}

func productToProto(p sqlcgen.Product) *stillhousev1.Product {
	return &stillhousev1.Product{
		Id:           p.ID.String(),
		TenantId:     p.TenantID.String(),
		Name:         p.Name,
		SpiritKind:   spiritKindToProto(p.SpiritKind),
		BottleSizeMl: p.BottleSizeMl,
		TargetAbvPct: p.TargetAbvPct,
		LabelNotes:   p.LabelNotes,
		Archived:     p.Archived,
		CreatedAt:    timestamppb.New(p.CreatedAt.Time),
		UpdatedAt:    timestamppb.New(p.UpdatedAt.Time),
	}
}
