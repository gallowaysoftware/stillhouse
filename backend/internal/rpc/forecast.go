package rpc

import (
	"context"
	"errors"
	"strings"
	"time"

	"fmt"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/distilling"
	"github.com/gallowaysoftware/stillhouse/backend/internal/forecast"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/units"
)

// forecastCaution is on every response rather than in the documentation,
// for the reason stage 185 gave: a plan built on an invented forecast
// looks exactly as authoritative as one built on orders.
const forecastCaution = "Projected, not committed. The committed column is confirmed, unshipped orders — money somebody owes. The forecast column is arithmetic over what was sold before. They are shown side by side and are never added together, because a single number combining the two cannot be taken apart again."

// errNoForecastMethod is the refusal that stands in for the choice
// Stillhouse will not make. A trailing average and a seasonal naive
// disagree, sometimes by a lot, and which is right turns on whether this
// distillery's sales are trending or seasonal.
const errNoForecastMethod = "no forecast method has been chosen, so nothing is projected. Pick one under Plan — a trailing average, the same month last year, or your own numbers. They disagree, sometimes by a lot, and which is right depends on whether your sales are steady or seasonal. Stillhouse cannot see that and you can."

// DemandForecast projects next month's demand, beside the committed
// orders and never added to them. PLAN F7.
func (s *SchedulingService) DemandForecast(
	ctx context.Context,
	req *connect.Request[stillhousev1.DemandForecastRequest],
) (*connect.Response[stillhousev1.DemandForecastResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	month := time.Now().UTC().AddDate(0, 1, 0)
	if v := strings.TrimSpace(req.Msg.GetMonth()); v != "" {
		d, err := parseDate(v, "month")
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		month = d
	}
	start := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, -1)

	out := &stillhousev1.DemandForecastResponse{
		PeriodStart: start.Format("2006-01-02"),
		PeriodEnd:   end.Format("2006-01-02"),
		Caution:     forecastCaution,
	}

	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		cfg, e := q.GetForecastSettings(ctx, u.TenantID)
		if e != nil {
			return e
		}
		if !cfg.ForecastMethod.Valid {
			out.Refused = errNoForecastMethod
			return nil
		}
		method := forecast.Method(cfg.ForecastMethod.ForecastMethod)
		out.Method = forecastMethodToProto(cfg.ForecastMethod)
		out.TrailingMonths = cfg.ForecastTrailingMonths

		// Committed demand: the same figure the production plan uses,
		// fetched from the same query so the two cannot disagree.
		demand, e := q.DemandByProduct(ctx)
		if e != nil {
			return e
		}

		// History reaches back far enough for the widest method: a
		// seasonal naive needs thirteen months, a trailing average needs
		// its window. One query rather than two so the two methods are
		// compared on identical data.
		since := start.AddDate(-1, -int(cfg.ForecastTrailingMonths)-1, 0)
		hist, e := q.MonthlyRemovalsByProduct(ctx, pgtype.Date{Valid: true, Time: since})
		if e != nil {
			return e
		}
		byProduct := map[uuid.UUID][]forecast.Observation{}
		names := map[uuid.UUID]string{}
		for _, h := range hist {
			byProduct[h.ProductID] = append(byProduct[h.ProductID], forecast.Observation{
				Month: h.Month.Time, Bottles: h.Bottles,
			})
			names[h.ProductID] = h.ProductName
		}

		overrides := map[uuid.UUID]sqlcgen.ListDemandForecastsForPeriodRow{}
		ovs, e := q.ListDemandForecastsForPeriod(ctx, sqlcgen.ListDemandForecastsForPeriodParams{
			PeriodStart: pgtype.Date{Valid: true, Time: start},
			PeriodEnd:   pgtype.Date{Valid: true, Time: end},
		})
		if e != nil {
			return e
		}
		for _, o := range ovs {
			overrides[o.ProductID] = o
			names[o.ProductID] = o.ProductName
		}

		// Every product that either owes bottles, has history, or has an
		// override. A product with none of those has nothing to say and
		// is left off rather than listed as a zero.
		committed := map[uuid.UUID]sqlcgen.DemandByProductRow{}
		seen := map[uuid.UUID]bool{}
		var order []uuid.UUID
		add := func(id uuid.UUID) {
			if !seen[id] {
				seen[id] = true
				order = append(order, id)
			}
		}
		for _, d := range demand {
			committed[d.ProductID] = d
			names[d.ProductID] = d.ProductName
			add(d.ProductID)
		}
		for id := range byProduct {
			add(id)
		}
		for id := range overrides {
			add(id)
		}

		for _, id := range order {
			line := &stillhousev1.ForecastLine{
				ProductId:   id.String(),
				ProductName: names[id],
			}
			if d, ok := committed[id]; ok {
				line.BottlesCommitted = d.BottlesOwed
				line.BottlesOnHand = d.BottlesOnHand
			}

			// An operator's own number beats a computed one, and says so.
			if o, ok := overrides[id]; ok {
				line.BottlesForecast = o.Bottles
				line.Available = true
				line.Overridden = true
				line.OverrideReason = o.Reason
				line.Basis = "entered by hand"
				if e := fillRequirement(ctx, q, id, line, start); e != nil {
					return e
				}
				out.TotalLaaNeeded += line.LaaNeeded
				out.Lines = append(out.Lines, line)
				continue
			}

			r := forecast.Project(method, byProduct[id], start, cfg.ForecastTrailingMonths)
			line.BottlesForecast = r.Bottles
			line.Available = r.Available
			line.Missing = r.Missing
			line.Basis = r.Basis
			line.MonthsUsed = r.MonthsUsed
			if e := fillRequirement(ctx, q, id, line, start); e != nil {
				return e
			}
			out.TotalLaaNeeded += line.LaaNeeded
			out.Lines = append(out.Lines, line)
		}
		stock, e := q.AlcoholOnHandForPlanning(ctx)
		if e != nil {
			return e
		}
		out.FreeLaa = round4(stock.FreeLaa)
		out.MaturingLaa = round4(stock.MaturingLaa)
		out.TotalLaaNeeded = round4(out.TotalLaaNeeded)
		return nil
	})
	if err != nil {
		s.logger.Error("DemandForecast", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(out), nil
}

func (s *SchedulingService) SetForecastMethod(
	ctx context.Context,
	req *connect.Request[stillhousev1.SetForecastMethodRequest],
) (*connect.Response[stillhousev1.SetForecastMethodResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	months := req.Msg.GetTrailingMonths()
	if months == 0 {
		months = 3
	}
	if months < 1 || months > 24 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("trailing_months must be between 1 and 24"))
	}

	var row sqlcgen.SetForecastSettingsRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		r, e := q.SetForecastSettings(ctx, sqlcgen.SetForecastSettingsParams{
			ID:                     u.TenantID,
			ForecastMethod:         forecastMethodFromProto(req.Msg.GetMethod()),
			ForecastTrailingMonths: months,
		})
		if e != nil {
			return e
		}
		row = r
		return audit.Write(ctx, q, u.TenantID, u.ID, "tenant", u.TenantID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"forecast_method": req.Msg.GetMethod().String(), "trailing_months": months,
			})
	}); err != nil {
		s.logger.Error("SetForecastMethod", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SetForecastMethodResponse{
		Method:         forecastMethodToProto(row.ForecastMethod),
		TrailingMonths: row.ForecastTrailingMonths,
	}), nil
}

