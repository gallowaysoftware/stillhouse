package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type BottlingService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewBottlingService(db *tenantdb.DB, logger *slog.Logger) *BottlingService {
	return &BottlingService{db: db, logger: logger}
}

// CreateBottlingRun runs the full bottling transaction:
//
//  1. validate inputs, fetch product + source container
//  2. compute required volume (= bottle_count × bottle_size + loss)
//  3. allocate excise stamps for the destination jurisdiction (FIFO across
//     received orders); reject early if there aren't enough on hand
//  4. write a BulkMovement (reason=transfer_to_packaging) draining the source
//  5. update the source container balance
//  6. insert the bottling_run row
//  7. write one bottling_run_stamp_usage per stamp order consumed and bump
//     each order's quantity_applied counter
//  8. upsert packaged_inventory for (product, lot_code, jurisdiction)
func (s *BottlingService) CreateBottlingRun(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreateBottlingRunRequest],
) (*connect.Response[stillhousev1.CreateBottlingRunResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	productID, err := uuid.Parse(in.GetProductId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid product_id"))
	}
	sourceID, err := uuid.Parse(in.GetSourceContainerId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid source_container_id"))
	}
	if in.GetDestinationJurisdiction() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("destination_jurisdiction is required"))
	}
	if in.GetBottleCount() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bottle_count must be > 0"))
	}
	if in.GetLotCode() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("lot_code is required"))
	}

	bottlingDate, err := parseDateOrToday(in.GetBottlingDate())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var (
		run        sqlcgen.BottlingRun
		stampUses  []sqlcgen.BottlingRunStampUsage
		packaged   sqlcgen.PackagedInventory
		productOut sqlcgen.Product
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := assertDateNotInLockedPeriod(ctx, q, bottlingDate); e != nil {
			return e
		}
		product, e := q.GetProduct(ctx, productID)
		if e != nil {
			return e
		}
		if product.Archived {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("product is archived"))
		}
		source, e := q.GetBulkContainerForUpdate(ctx, sourceID)
		if e != nil {
			return e
		}
		if !source.CurrentAbvPct.Valid {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("source container is empty"))
		}

		// Bottling debit is in LAA terms, not in volume. The packaged
		// inventory carries product.target_abv_pct on every bottle, so the
		// alcohol going into bottles is bottleVolume * target_abv. If the
		// operator is bottling cask-strength spirit at its source ABV,
		// less liquid is pulled from the source than ends up in bottles —
		// the difference is water added during bottling (implicit
		// dilution). Tracking LAA directly conserves alcohol across the
		// source→packaged transition and keeps B266 figures balanced.
		// Bottling loss is liquid lost from the finished spirit stream,
		// so it carries product.target_abv_pct too.
		bottleVolumeL := float64(in.GetBottleCount()) * float64(product.BottleSizeMl) / 1000
		bottleLAA := bottleVolumeL * product.TargetAbvPct / 100
		lossLAA := in.GetBottlingLossL() * product.TargetAbvPct / 100
		requiredLAA := bottleLAA + lossLAA

		abv := source.CurrentAbvPct.Float64
		if abv <= 0 {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("source container has no measurable ABV"))
		}
		// Reject bottling at a higher ABV than the source — the system
		// has no way to add ethanol, only to dilute.
		if product.TargetAbvPct > abv+1e-6 {
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("product target ABV (%.2f%%) exceeds source ABV (%.2f%%) — cannot bottle stronger than the source",
					product.TargetAbvPct, abv))
		}
		if source.CurrentLaa+1e-6 < requiredLAA {
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("source has %.4f LAA on hand but bottling needs %.4f LAA (%.2f%% of %d × %d ml bottles%s)",
					source.CurrentLaa, requiredLAA, product.TargetAbvPct, in.GetBottleCount(), product.BottleSizeMl,
					func() string {
						if in.GetBottlingLossL() > 0 {
							return fmt.Sprintf(" + %.3f L loss", in.GetBottlingLossL())
						}
						return ""
					}()))
		}
		// Physical volume actually drawn from the source. ≤ bottleVolume
		// when product target ABV < source ABV (dilution); equal when
		// they match.
		requiredVolume := requiredLAA / abv * 100
		if source.CurrentVolumeL+1e-6 < requiredVolume {
			// Defensive: LAA check should have caught this, but float math
			// could theoretically allow a sliver through.
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("source has %.4f L on hand but bottling needs %.4f L of source spirit", source.CurrentVolumeL, requiredVolume))
		}
		laa := requiredLAA

		// 3. Allocate stamps FIFO.
		stampSources, e := q.ListStampOrdersWithAvailable(ctx, in.GetDestinationJurisdiction())
		if e != nil {
			return e
		}
		var totalAvailable int32
		for _, o := range stampSources {
			totalAvailable += o.AvailableCount
		}
		if totalAvailable < in.GetBottleCount() {
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("only %d stamps available for %s, need %d",
					totalAvailable, in.GetDestinationJurisdiction(), in.GetBottleCount()))
		}

		// 4. BulkMovement draining the source.
		mv, e := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
			TenantID:               u.TenantID,
			SourceContainerID:      uuid.NullUUID{UUID: sourceID, Valid: true},
			DestinationContainerID: uuid.NullUUID{Valid: false},
			VolumeL:                requiredVolume,
			AbvPct:                 abv,
			Laa:                    laa,
			Reason:                 sqlcgen.BulkMovementReasonTransferToPackaging,
			ReferenceType:          "bottling_run",
			ReferenceID:            uuid.NullUUID{Valid: false}, // run id assigned below
			Notes:                  in.GetNotes(),
			OccurredAt:             pgtype.Timestamptz{Valid: true, Time: bottlingDate.Time},
		})
		if e != nil {
			return e
		}

		// 5. Update source balance.
		newSrcVol := source.CurrentVolumeL - requiredVolume
		newSrcAbv := source.CurrentAbvPct
		newSrcLAA := 0.0
		if newSrcVol > 0 {
			newSrcLAA = newSrcVol * newSrcAbv.Float64 / 100
		} else {
			newSrcAbv = pgtype.Float8{Valid: false}
		}
		if _, e := q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
			ID:             sourceID,
			CurrentVolumeL: newSrcVol,
			CurrentAbvPct:  newSrcAbv,
			CurrentLaa:     newSrcLAA,
		}); e != nil {
			return e
		}

		// 6. Duty, if this run is the duty event.
		//
		// A licensee without an excise warehouse licence cannot hold
		// non-duty-paid packaged spirits, so duty crystallises here rather
		// than at a removal months later. Charged on the sealed bottles:
		// the packaging loss never became packaged spirits, and its
		// treatment is a separate question (PLAN A5).
		basis, e := tenantDutyBasis(ctx, q, u.TenantID)
		if e != nil {
			return e
		}
		var (
			dutyRate   pgtype.Float8
			dutyAmount pgtype.Float8
			dutySource string
		)
		if basis.dutiesAtPackaging(bottlingDate.Time) {
			rate, amount, src, de := packagingDuty(bottlingDate.Time,
				in.GetBottleCount(), product.BottleSizeMl, product.TargetAbvPct)
			if de != nil {
				return asRateRefusal(de)
			}
			dutyRate = pgtype.Float8{Float64: rate, Valid: true}
			dutyAmount = pgtype.Float8{Float64: amount, Valid: true}
			dutySource = src
		}

		// 7. Insert bottling_run.
		if e := q.LockDocumentSequence(ctx, "bottling_runs"); e != nil {
			return e
		}
		nextNo, e := q.NextBottlingRunNo(ctx)
		if e != nil {
			return e
		}
		run, e = q.CreateBottlingRun(ctx, sqlcgen.CreateBottlingRunParams{
			TenantID:                u.TenantID,
			RunNo:                   nextNo,
			ProductID:               productID,
			SourceContainerID:       sourceID,
			DestinationJurisdiction: in.GetDestinationJurisdiction(),
			BottlingDate:            bottlingDate,
			BottleCount:             in.GetBottleCount(),
			BottlingLossL:           in.GetBottlingLossL(),
			LotCode:                 in.GetLotCode(),
			TankGaugeVolumeL:        requiredVolume,
			TankGaugeAbvPct:         abv,
			TankGaugeLaa:            laa,
			BulkMovementID:          mv.ID,
			Notes:                   in.GetNotes(),
			DutyRatePerLaa:          dutyRate,
			DutyAmountCad:           dutyAmount,
			DutyRateSource:          dutySource,
		})
		if e != nil {
			return e
		}

		// 7. Apply stamps + record usage.
		remaining := in.GetBottleCount()
		for _, orderRow := range stampSources {
			if remaining <= 0 {
				break
			}
			take := orderRow.AvailableCount
			if take > remaining {
				take = remaining
			}
			serialStart, serialEnd := "", ""
			if orderRow.SerialStart.Valid {
				serialStart, serialEnd = computeStampRange(
					orderRow.SerialStart.String,
					orderRow.QuantityApplied,
					take,
				)
			}
			use, e := q.CreateBottlingRunStampUsage(ctx, sqlcgen.CreateBottlingRunStampUsageParams{
				TenantID:      u.TenantID,
				BottlingRunID: run.ID,
				StampOrderID:  orderRow.ID,
				BottleCount:   take,
				SerialStart:   serialStart,
				SerialEnd:     serialEnd,
				Voids:         0,
			})
			if e != nil {
				return e
			}
			stampUses = append(stampUses, use)
			if _, e := q.IncrementStampOrderApplied(ctx, sqlcgen.IncrementStampOrderAppliedParams{
				ID:              orderRow.ID,
				QuantityApplied: take,
			}); e != nil {
				return e
			}
			remaining -= take
		}

		// 8. Upsert packaged_inventory.
		packaged, e = q.UpsertPackagedInventory(ctx, sqlcgen.UpsertPackagedInventoryParams{
			TenantID:      u.TenantID,
			ProductID:     productID,
			LotCode:       in.GetLotCode(),
			Jurisdiction:  in.GetDestinationJurisdiction(),
			BottlingRunID: uuid.NullUUID{UUID: run.ID, Valid: true},
			BottlesOnHand: in.GetBottleCount(),
		})
		if e != nil {
			return e
		}
		productOut = product

		// 9. Audit log.
		if e := audit.Write(ctx, q, u.TenantID, u.ID, "bottling_run", run.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"run_no":       run.RunNo,
				"product_id":   productID.String(),
				"product_name": product.Name,
				"jurisdiction": in.GetDestinationJurisdiction(),
				"bottle_count": in.GetBottleCount(),
				"lot_code":     in.GetLotCode(),
				"tank_laa":     laa,
				"source_id":    sourceID.String(),
			}); e != nil {
			return e
		}
		return nil
	})
	if err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("product or source not found"))
		}
		s.logger.Error("CreateBottlingRun", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	runProto := bottlingRunToProto(run, productOut, nil)
	for _, u := range stampUses {
		runProto.StampUsage = append(runProto.StampUsage, bottlingRunStampUsageToProto(u, in.GetDestinationJurisdiction()))
	}
	pkgProto := packagedInventoryToProto(packaged, productOut)
	return connect.NewResponse(&stillhousev1.CreateBottlingRunResponse{
		Run:      runProto,
		Packaged: pkgProto,
	}), nil
}

