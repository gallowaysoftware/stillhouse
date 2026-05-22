package rpc

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/excise"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/pricing"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type PricingService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewPricingService(db *tenantdb.DB, logger *slog.Logger) *PricingService {
	return &PricingService{db: db, logger: logger}
}

func (s *PricingService) ComputeProvincialPricing(
	ctx context.Context,
	req *connect.Request[stillhousev1.ComputeProvincialPricingRequest],
) (*connect.Response[stillhousev1.ComputeProvincialPricingResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	productID, err := uuid.Parse(req.Msg.GetProductId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid product_id"))
	}
	if req.Msg.GetFobCad() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("fob_cad must be > 0"))
	}

	var product sqlcgen.Product
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		product, e = q.GetProduct(ctx, productID)
		return e
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("product not found"))
		}
		s.logger.Error("ComputeProvincialPricing", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	breakdowns := pricing.ComputeBreakdown(
		req.Msg.GetFobCad(),
		product.BottleSizeMl,
		product.TargetAbvPct,
		excise.DutyRatePerLAAOver7Pct,
	)
	out := &stillhousev1.ComputeProvincialPricingResponse{
		ProductName:  product.Name,
		BottleSizeMl: product.BottleSizeMl,
		BottleAbvPct: product.TargetAbvPct,
		Breakdowns:   make([]*stillhousev1.ProvincialPricingBreakdown, 0, len(breakdowns)),
	}
	for _, b := range breakdowns {
		out.Breakdowns = append(out.Breakdowns, &stillhousev1.ProvincialPricingBreakdown{
			Jurisdiction:        b.Jurisdiction,
			Name:                b.Name,
			FobCad:              b.FOBCAD,
			MarkupCad:           b.MarkupCAD,
			PerLitreCad:         b.PerLitreCAD,
			BasicTaxCad:         b.BasicTaxCAD,
			FederalExciseCad:    b.FederalExciseCAD,
			ContainerDepositCad: b.ContainerDepositCAD,
			ShelfBeforeSalesTax: b.ShelfBeforeSalesTax,
			OnSiteRetailNetCad:  b.OnSiteRetailNetCAD,
			Notes:               b.Notes,
		})
	}
	return connect.NewResponse(out), nil
}