// SaveDemandForecast records the operator's own number for one product
// and month.
//
// The reason is required. "Somebody typed 400" is not a basis anybody can
// check next quarter, and a hand-entered forecast that beats the computed
// one has to be able to explain itself.
func (s *SchedulingService) SaveDemandForecast(
	ctx context.Context,
	req *connect.Request[stillhousev1.SaveDemandForecastRequest],
) (*connect.Response[stillhousev1.SaveDemandForecastResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	productID, err := uuid.Parse(req.Msg.GetProductId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid product_id"))
	}
	if req.Msg.GetBottles() < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bottles cannot be negative"))
	}
	if strings.TrimSpace(req.Msg.GetReason()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(
			"a hand-entered forecast needs its reason — it overrides the computed figure, and next quarter nobody will remember why"))
	}
	month, err := parseDate(req.Msg.GetMonth(), "month")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	start := time.Date(month.Year(), month.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, -1)

	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if _, e := q.UpsertDemandForecast(ctx, sqlcgen.UpsertDemandForecastParams{
			TenantID:    u.TenantID,
			ProductID:   productID,
			PeriodStart: pgtype.Date{Valid: true, Time: start},
			PeriodEnd:   pgtype.Date{Valid: true, Time: end},
			Bottles:     req.Msg.GetBottles(),
			Reason:      req.Msg.GetReason(),
			CreatedBy:   uuid.NullUUID{UUID: u.ID, Valid: true},
		}); e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "demand_forecast", productID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"month": start.Format("2006-01"), "bottles": req.Msg.GetBottles(),
				"reason": req.Msg.GetReason(),
			})
	}); err != nil {
		s.logger.Error("SaveDemandForecast", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SaveDemandForecastResponse{}), nil
}