func (s *BottlingService) GetBottlingRun(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetBottlingRunRequest],
) (*connect.Response[stillhousev1.GetBottlingRunResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	var (
		run     sqlcgen.BottlingRun
		product sqlcgen.Product
		usage   []sqlcgen.ListBottlingRunStampUsageRow
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		run, e = q.GetBottlingRun(ctx, id)
		if e != nil {
			return e
		}
		product, e = q.GetProduct(ctx, run.ProductID)
		if e != nil {
			return e
		}
		usage, e = q.ListBottlingRunStampUsage(ctx, id)
		return e
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("bottling run not found"))
		}
		s.logger.Error("GetBottlingRun", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := bottlingRunToProto(run, product, nil)
	for _, u := range usage {
		out.StampUsage = append(out.StampUsage, bottlingRunStampUsageRowToProto(u))
	}
	return connect.NewResponse(&stillhousev1.GetBottlingRunResponse{Run: out}), nil
}

func (s *BottlingService) ListBottlingRuns(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListBottlingRunsRequest],
) (*connect.Response[stillhousev1.ListBottlingRunsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	limit := req.Msg.GetLimit()
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	offset := req.Msg.GetOffset()
	if offset < 0 {
		offset = 0
	}
	var pStart, pEnd pgtype.Date
	if s := req.Msg.GetPeriodStart(); s != "" {
		d, err := parseDateOrToday(s)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid period_start"))
		}
		pStart = d
	}
	if s := req.Msg.GetPeriodEnd(); s != "" {
		d, err := parseDateOrToday(s)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid period_end"))
		}
		pEnd = d
	}
	var (
		rows  []sqlcgen.ListBottlingRunsRow
		total int32
	)
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListBottlingRuns(ctx, sqlcgen.ListBottlingRunsParams{
			Limit:       limit,
			Offset:      offset,
			PeriodStart: pStart,
			PeriodEnd:   pEnd,
		})
		if e != nil {
			return e
		}
		total, e = q.CountBottlingRuns(ctx, sqlcgen.CountBottlingRunsParams{
			PeriodStart: pStart,
			PeriodEnd:   pEnd,
		})
		return e
	})
	if err != nil {
		s.logger.Error("ListBottlingRuns", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.BottlingRun, 0, len(rows))
	for _, r := range rows {
		run := sqlcgen.BottlingRun{
			ID:                      r.ID,
			TenantID:                r.TenantID,
			RunNo:                   r.RunNo,
			ProductID:               r.ProductID,
			SourceContainerID:       r.SourceContainerID,
			DestinationJurisdiction: r.DestinationJurisdiction,
			BottlingDate:            r.BottlingDate,
			BottleCount:             r.BottleCount,
			BottlingLossL:           r.BottlingLossL,
			LotCode:                 r.LotCode,
			TankGaugeVolumeL:        r.TankGaugeVolumeL,
			TankGaugeAbvPct:         r.TankGaugeAbvPct,
			TankGaugeLaa:            r.TankGaugeLaa,
			BulkMovementID:          r.BulkMovementID,
			Notes:                   r.Notes,
			CreatedAt:               r.CreatedAt,
			UpdatedAt:               r.UpdatedAt,
			VoidedAt:                r.VoidedAt,
			VoidedBy:                r.VoidedBy,
			VoidedReason:            r.VoidedReason,
		}
		out = append(out, bottlingRunToProto(run, sqlcgen.Product{
			Name:         r.ProductName,
			BottleSizeMl: r.ProductBottleSizeMl,
			TargetAbvPct: r.ProductTargetAbvPct,
		}, nil))
	}
	return connect.NewResponse(&stillhousev1.ListBottlingRunsResponse{Runs: out, TotalCount: total}), nil
}

