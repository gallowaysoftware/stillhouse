package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/importer"
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

		Gtin:                 p.Gtin,
		CspcCode:             p.CspcCode,
		BottlesPerCase:       p.BottlesPerCase.Int32,
		CasesPerLayer:        p.CasesPerLayer.Int32,
		LayersPerPallet:      p.LayersPerPallet.Int32,
		CaseGrossWeightKg:    p.CaseGrossWeightKg.Float64,
		CommonName:           p.CommonName,
		AgeStatement:         p.AgeStatement,
		ContainerMarking:     p.ContainerMarking,
		AllergenStatement:    p.AllergenStatement,
		CountryOfOrigin:      p.CountryOfOrigin,
		MarketingDescription: p.MarketingDescription,
	}
}

// UpdateProductSKU sets the trade and label fields.
//
// Separate from UpdateProduct because they are different decisions made
// by different people: bottle size and strength change what is in the
// bottle, while a GTIN or a case configuration changes how it is sold.
//
// Note what this does NOT do. It does not derive common_name from
// spirit_kind, or age_statement from the maturation clock. Whether a
// spirit qualifies for a standardised common name under Division 2 of
// the Food and Drug Regulations, and what a blend may claim about its
// age, are the licensee's declarations — resting on how it was made and
// how long it sat. Stillhouse asserting either on their behalf would be
// putting words in their mouth on a label.
func (s *ProductService) UpdateProductSKU(
	ctx context.Context,
	req *connect.Request[stillhousev1.UpdateProductSKURequest],
) (*connect.Response[stillhousev1.UpdateProductSKUResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	id, err := uuid.Parse(in.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	gtin := strings.TrimSpace(in.GetGtin())
	if gtin != "" {
		if err := importer.ValidateGTIN(gtin); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
	}
	for _, f := range []struct {
		name string
		v    int32
	}{
		{"bottles_per_case", in.GetBottlesPerCase()},
		{"cases_per_layer", in.GetCasesPerLayer()},
		{"layers_per_pallet", in.GetLayersPerPallet()},
	} {
		if f.v < 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("%s cannot be negative", f.name))
		}
	}
	if in.GetCaseGrossWeightKg() < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("case_gross_weight_kg cannot be negative"))
	}

	var row sqlcgen.Product
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		row, e = q.UpdateProductSKU(ctx, sqlcgen.UpdateProductSKUParams{
			ID:                   id,
			Gtin:                 gtin,
			CspcCode:             strings.TrimSpace(in.GetCspcCode()),
			BottlesPerCase:       nullInt32(in.GetBottlesPerCase()),
			CasesPerLayer:        nullInt32(in.GetCasesPerLayer()),
			LayersPerPallet:      nullInt32(in.GetLayersPerPallet()),
			CaseGrossWeightKg:    nullFloat64(in.GetCaseGrossWeightKg()),
			CommonName:           strings.TrimSpace(in.GetCommonName()),
			AgeStatement:         strings.TrimSpace(in.GetAgeStatement()),
			ContainerMarking:     in.GetContainerMarking(),
			AllergenStatement:    in.GetAllergenStatement(),
			CountryOfOrigin:      strings.TrimSpace(in.GetCountryOfOrigin()),
			MarketingDescription: in.GetMarketingDescription(),
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "product", row.ID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"event": "sku_details", "gtin": row.Gtin, "cspc_code": row.CspcCode,
				"common_name": row.CommonName,
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("product not found"))
		}
		if ce := classifyWriteErr(err, "the product no longer exists"); ce != nil {
			return nil, ce
		}
		s.logger.Error("UpdateProductSKU", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.UpdateProductSKUResponse{
		Product: productToProto(row),
	}), nil
}

// nullInt32 treats zero as "not recorded". A case of zero bottles is not
// a thing, so no information is lost by the conflation.
func nullInt32(v int32) pgtype.Int4 {
	if v <= 0 {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: v, Valid: true}
}

func nullFloat64(v float64) pgtype.Float8 {
	if v <= 0 {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: v, Valid: true}
}