func forecastMethodToProto(m sqlcgen.NullForecastMethod) stillhousev1.ForecastMethod {
	if !m.Valid {
		return stillhousev1.ForecastMethod_FORECAST_METHOD_UNSPECIFIED
	}
	switch m.ForecastMethod {
	case sqlcgen.ForecastMethodTrailingAverage:
		return stillhousev1.ForecastMethod_FORECAST_METHOD_TRAILING_AVERAGE
	case sqlcgen.ForecastMethodSamePeriodLastYear:
		return stillhousev1.ForecastMethod_FORECAST_METHOD_SAME_PERIOD_LAST_YEAR
	case sqlcgen.ForecastMethodManual:
		return stillhousev1.ForecastMethod_FORECAST_METHOD_MANUAL
	}
	return stillhousev1.ForecastMethod_FORECAST_METHOD_UNSPECIFIED
}

func forecastMethodFromProto(m stillhousev1.ForecastMethod) sqlcgen.NullForecastMethod {
	switch m {
	case stillhousev1.ForecastMethod_FORECAST_METHOD_TRAILING_AVERAGE:
		return sqlcgen.NullForecastMethod{Valid: true, ForecastMethod: sqlcgen.ForecastMethodTrailingAverage}
	case stillhousev1.ForecastMethod_FORECAST_METHOD_SAME_PERIOD_LAST_YEAR:
		return sqlcgen.NullForecastMethod{Valid: true, ForecastMethod: sqlcgen.ForecastMethodSamePeriodLastYear}
	case stillhousev1.ForecastMethod_FORECAST_METHOD_MANUAL:
		return sqlcgen.NullForecastMethod{Valid: true, ForecastMethod: sqlcgen.ForecastMethodManual}
	}
	// UNSPECIFIED clears it back to unset, which is a legitimate thing to
	// want: an operator who chose a method by mistake should be able to
	// go back to being refused rather than keep a projection they do not
	// believe.
	return sqlcgen.NullForecastMethod{Valid: false}
}

// fillRequirement works out what a forecast line implies has to be made,
// and bought to make it.
//
// A product with no forecast still gets its alcohol figure computed from
// zero bottles, which is the honest answer: nothing forecast is nothing
// to make. Only the materials half can refuse for a reason of its own.
func fillRequirement(
	ctx context.Context, q *sqlcgen.Queries, productID uuid.UUID,
	line *stillhousev1.ForecastLine, needBy time.Time,
) error {
	prod, err := q.GetProduct(ctx, productID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}

	batch, err := recipeBatchFor(ctx, q, productID)
	if err != nil {
		return err
	}
	if batch != nil {
		line.RecipeName = batch.Name
	}

	req := forecast.Require(line.BottlesForecast, line.BottlesOnHand,
		prod.BottleSizeMl, prod.TargetAbvPct, batch)

	line.BottlesToMake = req.BottlesToMake
	line.LaaNeeded = round4(req.LAANeeded)
	line.MaterialsAvailable = req.GrainAvailable
	line.MaterialsMissing = req.GrainMissing
	line.Batches = req.Batches
	for _, g := range req.GrainLines {
		line.Materials = append(line.Materials, &stillhousev1.MaterialRequirement{
			Material: g.Material, Quantity: g.Quantity, Uom: g.UOM,
		})
	}

	sched, err := planFor(ctx, q, productID, req, needBy)
	if err != nil {
		return err
	}
	line.Mashes = sched.Mashes
	line.MashesAvailable = sched.MashesAvailable
	line.MashesMissing = sched.MashesMissing
	line.MashVessel = sched.VesselName
	line.OrderBy = sched.OrderBy
	line.OrderByAvailable = sched.OrderByAvailable
	line.OrderByMissing = sched.OrderByMissing
	return nil
}

// planFor turns a requirement into mashes and an order-by date.
//
// Both halves refuse independently, and for reasons that live in
// different places: the mash count wants a vessel on the recipe, the
// order date wants lead times on the materials. An operator who has done
// one and not the other should see the half they have.
func planFor(
	ctx context.Context, q *sqlcgen.Queries, productID uuid.UUID,
	req forecast.Requirement, needBy time.Time,
) (forecast.Schedule, error) {
	rv, err := q.RecipeForProduct(ctx, productID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return forecast.Plan(req, nil, forecast.Lead{}, needBy), nil
		}
		return forecast.Schedule{}, err
	}

	var plant *forecast.Plant
	if rv.MashVesselName.Valid {
		plant = &forecast.Plant{
			Name:         rv.MashVesselName.String,
			CapacityL:    rv.MashVesselCapacityL.Float64,
			BatchVolumeL: rv.TargetWaterL.Float64,
		}
	}

	lead, err := q.LongestLeadTimeForRecipe(ctx, rv.ID)
	if err != nil {
		return forecast.Schedule{}, err
	}
	return forecast.Plan(req, plant, forecast.Lead{
		MaxDays:         lead.MaxDays,
		Slowest:         lead.SlowestMaterial,
		WithoutLeadTime: lead.WithoutLeadTime,
		TotalLines:      lead.Total,
	}, needBy), nil
}

