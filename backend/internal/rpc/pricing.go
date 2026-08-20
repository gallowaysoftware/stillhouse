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

	in := pricing.Input{
		FOBCAD:               req.Msg.GetFobCad(),
		BottleSizeML:         product.BottleSizeMl,
		BottleABVPct:         product.TargetAbvPct,
		FederalDutyPerLAA:    excise.DutyRatePerLAAOver7Pct,
		FreightCAD:           req.Msg.GetFreightCad(),
		ImportDutiesCAD:      req.Msg.GetImportDutiesCad(),
		Imported:             req.Msg.GetImported(),
		OnSiteRetailPriceCAD: req.Msg.GetOnSiteRetailPriceCad(),
	}
	rows := pricing.Compute(in)

	out := &stillhousev1.ComputeProvincialPricingResponse{
		ProductName:       product.Name,
		BottleSizeMl:      product.BottleSizeMl,
		BottleAbvPct:      product.TargetAbvPct,
		FederalDutyPerLaa: excise.DutyRatePerLAAOver7Pct,
		LaaPerBottle:      round4(in.LAA()),
		Jurisdictions:     make([]*stillhousev1.JurisdictionPricing, 0, len(rows)),
	}
	for _, r := range rows {
		out.Jurisdictions = append(out.Jurisdictions, &stillhousev1.JurisdictionPricing{
			Code:         r.Code,
			Name:         r.Name,
			Notes:        r.Notes,
			Wholesale:    channelToProto(r.Wholesale),
			OnSiteRetail: channelToProto(r.OnSiteRetail),
			Export:       channelToProto(r.Export),
		})
	}
	return connect.NewResponse(out), nil
}

// channelToProto carries the refusal across the wire as faithfully as the
// figures. A channel that couldn't be computed arrives with computable
// false and its missing rates named, so the UI can say why rather than
// rendering zeros.
func channelToProto(c pricing.ChannelResult) *stillhousev1.ChannelPricing {
	out := &stillhousev1.ChannelPricing{
		Channel:          salesChannelToProto(c.Channel),
		Computable:       c.Computable,
		LowestProvenance: provenanceToProto(c.LowestProvenance),
	}
	for _, m := range c.Missing {
		out.Missing = append(out.Missing, &stillhousev1.MissingRate{What: m.What, Why: m.Why})
	}
	if !c.Computable {
		return out
	}
	out.LandedCostCad = round2cents(c.LandedCostCAD)
	out.MarkupCad = round2cents(c.MarkupCAD)
	out.CosdCad = round2cents(c.COSDCAD)
	out.ProvincialTaxCad = round2cents(c.ProvincialTaxCAD)
	out.FederalExciseCad = round2cents(c.FederalExciseCAD)
	out.ContainerDepositCad = round2cents(c.ContainerDepositCAD)
	out.SalesTaxCad = round2cents(c.SalesTaxCAD)
	out.PriceToBuyerCad = round2cents(c.PriceToBuyerCAD)
	out.DistilleryNetCad = round2cents(c.DistilleryNetCAD)
	return out
}

func salesChannelToProto(c pricing.Channel) stillhousev1.SalesChannel {
	switch c {
	case pricing.ChannelOnSiteRetail:
		return stillhousev1.SalesChannel_SALES_CHANNEL_ON_SITE_RETAIL
	case pricing.ChannelExport:
		return stillhousev1.SalesChannel_SALES_CHANNEL_EXPORT
	default:
		return stillhousev1.SalesChannel_SALES_CHANNEL_WHOLESALE
	}
}

func provenanceToProto(p pricing.Provenance) stillhousev1.RateProvenance {
	switch p {
	case pricing.Sourced:
		return stillhousev1.RateProvenance_RATE_PROVENANCE_SOURCED
	case pricing.Indicative:
		return stillhousev1.RateProvenance_RATE_PROVENANCE_INDICATIVE
	default:
		return stillhousev1.RateProvenance_RATE_PROVENANCE_UNKNOWN
	}
}
