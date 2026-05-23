package rpc

import (
	"context"
	"errors"
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

type MaterialService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewMaterialService(db *tenantdb.DB, logger *slog.Logger) *MaterialService {
	return &MaterialService{db: db, logger: logger}
}

func (s *MaterialService) CreateMaterial(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreateMaterialRequest],
) (*connect.Response[stillhousev1.CreateMaterialResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	if in.GetName() == "" || in.GetUom() == "" || in.GetKind() == stillhousev1.MaterialKind_MATERIAL_KIND_UNSPECIFIED {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name, kind, and uom are required"))
	}
	kind, err := materialKindToDB(in.GetKind())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var created sqlcgen.Material
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var dbErr error
		created, dbErr = q.CreateMaterial(ctx, sqlcgen.CreateMaterialParams{
			TenantID:    u.TenantID,
			Name:        in.GetName(),
			Kind:        kind,
			Uom:         in.GetUom(),
			Supplier:    in.GetSupplier(),
			Notes:       in.GetNotes(),
			ExtractPct:  optionalFloat(in.GetExtractPctSet(), in.GetExtractPct()),
			MoisturePct: optionalFloat(in.GetMoisturePctSet(), in.GetMoisturePct()),
		})
		if dbErr != nil {
			return dbErr
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "material", created.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"name": created.Name, "kind": string(created.Kind), "uom": created.Uom,
				"supplier": created.Supplier,
			})
	})
	if err != nil {
		s.logger.Error("CreateMaterial", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.CreateMaterialResponse{Material: materialToProto(created)}), nil
}

func (s *MaterialService) UpdateMaterial(
	ctx context.Context,
	req *connect.Request[stillhousev1.UpdateMaterialRequest],
) (*connect.Response[stillhousev1.UpdateMaterialResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	id, err := uuid.Parse(in.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	if in.GetName() == "" || in.GetUom() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name and uom are required"))
	}

	var updated sqlcgen.Material
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var dbErr error
		updated, dbErr = q.UpdateMaterial(ctx, sqlcgen.UpdateMaterialParams{
			ID:          id,
			Name:        in.GetName(),
			Uom:         in.GetUom(),
			Supplier:    in.GetSupplier(),
			Notes:       in.GetNotes(),
			ExtractPct:  optionalFloat(in.GetExtractPctSet(), in.GetExtractPct()),
			MoisturePct: optionalFloat(in.GetMoisturePctSet(), in.GetMoisturePct()),
		})
		return dbErr
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("material not found"))
		}
		s.logger.Error("UpdateMaterial", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.UpdateMaterialResponse{Material: materialToProto(updated)}), nil
}

func (s *MaterialService) GetMaterial(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetMaterialRequest],
) (*connect.Response[stillhousev1.GetMaterialResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}

	var m sqlcgen.Material
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var dbErr error
		m, dbErr = q.GetMaterial(ctx, id)
		return dbErr
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("material not found"))
		}
		s.logger.Error("GetMaterial", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.GetMaterialResponse{Material: materialToProto(m)}), nil
}

func (s *MaterialService) ListMaterials(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListMaterialsRequest],
) (*connect.Response[stillhousev1.ListMaterialsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	params := sqlcgen.ListMaterialsParams{
		IncludeArchived: req.Msg.GetIncludeArchived(),
	}
	if req.Msg.GetKind() != stillhousev1.MaterialKind_MATERIAL_KIND_UNSPECIFIED {
		kind, err := materialKindToDB(req.Msg.GetKind())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		params.Kind = sqlcgen.NullMaterialKind{MaterialKind: kind, Valid: true}
	}

	var rows []sqlcgen.Material
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var dbErr error
		rows, dbErr = q.ListMaterials(ctx, params)
		return dbErr
	})
	if err != nil {
		s.logger.Error("ListMaterials", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.Material, 0, len(rows))
	for _, m := range rows {
		out = append(out, materialToProto(m))
	}
	return connect.NewResponse(&stillhousev1.ListMaterialsResponse{Materials: out}), nil
}

func (s *MaterialService) ArchiveMaterial(
	ctx context.Context,
	req *connect.Request[stillhousev1.ArchiveMaterialRequest],
) (*connect.Response[stillhousev1.ArchiveMaterialResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}

	var m sqlcgen.Material
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var dbErr error
		if req.Msg.GetArchived() {
			m, dbErr = q.ArchiveMaterial(ctx, id)
		} else {
			m, dbErr = q.UnarchiveMaterial(ctx, id)
		}
		return dbErr
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("material not found"))
		}
		s.logger.Error("ArchiveMaterial", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.ArchiveMaterialResponse{Material: materialToProto(m)}), nil
}