func (s *BottlingService) VoidBottlingRun(
	ctx context.Context,
	req *connect.Request[stillhousev1.VoidBottlingRunRequest],
) (*connect.Response[stillhousev1.VoidBottlingRunResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	reason := req.Msg.GetReason()
	if reason == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("reason is required"))
	}

	var (
		voided  sqlcgen.BottlingRun
		product sqlcgen.Product
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		existing, e := q.GetBottlingRun(ctx, id)
		if e != nil {
			return e
		}
		if existing.VoidedAt.Valid {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("bottling run is already voided"))
		}
		if e := assertDateNotInLockedPeriod(ctx, q, existing.BottlingDate); e != nil {
			return e
		}

		// 1) Reverse each stamp allocation. If any stamp_order can't be
		// decremented (shouldn't happen — IncrementApplied at bottling time
		// guarded against the inverse) we surface a clear error.
		usage, e := q.ListBottlingRunStampUsage(ctx, id)
		if e != nil {
			return e
		}
		for _, useRow := range usage {
			if _, e := q.DecrementStampOrderApplied(ctx, sqlcgen.DecrementStampOrderAppliedParams{
				ID:              useRow.StampOrderID,
				QuantityApplied: useRow.BottleCount,
			}); e != nil {
				return fmt.Errorf("decrement stamp order %s: %w", useRow.StampOrderID, e)
			}
		}

		// 2) Reverse packaged_inventory contribution. If bottles_on_hand is
		// already short (operator has shipped some bottles) we can't void
		// without an inconsistency — flag and abort.
		pkg, e := q.PackagedInventoryByLot(ctx, sqlcgen.PackagedInventoryByLotParams{
			ProductID:    existing.ProductID,
			LotCode:      existing.LotCode,
			Jurisdiction: existing.DestinationJurisdiction,
		})
		if e != nil {
			return e
		}
		if pkg.BottlesOnHand < existing.BottleCount {
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("only %d of %d bottles remain in packaged inventory — void any removals first",
					pkg.BottlesOnHand, existing.BottleCount))
		}
		if _, e := q.DecrementPackagedInventoryByRun(ctx, sqlcgen.DecrementPackagedInventoryByRunParams{
			ID:            pkg.ID,
			BottlesOnHand: existing.BottleCount,
		}); e != nil {
			return e
		}

		// 3) Refund the source bulk container. Reverses tank_gauge_volume/abv/laa
		// via the same applyDeposit math used at filling time, and writes a
		// regauge_correction movement so the journal records the inverse.
		container, e := q.GetBulkContainerForUpdate(ctx, existing.SourceContainerID)
		if e != nil {
			return e
		}
		newVol, newABV, newLAA := applyDeposit(
			container.CurrentVolumeL, container.CurrentAbvPct,
			existing.TankGaugeVolumeL, existing.TankGaugeAbvPct,
		)
		if _, e := q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
			ID:             container.ID,
			CurrentVolumeL: newVol,
			CurrentAbvPct:  newABV,
			CurrentLaa:     newLAA,
		}); e != nil {
			return e
		}
		if _, e := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
			TenantID:               u.TenantID,
			SourceContainerID:      uuid.NullUUID{Valid: false},
			DestinationContainerID: uuid.NullUUID{UUID: container.ID, Valid: true},
			VolumeL:                existing.TankGaugeVolumeL,
			AbvPct:                 existing.TankGaugeAbvPct,
			Laa:                    existing.TankGaugeLaa,
			Reason:                 sqlcgen.BulkMovementReasonRegaugeCorrection,
			ReferenceType:          "bottling_run_void",
			ReferenceID:            uuid.NullUUID{UUID: existing.ID, Valid: true},
			Notes:                  "void of bottling run " + fmt.Sprintf("#%d", existing.RunNo) + ": " + reason,
			OccurredAt:             pgtype.Timestamptz{Valid: true, Time: time.Now()},
		}); e != nil {
			return e
		}

		// 4) Mark the run voided. Has to be the last step so a failure in any
		// of the reversals above leaves the run un-voided and the operator can
		// retry cleanly.
		voided, e = q.VoidBottlingRun(ctx, sqlcgen.VoidBottlingRunParams{
			ID:           id,
			VoidedBy:     uuid.NullUUID{UUID: u.ID, Valid: true},
			VoidedReason: reason,
		})
		if e != nil {
			return e
		}
		product, e = q.GetProduct(ctx, voided.ProductID)
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "bottling_run", voided.ID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"event":            "voided",
				"run_no":           voided.RunNo,
				"bottle_count":     voided.BottleCount,
				"tank_gauge_laa":   voided.TankGaugeLaa,
				"refund_container": voided.SourceContainerID.String(),
				"stamp_orders_n":   len(usage),
				"reason":           reason,
			})
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("bottling run not found"))
		}
		s.logger.Error("VoidBottlingRun", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.VoidBottlingRunResponse{
		Run: bottlingRunToProto(voided, product, nil),
	}), nil
}