// SetRecipeMashEquipment states which vessel a recipe is mashed in, or
// clears it — which puts the mash count back to refusing rather than
// leaving plant that is no longer used planning the work.
func (s *SchedulingService) SetRecipeMashEquipment(
	ctx context.Context,
	req *connect.Request[stillhousev1.SetRecipeMashEquipmentRequest],
) (*connect.Response[stillhousev1.SetRecipeMashEquipmentResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	rvID, err := uuid.Parse(req.Msg.GetRecipeVersionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid recipe_version_id"))
	}
	var eq uuid.NullUUID
	if v := strings.TrimSpace(req.Msg.GetEquipmentId()); v != "" {
		id, e := uuid.Parse(v)
		if e != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid equipment_id"))
		}
		eq = uuid.NullUUID{UUID: id, Valid: true}
	}
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := q.SetRecipeMashEquipment(ctx, sqlcgen.SetRecipeMashEquipmentParams{
			ID: rvID, MashEquipmentID: eq,
		}); e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "recipe_version", rvID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{"mash_equipment_id": req.Msg.GetEquipmentId()})
	}); err != nil {
		s.logger.Error("SetRecipeMashEquipment", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SetRecipeMashEquipmentResponse{}), nil
}

// recipeBatchFor projects one batch of the recipe a product is planned
// from, or returns nil when none is linked.
//
// The projection runs through internal/distilling, the same code a recipe
// page shows, so the grain a plan asks for and the alcohol a recipe
// promises are the same arithmetic rather than two of it.
func recipeBatchFor(
	ctx context.Context, q *sqlcgen.Queries, productID uuid.UUID,
) (*forecast.RecipeBatch, error) {
	rv, err := q.RecipeForProduct(ctx, productID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // no recipe linked; Require reports why
		}
		return nil, err
	}
	rows, err := q.RecipeIngredientsForProjection(ctx, rv.ID)
	if err != nil {
		return nil, err
	}

	ings := make([]distilling.Ingredient, 0, len(rows))
	lines := make([]forecast.GrainLine, 0, len(rows))
	for _, r := range rows {
		lines = append(lines, forecast.GrainLine{
			Material: r.MaterialName, Quantity: r.Quantity, UOM: r.Uom,
		})
		// Only what has an extract fraction can produce alcohol. A
		// botanical has none and contributes nothing to the projection,
		// while still being on the shopping list.
		if !r.ExtractFraction.Valid || r.Uom != "kg" {
			continue
		}
		ings = append(ings, distilling.Ingredient{
			Name:    r.MaterialName,
			MassKg:  r.Quantity,
			Extract: units.Fraction(r.ExtractFraction.Float64),
		})
	}

	p := distilling.ProjectBatch(ings, distilling.Efficiencies{
		Mash:                 units.Fraction(rv.MashEfficiencyFraction),
		Ferment:              units.Fraction(rv.FermentEfficiencyFraction),
		DistillationRecovery: units.Fraction(rv.DistillationRecoveryFraction),
	})
	return &forecast.RecipeBatch{
		Name:         fmt.Sprintf("%s v%d", rv.RecipeName, rv.VersionNo),
		ProjectedLAA: p.TotalProjectedLAA,
		Ingredients:  lines,
	}, nil
}

// SetProductRecipe links a product to the recipe it is planned from, or
// clears the link. Clearing puts material requirements back to refusing
// rather than leaving a stale recipe planning next month's grain.
func (s *SchedulingService) SetProductRecipe(
	ctx context.Context,
	req *connect.Request[stillhousev1.SetProductRecipeRequest],
) (*connect.Response[stillhousev1.SetProductRecipeResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	productID, err := uuid.Parse(req.Msg.GetProductId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid product_id"))
	}
	var rv uuid.NullUUID
	if v := strings.TrimSpace(req.Msg.GetRecipeVersionId()); v != "" {
		id, e := uuid.Parse(v)
		if e != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid recipe_version_id"))
		}
		rv = uuid.NullUUID{UUID: id, Valid: true}
	}
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if _, e := q.SetProductRecipe(ctx, sqlcgen.SetProductRecipeParams{
			ID: productID, RecipeVersionID: rv,
		}); e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "product", productID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{"recipe_version_id": req.Msg.GetRecipeVersionId()})
	}); err != nil {
		s.logger.Error("SetProductRecipe", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SetProductRecipeResponse{}), nil
}