func (s *MaterialService) RecordMaterialReceipt(
	ctx context.Context,
	req *connect.Request[stillhousev1.RecordMaterialReceiptRequest],
) (*connect.Response[stillhousev1.RecordMaterialReceiptResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	matID, err := uuid.Parse(in.GetMaterialId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid material_id"))
	}
	if in.GetQuantityReceived() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("quantity_received must be > 0"))
	}

	received := timestampOrNow(in.GetReceivedAt())

	var lot sqlcgen.MaterialLot
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		// Ensure the material exists (RLS-scoped lookup).
		if _, e := q.GetMaterial(ctx, matID); e != nil {
			return e
		}
		var dbErr error
		lot, dbErr = q.CreateMaterialLot(ctx, sqlcgen.CreateMaterialLotParams{
			TenantID:         u.TenantID,
			MaterialID:       matID,
			SupplierLot:      in.GetSupplierLot(),
			QuantityReceived: in.GetQuantityReceived(),
			ReceivedAt:       received,
			Notes:            in.GetNotes(),
			UnitCostCad:      optionalFloat(in.GetUnitCostCadSet(), in.GetUnitCostCad()),
		})
		if dbErr != nil {
			return dbErr
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "material_lot", lot.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"material_id":       matID.String(),
				"supplier_lot":      lot.SupplierLot,
				"quantity_received": lot.QuantityReceived,
				"unit_cost_cad":     in.GetUnitCostCad(),
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("material not found"))
		}
		s.logger.Error("RecordMaterialReceipt", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.RecordMaterialReceiptResponse{Lot: materialLotToProto(lot)}), nil
}

func (s *MaterialService) ListMaterialLots(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListMaterialLotsRequest],
) (*connect.Response[stillhousev1.ListMaterialLotsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	params := sqlcgen.ListMaterialLotsParams{
		OnHandOnly: req.Msg.GetOnHandOnly(),
	}
	if id := req.Msg.GetMaterialId(); id != "" {
		matID, err := uuid.Parse(id)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid material_id"))
		}
		params.MaterialID = uuid.NullUUID{UUID: matID, Valid: true}
	}

	var rows []sqlcgen.MaterialLot
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var dbErr error
		rows, dbErr = q.ListMaterialLots(ctx, params)
		return dbErr
	})
	if err != nil {
		s.logger.Error("ListMaterialLots", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.MaterialLot, 0, len(rows))
	for _, l := range rows {
		out = append(out, materialLotToProto(l))
	}
	return connect.NewResponse(&stillhousev1.ListMaterialLotsResponse{Lots: out}), nil
}

