package rpc

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// exactnessNote is on every response rather than in the documentation,
// because the person reading a recall is not reading the documentation.
const exactnessNote = "Exact up to the production gauge: the material lot, the mashes, the fermentations and the gauges below are recorded links, not inferences. Past the gauge, spirit is blended, vatted and transferred, so the packaged lots and removals are everything that COULD carry it — not everything that does. Stillhouse will not narrow that for you: doing so would either recall stock that was never affected or leave stock that was."

// SimulateRecall walks forward from a material lot. PLAN I5.
//
// It simulates and does not act: nothing is held, nothing is blocked,
// nothing is notified. A recall is a decision with a cost, and a tool that
// started making it on the strength of a lot number being typed into a box
// would be the wrong tool.
func (s *TraceabilityService) SimulateRecall(
	ctx context.Context,
	req *connect.Request[stillhousev1.SimulateRecallRequest],
) (*connect.Response[stillhousev1.SimulateRecallResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	lotID, err := uuid.Parse(req.Msg.GetMaterialLotId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid material_lot_id"))
	}

	out := &stillhousev1.SimulateRecallResponse{ExactnessNote: exactnessNote}
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		chain, e := q.RecallExactChainFromMaterialLot(ctx, lotID)
		if e != nil {
			return e
		}
		if len(chain) == 0 {
			out.Note = "This lot has not been used in any mash, so nothing has been made from it. That is not the same as the lot being unknown — check the lot id if you expected otherwise."
			return nil
		}

		// One up, and the exact half of one down.
		out.MaterialName = chain[0].MaterialName
		out.SupplierName = chain[0].SupplierName.String
		out.SupplierLot = chain[0].SupplierLot

		seenMash := map[string]bool{}
		seenGauge := map[string]bool{}
		// Earliest affected gauge per container: a run that bottled before
		// the spirit arrived cannot contain it, so each container is
		// searched from its own arrival date rather than from the earliest
		// across all of them.
		earliest := map[uuid.UUID]time.Time{}
		for _, r := range chain {
			if id := r.MashRunID.String(); !seenMash[id] {
				seenMash[id] = true
				out.Mashes = append(out.Mashes, &stillhousev1.RecallMashLink{
					MashRunId:    id,
					MashNo:       r.MashNo,
					MashDate:     r.MashDate.Time.Format("2006-01-02"),
					QuantityUsed: r.QuantityUsed,
					Uom:          r.Uom,
				})
			}
			if !r.ProductionGaugeID.Valid {
				continue // mashed but not yet distilled, or never charged
			}
			gid := r.ProductionGaugeID.UUID
			if seenGauge[gid.String()] {
				continue
			}
			seenGauge[gid.String()] = true
			voided := r.DistillationVoidedAt.Valid
			out.Gauges = append(out.Gauges, &stillhousev1.RecallGaugeLink{
				ProductionGaugeId: gid.String(),
				GaugeDate:         r.GaugeDate.Time.Format("2006-01-02"),
				Laa:               r.GaugeLaa.Float64,
				ContainerId:       r.DestinationContainerID.UUID.String(),
				ContainerName:     r.ContainerName.String,
				DistillationRunNo: r.DistillationRunNo.Int32,
				Voided:            voided,
			})
			// A voided run's spirit went back out of the ledger, so it
			// cannot be in a bottle and must not widen the search.
			if voided || !r.DestinationContainerID.Valid {
				continue
			}
			c := r.DestinationContainerID.UUID
			if prev, ok := earliest[c]; !ok || r.GaugeDate.Time.Before(prev) {
				earliest[c] = r.GaugeDate.Time
			}
		}
		if len(earliest) == 0 {
			out.Note = "Nothing made from this lot has reached a production gauge yet, so nothing has been bottled from it."
			return nil
		}

		// Possible contact. Queried per container so each is bounded by
		// its own arrival date.
		var packagedIDs []uuid.UUID
		for containerID, from := range earliest {
			lots, e := q.RecallPackagedLotsFromContainers(ctx, sqlcgen.RecallPackagedLotsFromContainersParams{
				ContainerIds: []uuid.UUID{containerID},
				Earliest:     pgtype.Date{Valid: true, Time: from},
			})
			if e != nil {
				return e
			}
			for _, l := range lots {
				if !l.PackagedInventoryID.Valid {
					continue // a run that produced no packaged lot row
				}
				voided := l.BottlingVoidedAt.Valid
				out.PackagedLots = append(out.PackagedLots, &stillhousev1.RecallPackagedLot{
					PackagedInventoryId: l.PackagedInventoryID.UUID.String(),
					LotCode:             l.LotCode.String,
					ProductName:         l.ProductName.String,
					BottledOn:           l.BottledOn.Time.Format("2006-01-02"),
					ContainerName:       l.ContainerName,
					BottlesPackaged:     l.BottlesPackaged.Int32,
					BottlesOnHand:       l.BottlesOnHand.Int32,
					BottlesRemoved:      l.BottlesRemoved.Int32,
					Voided:              voided,
				})
				if voided {
					continue
				}
				out.BottlesPackaged += l.BottlesPackaged.Int32
				out.BottlesOnHand += l.BottlesOnHand.Int32
				out.BottlesRemoved += l.BottlesRemoved.Int32
				packagedIDs = append(packagedIDs, l.PackagedInventoryID.UUID)
			}
		}
		if len(packagedIDs) == 0 {
			return nil
		}

		removals, e := q.RecallRemovalsForPackagedLots(ctx, packagedIDs)
		if e != nil {
			return e
		}
		for _, r := range removals {
			voided := r.VoidedAt.Valid
			if voided {
				out.VoidedRemovals++
			}
			out.Removals = append(out.Removals, &stillhousev1.RecallRemoval{
				Id:              r.ID.String(),
				RemovalDate:     r.RemovalDate.Time.Format("2006-01-02"),
				Bottles:         r.BottlesRemoved,
				LotCode:         r.LotCode,
				CustomerId:      r.CustomerID,
				CustomerName:    r.CustomerName,
				DestinationName: r.DestinationName,
				Voided:          voided,
			})
		}
		return nil
	})
	if err != nil {
		s.logger.Error("SimulateRecall", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(out), nil
}
