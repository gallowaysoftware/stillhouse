package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type SchedulingService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewSchedulingService(db *tenantdb.DB, logger *slog.Logger) *SchedulingService {
	return &SchedulingService{db: db, logger: logger}
}

// ProductionPlan answers "what needs making, and can we make it".
//
// Demand is confirmed, unshipped order lines: real commitments to real
// customers. Stillhouse has no forecast and does not produce one — a plan
// built on an invented forecast looks exactly as authoritative as a plan
// built on orders, which is why the basis is stated on the response and
// shown on the page rather than left to be assumed.
//
// The plant half is the same discipline. Equipment with no recorded
// capacity or no way to estimate a run cannot be planned against, and is
// returned with the reason rather than dropped: an empty schedule and a
// schedule that silently omitted half the still house look identical.
func (s *SchedulingService) ProductionPlan(
	ctx context.Context,
	req *connect.Request[stillhousev1.ProductionPlanRequest],
) (*connect.Response[stillhousev1.ProductionPlanResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	from, to := today, today.AddDate(0, 0, 28)
	if v := req.Msg.GetFrom(); v != "" {
		t, err := parseDate(v, "from")
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		from = t
	}
	if v := req.Msg.GetTo(); v != "" {
		t, err := parseDate(v, "to")
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		to = t
	}
	if to.Before(from) {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("the window ends before it starts"))
	}

	out := &stillhousev1.ProductionPlanResponse{
		From: from.Format("2006-01-02"),
		To:   to.Format("2006-01-02"),
		Basis: "Demand is confirmed, unshipped order lines — real commitments, not " +
			"a forecast; Stillhouse does not produce one. Stock is bottles you own, " +
			"here, released, and not already picked onto an open shipment. Alcohol " +
			"available is bulk you own and hold, casks included.",
	}

	var (
		demand    []sqlcgen.DemandByProductRow
		plant     []sqlcgen.PlannableEquipmentRow
		scheduled []sqlcgen.ScheduledWorkByEquipmentRow
		available float64
	)
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		if demand, e = q.DemandByProduct(ctx); e != nil {
			return e
		}
		if plant, e = q.PlannableEquipment(ctx); e != nil {
			return e
		}
		if scheduled, e = q.ScheduledWorkByEquipment(ctx, sqlcgen.ScheduledWorkByEquipmentParams{
			FromDate: pgtype.Date{Valid: true, Time: from},
			ToDate:   pgtype.Date{Valid: true, Time: to},
		}); e != nil {
			return e
		}
		available, e = q.BulkAvailableForBottling(ctx)
		return e
	}); err != nil {
		s.logger.Error("ProductionPlan", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	noRequiredBy := 0
	for _, d := range demand {
		avail := d.BottlesOnHand - d.BottlesPicked
		if avail < 0 {
			avail = 0
		}
		short := d.BottlesOwed - avail
		if short < 0 {
			short = 0
		}
		litres := float64(short) * float64(d.BottleSizeMl) / 1000
		line := &stillhousev1.DemandLine{
			ProductId: d.ProductID.String(), ProductName: d.ProductName,
			BottleSizeMl: d.BottleSizeMl, BottleAbvPct: d.TargetAbvPct,
			BottlesOwed: d.BottlesOwed, BottlesOnHand: d.BottlesOnHand,
			BottlesPicked: d.BottlesPicked, BottlesAvailable: avail,
			Shortfall:    short,
			ShortfallLaa: litres * d.TargetAbvPct / 100,
		}
		if d.EarliestRequired.Valid {
			line.EarliestRequired = d.EarliestRequired.Time.Format("2006-01-02")
			line.Late = short > 0 && d.EarliestRequired.Time.Before(today)
		} else {
			noRequiredBy++
		}
		out.ShortfallLaa += line.GetShortfallLaa()
		out.Demand = append(out.Demand, line)
	}
	out.AvailableLaa = available
	out.ShortOfAlcohol = out.ShortfallLaa > available

	byEquipment := map[uuid.UUID][]sqlcgen.ScheduledWorkByEquipmentRow{}
	for _, w := range scheduled {
		byEquipment[w.EquipmentID] = append(byEquipment[w.EquipmentID], w)
	}

	unplannable := 0
	for _, e := range plant {
		item := &stillhousev1.PlannableEquipment{
			Id: e.ID.String(), Name: e.Name,
			Kind:                equipmentKindToProto(e.Kind),
			Status:              equipmentStatusToProto(e.Status),
			ObservedMedianHours: e.ObservedMedianHours,
			ObservedRuns:        e.ObservedRuns,
		}
		if e.CapacityL.Valid {
			item.CapacityL, item.CapacityLSet = e.CapacityL.Float64, true
		}
		if e.TypicalRunHours.Valid {
			item.TypicalRunHours, item.TypicalRunHoursSet = e.TypicalRunHours.Float64, true
		}

		// Two things make a piece of plant plannable: it is available,
		// and there is a defensible number for how long a run on it
		// takes. Observed beats recorded; neither means it cannot be
		// planned against, which is said rather than assumed away.
		hours := item.GetObservedMedianHours()
		if hours <= 0 {
			hours = item.GetTypicalRunHours()
		}
		switch {
		case e.Status == sqlcgen.EquipmentStatusDown:
			item.WhyNot = "it is down"
		case hours <= 0:
			item.WhyNot = "no run has been timed on it and no typical run time is " +
				"recorded, so there is nothing to plan a duration from"
		case !e.CapacityL.Valid:
			item.WhyNot = "no capacity is recorded, so nothing can be sized against it"
		default:
			item.Plannable = true
		}
		if !item.Plannable {
			unplannable++
		}

		for _, w := range byEquipment[e.ID] {
			item.Scheduled = append(item.Scheduled, &stillhousev1.ScheduledWork{
				WorkOrderId: w.WorkOrderID.String(), WorkOrderNo: w.WorkOrderNo,
				Title:        w.Title,
				ScheduledFor: formatDate(w.ScheduledFor),
				DueOn:        formatDate(w.DueOn),
				Status:       string(w.WorkStatus),
			})
			item.ScheduledHours += hours
		}
		out.Equipment = append(out.Equipment, item)
	}

	if unplannable > 0 {
		out.BlindSpots = append(out.BlindSpots, fmt.Sprintf(
			"%d piece%s of equipment cannot be planned against — see the reason on "+
				"each. Anything they would have made is not in this plan.",
			unplannable, plural(int32(unplannable))))
	}
	if noRequiredBy > 0 {
		out.BlindSpots = append(out.BlindSpots, fmt.Sprintf(
			"%d product%s owed on orders with no required-by date, so nothing here "+
				"can say whether they are late.",
			noRequiredBy, plural(int32(noRequiredBy))))
	}
	if len(out.GetDemand()) == 0 {
		out.BlindSpots = append(out.BlindSpots,
			"No confirmed orders are outstanding, so there is no demand to plan "+
				"against. This is not a statement that nothing needs making — it is "+
				"a statement that nothing has been ordered.")
	}
	return connect.NewResponse(out), nil
}