func (s *BottlingService) ListPackagedInventory(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListPackagedInventoryRequest],
) (*connect.Response[stillhousev1.ListPackagedInventoryResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.ListPackagedInventoryRow
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListPackagedInventory(ctx, req.Msg.GetIncludeEmpty())
		return e
	})
	if err != nil {
		s.logger.Error("ListPackagedInventory", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.PackagedInventoryRow, 0, len(rows))
	for _, r := range rows {
		row := &stillhousev1.PackagedInventoryRow{
			Id:              r.ID.String(),
			ProductId:       r.ProductID.String(),
			ProductName:     r.ProductName,
			BottleSizeMl:    r.BottleSizeMl,
			TargetAbvPct:    r.TargetAbvPct,
			LotCode:         r.LotCode,
			Jurisdiction:    r.Jurisdiction,
			BottlesOnHand:   r.BottlesOnHand,
			BottlesPackaged: r.BottlesPackaged,
			BottlesRemoved:  r.BottlesRemoved,
			UpdatedAt:       timestamppb.New(r.UpdatedAt.Time),
			ReleaseNotes:    r.ReleaseNotes,
			ReleasedByName:  r.ReleasedByName,
			HoldReason:      r.HoldReason,
			HeldByName:      r.HeldByName,
		}
		if r.FirstBottledDate.Valid {
			row.FirstBottledDate = formatDate(r.FirstBottledDate)
		}
		if r.BottlingRunID.Valid {
			row.BottlingRunId = r.BottlingRunID.UUID.String()
		}
		if r.ReleasedAt.Valid {
			row.ReleasedAt = timestamppb.New(r.ReleasedAt.Time)
		}
		if r.HeldAt.Valid {
			row.HeldAt = timestamppb.New(r.HeldAt.Time)
		}
		out = append(out, row)
	}
	return connect.NewResponse(&stillhousev1.ListPackagedInventoryResponse{Rows: out}), nil
}

