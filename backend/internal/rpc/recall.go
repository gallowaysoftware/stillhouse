package rpc

import (
	"context"
	"errors"
	"time"

	"strings"

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
	origin := req.Msg.GetOrigin()
	originID := req.Msg.GetOriginId()
	// Callers written before other origins existed pass material_lot_id
	// and nothing else.
	if origin == stillhousev1.RecallOrigin_RECALL_ORIGIN_UNSPECIFIED && req.Msg.GetMaterialLotId() != "" {
		origin = stillhousev1.RecallOrigin_RECALL_ORIGIN_MATERIAL_LOT
		originID = req.Msg.GetMaterialLotId()
	}
	id, err := uuid.Parse(originID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid origin_id"))
	}

	switch origin {
	case stillhousev1.RecallOrigin_RECALL_ORIGIN_PACKAGED_LOT:
		return s.recallFromPackagedLot(ctx, u.TenantID, id)
	case stillhousev1.RecallOrigin_RECALL_ORIGIN_CONTAINER:
		return s.recallFromContainer(ctx, u.TenantID, id, req.Msg.GetSince())
	case stillhousev1.RecallOrigin_RECALL_ORIGIN_MATERIAL_LOT:
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say where the recall starts: a material lot, a packaged lot, or a container"))
	}
	lotID := id

	out := &stillhousev1.SimulateRecallResponse{
		ExactnessNote: exactnessNote,
		Origin:        stillhousev1.RecallOrigin_RECALL_ORIGIN_MATERIAL_LOT,
	}
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

// recallFromPackagedLot answers the commonest recall there is: a lot code
// on a bottle in somebody's hand.
//
// Exact throughout, and that is not a shortcut — the lot code IS the
// thing being recalled, so there is nothing to infer. No possible-contact
// set, no boundary to explain, and the answer is a list to act on rather
// than a list to judge.
func (s *TraceabilityService) recallFromPackagedLot(
	ctx context.Context, tenantID, lotID uuid.UUID,
) (*connect.Response[stillhousev1.SimulateRecallResponse], error) {
	out := &stillhousev1.SimulateRecallResponse{
		Origin:          stillhousev1.RecallOrigin_RECALL_ORIGIN_PACKAGED_LOT,
		ExactThroughout: true,
		ExactnessNote: "Exact throughout. The lot code is what is being recalled, so nothing here is inferred: " +
			"these are the removals that carried this lot, and the bottles still on hand are this lot's.",
	}
	err := s.db.WithTenantTx(ctx, tenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		rows, e := q.RecallPackagedLotOneDown(ctx, lotID)
		if e != nil {
			return e
		}
		if len(rows) == 0 {
			// Either nothing has left, or the lot does not exist. The two
			// are different and the caller has to be able to tell.
			out.Note = "No removals are recorded for this lot. Either none of it has left, or the lot id is wrong — check it before concluding the first."
			return nil
		}
		first := rows[0]
		out.PackagedLots = append(out.PackagedLots, &stillhousev1.RecallPackagedLot{
			PackagedInventoryId: first.PackagedInventoryID.String(),
			LotCode:             first.LotCode,
			BottlesOnHand:       first.BottlesOnHand,
		})
		out.BottlesOnHand = first.BottlesOnHand
		for _, r := range rows {
			voided := r.VoidedAt.Valid
			if voided {
				out.VoidedRemovals++
			} else {
				out.BottlesRemoved += r.BottlesRemoved
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
		s.logger.Error("recallFromPackagedLot", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(out), nil
}

// recallFromContainer follows spirit forward out of a cask or tank.
//
// Possible contact from the start: a container has no lot identity, so
// everything that left it after the date given may carry whatever is
// wrong with it. The walk is recursive because spirit does not move once
// — cask to vatting tank to bottling tank — and a single hop would
// under-recall, which is the failure that leaves affected stock on a
// shelf.
func (s *TraceabilityService) recallFromContainer(
	ctx context.Context, tenantID, containerID uuid.UUID, sinceStr string,
) (*connect.Response[stillhousev1.SimulateRecallResponse], error) {
	since := time.Time{}
	if v := strings.TrimSpace(sinceStr); v != "" {
		d, err := parseDate(v, "since")
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		since = d
	}

	out := &stillhousev1.SimulateRecallResponse{
		Origin: stillhousev1.RecallOrigin_RECALL_ORIGIN_CONTAINER,
		ExactnessNote: "Possible contact throughout. A container has no lot identity, so everything that left it on or after the date given may carry whatever is wrong with it — " +
			"and spirit does not move once, so the walk follows it onward through every vessel it reached. Narrow the date if you know when the problem started; widening it recalls stock that was never affected.",
	}
	if since.IsZero() {
		out.Note = "No start date was given, so every move this container has ever made was followed. That is almost certainly wider than the problem — give a date if you know one."
	}

	err := s.db.WithTenantTx(ctx, tenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		fed, e := q.RecallContainersFedBy(ctx, sqlcgen.RecallContainersFedByParams{
			ContainerID: uuid.NullUUID{UUID: containerID, Valid: true},
			Since:       pgtype.Timestamptz{Valid: true, Time: since},
		})
		if e != nil {
			return e
		}
		// The origin itself is where bottling may also have drawn from.
		ids := []uuid.UUID{containerID}
		for _, c := range fed {
			ids = append(ids, c.ContainerID)
			if c.Depth > out.MovesFollowed {
				out.MovesFollowed = c.Depth
			}
			out.Containers = append(out.Containers, &stillhousev1.RecallContainer{
				Id: c.ContainerID.String(), Name: c.ContainerName, Moves: c.Depth,
			})
		}
		// The cap in the query is 10; hitting it means the answer may be
		// short, and short is the dangerous direction.
		out.WalkTruncated = out.MovesFollowed >= 10

		lots, e := q.RecallPackagedLotsFromContainerSet(ctx, sqlcgen.RecallPackagedLotsFromContainerSetParams{
			ContainerIds: ids,
			Earliest:     pgtype.Date{Valid: true, Time: since},
		})
		if e != nil {
			return e
		}
		var packagedIDs []uuid.UUID
		for _, l := range lots {
			if !l.PackagedInventoryID.Valid {
				continue
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
				Id: r.ID.String(), RemovalDate: r.RemovalDate.Time.Format("2006-01-02"),
				Bottles: r.BottlesRemoved, LotCode: r.LotCode,
				CustomerId: r.CustomerID, CustomerName: r.CustomerName,
				DestinationName: r.DestinationName, Voided: voided,
			})
		}
		return nil
	})
	if err != nil {
		s.logger.Error("recallFromContainer", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(out), nil
}
