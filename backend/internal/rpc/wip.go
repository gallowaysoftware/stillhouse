package rpc

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	"github.com/gallowaysoftware/stillhouse/backend/internal/costing"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// WIPProduction values the spirit gauged into bulk over a period. PLAN E7.
//
// The licensee's charge basis is read here rather than passed in: it is a
// stated policy, not a report parameter, and letting a caller supply it
// would mean two runs of the same period could disagree with each other
// and both look authoritative.
func (s *CostingService) WIPProduction(
	ctx context.Context,
	req *connect.Request[stillhousev1.WIPProductionRequest],
) (*connect.Response[stillhousev1.WIPProductionResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	start, err := parseDate(req.Msg.GetPeriodStart(), "period_start")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	end, err := parseDate(req.Msg.GetPeriodEnd(), "period_end")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if end.Before(start) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("period_end is before period_start"))
	}

	var out costing.WIPProduction
	var basis sqlcgen.NullWipChargeBasis
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		b, e := q.GetWIPChargeBasis(ctx, u.TenantID)
		if e != nil {
			return e
		}
		basis = b
		var stated string
		if b.Valid {
			stated = string(b.WipChargeBasis)
		}
		// The query's window is half-open on the far end, so a gauge taken
		// on the last day of the period is inside it.
		out, e = costing.ProductionGaugeWIP(ctx, q, stated, start, end.AddDate(0, 0, 1))
		return e
	}); err != nil {
		return nil, s.fail("WIPProduction", err)
	}

	resp := &stillhousev1.WIPProductionResponse{
		TotalCad:    out.TotalCAD,
		ValuedCount: out.ValuedCount,
		TotalLaa:    out.TotalLAA,
		ValuedLaa:   out.ValuedLAA,
		Complete:    out.Complete,
		Refused:     out.Refused,
		Basis:       wipBasisToProto(basis),
	}
	for _, g := range out.Gauges {
		resp.Gauges = append(resp.Gauges, &stillhousev1.WIPGauge{
			Id:            g.ID,
			GaugeDate:     g.GaugeDate.Format("2006-01-02"),
			Laa:           g.LAA,
			ContainerName: g.ContainerName,
			AmountCad:     g.Value.AmountCAD,
			Available:     g.Value.Available,
			Missing:       g.Value.Missing,
			Basis:         g.Value.Basis,
			ChargeCount:   g.ChargeCount,
		})
	}
	return connect.NewResponse(resp), nil
}

func (s *CostingService) GetWIPChargeBasis(
	ctx context.Context,
	_ *connect.Request[stillhousev1.GetWIPChargeBasisRequest],
) (*connect.Response[stillhousev1.GetWIPChargeBasisResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var b sqlcgen.NullWipChargeBasis
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		b, e = q.GetWIPChargeBasis(ctx, u.TenantID)
		return e
	}); err != nil {
		return nil, s.fail("GetWIPChargeBasis", err)
	}
	return connect.NewResponse(&stillhousev1.GetWIPChargeBasisResponse{Basis: wipBasisToProto(b)}), nil
}

// SetWIPChargeBasis records the convention. UNSPECIFIED clears it back to
// unstated, which is a legitimate thing to want: a licensee who set one by
// mistake should be able to go back to being refused rather than being
// stuck with a policy they did not mean to adopt.
func (s *CostingService) SetWIPChargeBasis(
	ctx context.Context,
	req *connect.Request[stillhousev1.SetWIPChargeBasisRequest],
) (*connect.Response[stillhousev1.SetWIPChargeBasisResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var b sqlcgen.NullWipChargeBasis
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		b, e = q.SetWIPChargeBasis(ctx, sqlcgen.SetWIPChargeBasisParams{
			ID:             u.TenantID,
			WipChargeBasis: wipBasisFromProto(req.Msg.GetBasis()),
		})
		return e
	}); err != nil {
		return nil, s.fail("SetWIPChargeBasis", err)
	}
	return connect.NewResponse(&stillhousev1.SetWIPChargeBasisResponse{Basis: wipBasisToProto(b)}), nil
}

func wipBasisToProto(b sqlcgen.NullWipChargeBasis) stillhousev1.WIPChargeBasis {
	if !b.Valid {
		return stillhousev1.WIPChargeBasis_WIP_CHARGE_BASIS_UNSPECIFIED
	}
	switch b.WipChargeBasis {
	case sqlcgen.WipChargeBasisChargedVolume:
		return stillhousev1.WIPChargeBasis_WIP_CHARGE_BASIS_CHARGED_VOLUME
	case sqlcgen.WipChargeBasisChargedLaa:
		return stillhousev1.WIPChargeBasis_WIP_CHARGE_BASIS_CHARGED_LAA
	}
	return stillhousev1.WIPChargeBasis_WIP_CHARGE_BASIS_UNSPECIFIED
}

func wipBasisFromProto(b stillhousev1.WIPChargeBasis) sqlcgen.NullWipChargeBasis {
	switch b {
	case stillhousev1.WIPChargeBasis_WIP_CHARGE_BASIS_CHARGED_VOLUME:
		return sqlcgen.NullWipChargeBasis{Valid: true, WipChargeBasis: sqlcgen.WipChargeBasisChargedVolume}
	case stillhousev1.WIPChargeBasis_WIP_CHARGE_BASIS_CHARGED_LAA:
		return sqlcgen.NullWipChargeBasis{Valid: true, WipChargeBasis: sqlcgen.WipChargeBasisChargedLaa}
	}
	return sqlcgen.NullWipChargeBasis{Valid: false}
}
