package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

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
		product, e := q.GetProduct(ctx, productID)
		if e != nil {
			return e
		}
		if product.Archived {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("product is archived"))
		}
		source, e := q.GetBulkContainer(ctx, sourceID)
		if e != nil {
			return e
		}
		if !source.CurrentAbvPct.Valid {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("source container is empty"))
		}

		requiredVolume := float64(in.GetBottleCount())*float64(product.BottleSizeMl)/1000 + in.GetBottlingLossL()
		if source.CurrentVolumeL+1e-6 < requiredVolume {
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("source has %.4f L on hand but bottling needs %.4f L", source.CurrentVolumeL, requiredVolume))
		}

		abv := source.CurrentAbvPct.Float64
		laa := requiredVolume * abv / 100

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

		// 6. Insert bottling_run.
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
				TenantID:       u.TenantID,
				BottlingRunID:  run.ID,
				StampOrderID:   orderRow.ID,
				BottleCount:    take,
				SerialStart:    serialStart,
				SerialEnd:      serialEnd,
				Voids:          0,
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
			TenantID:        u.TenantID,
			ProductID:       productID,
			LotCode:         in.GetLotCode(),
			Jurisdiction:    in.GetDestinationJurisdiction(),
			BottlingRunID:   uuid.NullUUID{UUID: run.ID, Valid: true},
			BottlesOnHand:   in.GetBottleCount(),
		})
		if e != nil {
			return e
		}
		productOut = product

		// 9. Audit log.
		if e := audit.Write(ctx, q, u.TenantID, u.ID, "bottling_run", run.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"run_no":        run.RunNo,
				"product_id":    productID.String(),
				"product_name":  product.Name,
				"jurisdiction":  in.GetDestinationJurisdiction(),
				"bottle_count":  in.GetBottleCount(),
				"lot_code":      in.GetLotCode(),
				"tank_laa":      laa,
				"source_id":     sourceID.String(),
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
	_ = req
	var rows []sqlcgen.ListBottlingRunsRow
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListBottlingRuns(ctx)
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
		}
		out = append(out, bottlingRunToProto(run, sqlcgen.Product{
			Name:         r.ProductName,
			BottleSizeMl: r.ProductBottleSizeMl,
			TargetAbvPct: r.ProductTargetAbvPct,
		}, nil))
	}
	return connect.NewResponse(&stillhousev1.ListBottlingRunsResponse{Runs: out}), nil
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
		out = append(out, &stillhousev1.PackagedInventoryRow{
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
		})
	}
	return connect.NewResponse(&stillhousev1.ListPackagedInventoryResponse{Rows: out}), nil
}

// --- converters ---

func bottlingRunToProto(r sqlcgen.BottlingRun, p sqlcgen.Product, _ any) *stillhousev1.BottlingRun {
	return &stillhousev1.BottlingRun{
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
	}
}

func bottlingRunStampUsageToProto(u sqlcgen.BottlingRunStampUsage, jurisdiction string) *stillhousev1.BottlingRunStampUsage {
	return &stillhousev1.BottlingRunStampUsage{
		Id:             u.ID.String(),
		BottlingRunId:  u.BottlingRunID.String(),
		StampOrderId:   u.StampOrderID.String(),
		Jurisdiction:   jurisdiction,
		BottleCount:    u.BottleCount,
		SerialStart:    u.SerialStart,
		SerialEnd:      u.SerialEnd,
		Voids:          u.Voids,
		CreatedAt:      timestamppb.New(u.CreatedAt.Time),
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