// --- converters ---

func bottlingRunToProto(r sqlcgen.BottlingRun, p sqlcgen.Product, _ any) *stillhousev1.BottlingRun {
	out := &stillhousev1.BottlingRun{
		Id:                      r.ID.String(),
		TenantId:                r.TenantID.String(),
		RunNo:                   r.RunNo,
		ProductId:               r.ProductID.String(),
		ProductName:             p.Name,
		ProductBottleSizeMl:     p.BottleSizeMl,
		ProductTargetAbvPct:     p.TargetAbvPct,
		SourceContainerId:       r.SourceContainerID.String(),
		DestinationJurisdiction: r.DestinationJurisdiction,
		BottlingDate:            formatDate(r.BottlingDate),
		BottleCount:             r.BottleCount,
		BottlingLossL:           r.BottlingLossL,
		LotCode:                 r.LotCode,
		TankGaugeVolumeL:        r.TankGaugeVolumeL,
		TankGaugeAbvPct:         r.TankGaugeAbvPct,
		TankGaugeLaa:            r.TankGaugeLaa,
		BulkMovementId:          r.BulkMovementID.String(),
		Notes:                   r.Notes,
		CreatedAt:               timestamppb.New(r.CreatedAt.Time),
		UpdatedAt:               timestamppb.New(r.UpdatedAt.Time),
		VoidedReason:            r.VoidedReason,
		DutyRateSource:          r.DutyRateSource,
	}
	// A NULL duty amount means this run was not a duty event — an
	// at-removal tenant, or a run before the tenant's duty-point cutover —
	// which is different from a run dutied at zero. The bool carries that
	// distinction across the wire, where a bare 0.0 could not.
	if r.DutyAmountCad.Valid {
		out.DutyPaidAtPackaging = true
		out.DutyAmountCad = r.DutyAmountCad.Float64
	}
	if r.DutyRatePerLaa.Valid {
		out.DutyRatePerLaa = r.DutyRatePerLaa.Float64
	}
	if r.VoidedAt.Valid {
		out.VoidedAt = timestamppb.New(r.VoidedAt.Time)
	}
	if r.VoidedBy.Valid {
		out.VoidedBy = r.VoidedBy.UUID.String()
	}
	return out
}