// BottlingRunCost walks back from a bottling run to every mash that fed its
// source container and sums material costs. Uses the same chain-walk as
// TraceabilityService.TraceBottlingRun (intentionally duplicated rather than
// shared — cost has different semantics around missing prices and we want
// the traceability output to stay generic).
//
// Limitations:
//   - Only counts ingredients whose mash entry has a material_lot_id; lot-less
//     ingredients can't be priced and are dropped.
//   - Lines without a recorded unit_cost_cad surface as line_cost_cad = 0 with
//     unit_cost_cad = 0 so the UI can flag missing-price rows.
//   - Allocates total mash material cost evenly across the bottling run.
//     A single mash feeding multiple bottling runs (split-bottle case) over-
//     counts in the first run and under-counts in the second; v2 should track
//     bottled fraction.
func (s *MaterialService) BottlingRunCost(
	ctx context.Context,
	req *connect.Request[stillhousev1.BottlingRunCostRequest],
) (*connect.Response[stillhousev1.BottlingRunCostResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	runID, err := uuid.Parse(req.Msg.GetBottlingRunId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid bottling_run_id"))
	}

	out := &stillhousev1.BottlingRunCostResponse{BottlingRunId: runID.String()}
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		run, e := q.GetBottlingRun(ctx, runID)
		if e != nil {
			return e
		}
		out.BottleCount = run.BottleCount

		feedCutoff := run.BottlingDate.Time.Add(24 * time.Hour)
		feeds, e := q.BottlingRunChainFeeds(ctx, sqlcgen.BottlingRunChainFeedsParams{
			DestinationContainerID: uuid.NullUUID{UUID: run.SourceContainerID, Valid: true},
			OccurredAt:             pgtype.Timestamptz{Time: feedCutoff, Valid: true},
		})
		if e != nil {
			return e
		}

		// Dedupe mashes across the (potentially multi-feed) chain so a mash
		// feeding twice doesn't double-count.
		seen := make(map[string]bool)
		for _, fd := range feeds {
			if fd.Reason != sqlcgen.BulkMovementReasonProductionGauge {
				continue
			}
			charges, ce := q.DistillationChainFromGauge(ctx, fd.ID)
			if errors.Is(ce, pgx.ErrNoRows) || len(charges) == 0 {
				continue
			}
			if ce != nil {
				return ce
			}
			for _, ch := range charges {
				if !ch.MashRunID.Valid || seen[ch.MashRunID.UUID.String()] {
					continue
				}
				seen[ch.MashRunID.UUID.String()] = true

				ings, ie := q.ListMashIngredients(ctx, ch.MashRunID.UUID)
				if ie != nil {
					return ie
				}
				for _, ing := range ings {
					if !ing.MaterialLotID.Valid {
						continue
					}
					lot, le := q.GetMaterialLot(ctx, ing.MaterialLotID.UUID)
					if le != nil {
						return le
					}
					line := &stillhousev1.BottlingRunCostLine{
						MaterialName: ing.MaterialName,
						SupplierLot:  ing.SupplierLot.String,
						QuantityUsed: ing.QuantityUsed,
						Uom:          ing.Uom,
					}
					if lot.UnitCostCad.Valid {
						line.UnitCostCad = lot.UnitCostCad.Float64
						line.LineCostCad = ing.QuantityUsed * lot.UnitCostCad.Float64
						out.TotalMaterialCostCad += line.LineCostCad
					}
					out.Lines = append(out.Lines, line)
				}
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("bottling run not found"))
		}
		s.logger.Error("BottlingRunCost", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	if out.BottleCount > 0 {
		out.MaterialCostPerBottleCad = out.TotalMaterialCostCad / float64(out.BottleCount)
	}
	return connect.NewResponse(out), nil
}

// ProductCostSummary aggregates BottlingRunCost across every non-voided
// bottling run for a product. Naive N+1 implementation — fine for most
// craft distilleries (rare to have more than a few dozen runs per SKU);
// when a tenant grows past that, the right fix is a denormalized
// per-run cost row updated at bottling time. Intentionally not cached.
func (s *MaterialService) ProductCostSummary(
	ctx context.Context,
	req *connect.Request[stillhousev1.ProductCostSummaryRequest],
) (*connect.Response[stillhousev1.ProductCostSummaryResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	productID, err := uuid.Parse(req.Msg.GetProductId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid product_id"))
	}

	out := &stillhousev1.ProductCostSummaryResponse{ProductId: productID.String()}
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		runs, e := q.ListBottlingRunsForProduct(ctx, productID)
		if e != nil {
			return e
		}
		for _, r := range runs {
			out.RunCount++
			out.TotalBottles += r.BottleCount
			cost, missing, ce := computeRunMaterialCost(ctx, q, r.SourceContainerID, r.BottlingDate)
			if ce != nil {
				return ce
			}
			if missing {
				out.RunsWithMissingPrices++
			}
			out.TotalMaterialCostCad += cost
		}
		return nil
	})
	if err != nil {
		s.logger.Error("ProductCostSummary", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	if out.TotalBottles > 0 {
		out.AverageMaterialCostPerBottleCad = out.TotalMaterialCostCad / float64(out.TotalBottles)
	}
	return connect.NewResponse(out), nil
}

// computeRunMaterialCost is the chain walk shared by BottlingRunCost and
// ProductCostSummary. Returns (cost, anyMissingPrice, err).
func computeRunMaterialCost(
	ctx context.Context,
	q *sqlcgen.Queries,
	sourceContainerID uuid.UUID,
	bottlingDate pgtype.Date,
) (float64, bool, error) {
	feedCutoff := bottlingDate.Time.Add(24 * time.Hour)
	feeds, e := q.BottlingRunChainFeeds(ctx, sqlcgen.BottlingRunChainFeedsParams{
		DestinationContainerID: uuid.NullUUID{UUID: sourceContainerID, Valid: true},
		OccurredAt:             pgtype.Timestamptz{Time: feedCutoff, Valid: true},
	})
	if e != nil {
		return 0, false, e
	}
	var (
		total   float64
		missing bool
		seen    = make(map[string]bool)
	)
	for _, fd := range feeds {
		if fd.Reason != sqlcgen.BulkMovementReasonProductionGauge {
			continue
		}
		charges, ce := q.DistillationChainFromGauge(ctx, fd.ID)
		if errors.Is(ce, pgx.ErrNoRows) || len(charges) == 0 {
			continue
		}
		if ce != nil {
			return 0, false, ce
		}
		for _, ch := range charges {
			if !ch.MashRunID.Valid || seen[ch.MashRunID.UUID.String()] {
				continue
			}
			seen[ch.MashRunID.UUID.String()] = true
			ings, ie := q.ListMashIngredients(ctx, ch.MashRunID.UUID)
			if ie != nil {
				return 0, false, ie
			}
			for _, ing := range ings {
				if !ing.MaterialLotID.Valid {
					continue
				}
				lot, le := q.GetMaterialLot(ctx, ing.MaterialLotID.UUID)
				if le != nil {
					return 0, false, le
				}
				if !lot.UnitCostCad.Valid {
					missing = true
					continue
				}
				total += ing.QuantityUsed * lot.UnitCostCad.Float64
			}
		}
	}
	return total, missing, nil
}

// --- helpers ---

func optionalFloat(set bool, v float64) pgtype.Float8 {
	if !set {
		return pgtype.Float8{Valid: false}
	}
	return pgtype.Float8{Float64: v, Valid: true}
}

func materialToProto(m sqlcgen.Material) *stillhousev1.Material {
	out := &stillhousev1.Material{
		Id:        m.ID.String(),
		TenantId:  m.TenantID.String(),
		Name:      m.Name,
		Kind:      materialKindToProto(m.Kind),
		Uom:       m.Uom,
		Supplier:  m.Supplier,
		Notes:     m.Notes,
		Archived:  m.Archived,
		CreatedAt: timestamppb.New(m.CreatedAt.Time),
		UpdatedAt: timestamppb.New(m.UpdatedAt.Time),
	}
	if m.ExtractPct.Valid {
		out.ExtractPct = m.ExtractPct.Float64
		out.ExtractPctSet = true
	}
	if m.MoisturePct.Valid {
		out.MoisturePct = m.MoisturePct.Float64
		out.MoisturePctSet = true
	}
	return out
}

func materialLotToProto(l sqlcgen.MaterialLot) *stillhousev1.MaterialLot {
	out := &stillhousev1.MaterialLot{
		Id:               l.ID.String(),
		TenantId:         l.TenantID.String(),
		MaterialId:       l.MaterialID.String(),
		SupplierLot:      l.SupplierLot,
		QuantityReceived: l.QuantityReceived,
		QuantityOnHand:   l.QuantityOnHand,
		ReceivedAt:       timestamppb.New(l.ReceivedAt.Time),
		Notes:            l.Notes,
		CreatedAt:        timestamppb.New(l.CreatedAt.Time),
		UpdatedAt:        timestamppb.New(l.UpdatedAt.Time),
	}
	if l.UnitCostCad.Valid {
		out.UnitCostCad = l.UnitCostCad.Float64
		out.UnitCostCadSet = true
	}
	return out
}

func materialKindToDB(k stillhousev1.MaterialKind) (sqlcgen.MaterialKind, error) {
	switch k {
	case stillhousev1.MaterialKind_MATERIAL_KIND_GRAIN:
		return sqlcgen.MaterialKindGrain, nil
	case stillhousev1.MaterialKind_MATERIAL_KIND_MALT:
		return sqlcgen.MaterialKindMalt, nil
	case stillhousev1.MaterialKind_MATERIAL_KIND_YEAST:
		return sqlcgen.MaterialKindYeast, nil
	case stillhousev1.MaterialKind_MATERIAL_KIND_WATER:
		return sqlcgen.MaterialKindWater, nil
	case stillhousev1.MaterialKind_MATERIAL_KIND_NGS:
		return sqlcgen.MaterialKindNgs, nil
	case stillhousev1.MaterialKind_MATERIAL_KIND_BOTANICAL:
		return sqlcgen.MaterialKindBotanical, nil
	case stillhousev1.MaterialKind_MATERIAL_KIND_PACKAGING:
		return sqlcgen.MaterialKindPackaging, nil
	case stillhousev1.MaterialKind_MATERIAL_KIND_OTHER:
		return sqlcgen.MaterialKindOther, nil
	}
	return "", errors.New("invalid material kind")
}

func materialKindToProto(k sqlcgen.MaterialKind) stillhousev1.MaterialKind {
	switch k {
	case sqlcgen.MaterialKindGrain:
		return stillhousev1.MaterialKind_MATERIAL_KIND_GRAIN
	case sqlcgen.MaterialKindMalt:
		return stillhousev1.MaterialKind_MATERIAL_KIND_MALT
	case sqlcgen.MaterialKindYeast:
		return stillhousev1.MaterialKind_MATERIAL_KIND_YEAST
	case sqlcgen.MaterialKindWater:
		return stillhousev1.MaterialKind_MATERIAL_KIND_WATER
	case sqlcgen.MaterialKindNgs:
		return stillhousev1.MaterialKind_MATERIAL_KIND_NGS
	case sqlcgen.MaterialKindBotanical:
		return stillhousev1.MaterialKind_MATERIAL_KIND_BOTANICAL
	case sqlcgen.MaterialKindPackaging:
		return stillhousev1.MaterialKind_MATERIAL_KIND_PACKAGING
	case sqlcgen.MaterialKindOther:
		return stillhousev1.MaterialKind_MATERIAL_KIND_OTHER
	}
	return stillhousev1.MaterialKind_MATERIAL_KIND_UNSPECIFIED
}