func bottlingRunStampUsageToProto(u sqlcgen.BottlingRunStampUsage, jurisdiction string) *stillhousev1.BottlingRunStampUsage {
	return &stillhousev1.BottlingRunStampUsage{
		Id:            u.ID.String(),
		BottlingRunId: u.BottlingRunID.String(),
		StampOrderId:  u.StampOrderID.String(),
		Jurisdiction:  jurisdiction,
		BottleCount:   u.BottleCount,
		SerialStart:   u.SerialStart,
		SerialEnd:     u.SerialEnd,
		Voids:         u.Voids,
		CreatedAt:     timestamppb.New(u.CreatedAt.Time),
	}
}

func bottlingRunStampUsageRowToProto(r sqlcgen.ListBottlingRunStampUsageRow) *stillhousev1.BottlingRunStampUsage {
	return &stillhousev1.BottlingRunStampUsage{
		Id:            r.ID.String(),
		BottlingRunId: r.BottlingRunID.String(),
		StampOrderId:  r.StampOrderID.String(),
		Jurisdiction:  r.Jurisdiction,
		BottleCount:   r.BottleCount,
		SerialStart:   r.SerialStart,
		SerialEnd:     r.SerialEnd,
		Voids:         r.Voids,
		CreatedAt:     timestamppb.New(r.CreatedAt.Time),
	}
}

func packagedInventoryToProto(p sqlcgen.PackagedInventory, product sqlcgen.Product) *stillhousev1.PackagedInventoryRow {
	return &stillhousev1.PackagedInventoryRow{
		Id:              p.ID.String(),
		ProductId:       p.ProductID.String(),
		ProductName:     product.Name,
		BottleSizeMl:    product.BottleSizeMl,
		TargetAbvPct:    product.TargetAbvPct,
		LotCode:         p.LotCode,
		Jurisdiction:    p.Jurisdiction,
		BottlesOnHand:   p.BottlesOnHand,
		BottlesPackaged: p.BottlesPackaged,
		BottlesRemoved:  p.BottlesRemoved,
		UpdatedAt:       timestamppb.New(p.UpdatedAt.Time),
	}
}
