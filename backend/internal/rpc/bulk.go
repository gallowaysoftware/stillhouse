package rpc

import (
	"context"
	"errors"
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

type BulkService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewBulkService(db *tenantdb.DB, logger *slog.Logger) *BulkService {
	return &BulkService{db: db, logger: logger}
}

func (s *BulkService) CreateBulkContainer(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreateBulkContainerRequest],
) (*connect.Response[stillhousev1.CreateBulkContainerResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	if in.GetName() == "" || in.GetKind() == stillhousev1.BulkContainerKind_BULK_CONTAINER_KIND_UNSPECIFIED {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name and kind are required"))
	}
	kind, err := bulkContainerKindToDB(in.GetKind())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var c sqlcgen.BulkContainer
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		c, e = q.CreateBulkContainer(ctx, sqlcgen.CreateBulkContainerParams{
			TenantID:  u.TenantID,
			Name:      in.GetName(),
			Kind:      kind,
			CapacityL: optionalFloat(in.GetCapacityLSet(), in.GetCapacityL()),
			Location:  in.GetLocation(),
			Notes:     in.GetNotes(),
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "bulk_container", c.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"name":     c.Name,
				"kind":     string(kind),
				"location": c.Location,
			})
	})
	if err != nil {
		s.logger.Error("CreateBulkContainer", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.CreateBulkContainerResponse{Container: bulkContainerToProto(c)}), nil
}

func (s *BulkService) UpdateBulkContainer(
	ctx context.Context,
	req *connect.Request[stillhousev1.UpdateBulkContainerRequest],
) (*connect.Response[stillhousev1.UpdateBulkContainerResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	kind, err := bulkContainerKindToDB(req.Msg.GetKind())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	var c sqlcgen.BulkContainer
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		c, e = q.UpdateBulkContainer(ctx, sqlcgen.UpdateBulkContainerParams{
			ID:        id,
			Name:      req.Msg.GetName(),
			Kind:      kind,
			CapacityL: optionalFloat(req.Msg.GetCapacityLSet(), req.Msg.GetCapacityL()),
			Location:  req.Msg.GetLocation(),
			Notes:     req.Msg.GetNotes(),
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "bulk_container", c.ID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"name":     c.Name,
				"location": c.Location,
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("container not found"))
		}
		s.logger.Error("UpdateBulkContainer", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.UpdateBulkContainerResponse{Container: bulkContainerToProto(c)}), nil
}

func (s *BulkService) SetBulkContainerArchived(
	ctx context.Context,
	req *connect.Request[stillhousev1.SetBulkContainerArchivedRequest],
) (*connect.Response[stillhousev1.SetBulkContainerArchivedResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	var c sqlcgen.BulkContainer
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		c, e = q.SetBulkContainerArchived(ctx, sqlcgen.SetBulkContainerArchivedParams{ID: id, Archived: req.Msg.GetArchived()})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "bulk_container", c.ID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"event":    "archived_changed",
				"archived": c.Archived,
			})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("container not found"))
		}
		s.logger.Error("SetBulkContainerArchived", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.SetBulkContainerArchivedResponse{Container: bulkContainerToProto(c)}), nil
}

func (s *BulkService) ListBulkContainers(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListBulkContainersRequest],
) (*connect.Response[stillhousev1.ListBulkContainersResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.BulkContainer
	var totalLAA float64
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListBulkContainers(ctx, req.Msg.GetIncludeArchived())
		if e != nil {
			return e
		}
		totalLAA, e = q.SumBulkLAA(ctx)
		return e
	})
	if err != nil {
		s.logger.Error("ListBulkContainers", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.BulkContainer, 0, len(rows))
	activeCount := int32(0)
	for _, c := range rows {
		out = append(out, bulkContainerToProto(c))
		if !c.Archived {
			activeCount++
		}
	}
	return connect.NewResponse(&stillhousev1.ListBulkContainersResponse{
		Containers: out,
		Summary:    &stillhousev1.BulkSummary{TotalLaa: totalLAA, ContainerCount: activeCount},
	}), nil
}

func (s *BulkService) GetBulkContainer(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetBulkContainerRequest],
) (*connect.Response[stillhousev1.GetBulkContainerResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}

	var (
		c     sqlcgen.BulkContainer
		moves []sqlcgen.ListBulkMovementsByContainerRow
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		c, e = q.GetBulkContainer(ctx, id)
		if e != nil {
			return e
		}
		moves, e = q.ListBulkMovementsByContainer(ctx, uuid.NullUUID{UUID: id, Valid: true})
		return e
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("container not found"))
		}
		s.logger.Error("GetBulkContainer", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := &stillhousev1.GetBulkContainerResponse{
		Container: bulkContainerToProto(c),
		Movements: make([]*stillhousev1.BulkMovement, 0, len(moves)),
	}
	for _, m := range moves {
		out.Movements = append(out.Movements, bulkMovementRowToProto(m))
	}
	return connect.NewResponse(out), nil
}

func (s *BulkService) ListRecentBulkMovements(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListRecentBulkMovementsRequest],
) (*connect.Response[stillhousev1.ListRecentBulkMovementsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	_ = req
	var rows []sqlcgen.ListRecentBulkMovementsRow
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListRecentBulkMovements(ctx)
		return e
	})
	if err != nil {
		s.logger.Error("ListRecentBulkMovements", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := &stillhousev1.ListRecentBulkMovementsResponse{
		Movements: make([]*stillhousev1.BulkMovement, 0, len(rows)),
	}
	for _, r := range rows {
		out.Movements = append(out.Movements, &stillhousev1.BulkMovement{
			Id:                       r.ID.String(),
			SourceContainerId:        nullUUIDString(r.SourceContainerID),
			SourceContainerName:      r.SourceName.String,
			DestinationContainerId:   nullUUIDString(r.DestinationContainerID),
			DestinationContainerName: r.DestinationName.String,
			VolumeL:                  r.VolumeL,
			AbvPct:                   r.AbvPct,
			Laa:                      r.Laa,
			Reason:                   bulkMovementReasonToProto(r.Reason),
			ReferenceType:            r.ReferenceType,
			ReferenceId:              nullUUIDString(r.ReferenceID),
			Notes:                    r.Notes,
			OccurredAt:               timestamppb.New(r.OccurredAt.Time),
			CreatedAt:                timestamppb.New(r.CreatedAt.Time),
		})
	}
	return connect.NewResponse(out), nil
}

// --- helpers + converters ---

func bulkContainerToProto(c sqlcgen.BulkContainer) *stillhousev1.BulkContainer {
	out := &stillhousev1.BulkContainer{
		Id:             c.ID.String(),
		TenantId:       c.TenantID.String(),
		Name:           c.Name,
		Kind:           bulkContainerKindToProto(c.Kind),
		Location:       c.Location,
		Notes:          c.Notes,
		Archived:       c.Archived,
		CurrentVolumeL: c.CurrentVolumeL,
		CurrentLaa:     c.CurrentLaa,
		CreatedAt:      timestamppb.New(c.CreatedAt.Time),
		UpdatedAt:      timestamppb.New(c.UpdatedAt.Time),
	}
	if c.CapacityL.Valid {
		out.CapacityL = c.CapacityL.Float64
		out.CapacityLSet = true
	}
	if c.CurrentAbvPct.Valid {
		out.CurrentAbvPct = c.CurrentAbvPct.Float64
		out.CurrentAbvPctSet = true
	}
	return out
}

func bulkMovementRowToProto(r sqlcgen.ListBulkMovementsByContainerRow) *stillhousev1.BulkMovement {
	return &stillhousev1.BulkMovement{
		Id:                       r.ID.String(),
		SourceContainerId:        nullUUIDString(r.SourceContainerID),
		SourceContainerName:      r.SourceName.String,
		DestinationContainerId:   nullUUIDString(r.DestinationContainerID),
		DestinationContainerName: r.DestinationName.String,
		VolumeL:                  r.VolumeL,
		AbvPct:                   r.AbvPct,
		Laa:                      r.Laa,
		Reason:                   bulkMovementReasonToProto(r.Reason),
		ReferenceType:            r.ReferenceType,
		ReferenceId:              nullUUIDString(r.ReferenceID),
		Notes:                    r.Notes,
		OccurredAt:               timestamppb.New(r.OccurredAt.Time),
		CreatedAt:                timestamppb.New(r.CreatedAt.Time),
	}
}

func bulkMovementToProto(m sqlcgen.BulkMovement) *stillhousev1.BulkMovement {
	return &stillhousev1.BulkMovement{
		Id:                     m.ID.String(),
		SourceContainerId:      nullUUIDString(m.SourceContainerID),
		DestinationContainerId: nullUUIDString(m.DestinationContainerID),
		VolumeL:                m.VolumeL,
		AbvPct:                 m.AbvPct,
		Laa:                    m.Laa,
		Reason:                 bulkMovementReasonToProto(m.Reason),
		ReferenceType:          m.ReferenceType,
		ReferenceId:            nullUUIDString(m.ReferenceID),
		Notes:                  m.Notes,
		OccurredAt:             timestamppb.New(m.OccurredAt.Time),
		CreatedAt:              timestamppb.New(m.CreatedAt.Time),
	}
}

func nullUUIDString(u uuid.NullUUID) string {
	if !u.Valid {
		return ""
	}
	return u.UUID.String()
}

func bulkContainerKindToDB(k stillhousev1.BulkContainerKind) (sqlcgen.BulkContainerKind, error) {
	switch k {
	case stillhousev1.BulkContainerKind_BULK_CONTAINER_KIND_SPIRIT_RECEIVER:
		return sqlcgen.BulkContainerKindSpiritReceiver, nil
	case stillhousev1.BulkContainerKind_BULK_CONTAINER_KIND_TANK:
		return sqlcgen.BulkContainerKindTank, nil
	case stillhousev1.BulkContainerKind_BULK_CONTAINER_KIND_IBC:
		return sqlcgen.BulkContainerKindIbc, nil
	case stillhousev1.BulkContainerKind_BULK_CONTAINER_KIND_TOTE:
		return sqlcgen.BulkContainerKindTote, nil
	case stillhousev1.BulkContainerKind_BULK_CONTAINER_KIND_BLEND_TANK:
		return sqlcgen.BulkContainerKindBlendTank, nil
	case stillhousev1.BulkContainerKind_BULK_CONTAINER_KIND_BOTTLING_TANK:
		return sqlcgen.BulkContainerKindBottlingTank, nil
	case stillhousev1.BulkContainerKind_BULK_CONTAINER_KIND_OTHER:
		return sqlcgen.BulkContainerKindOther, nil
	}
	return "", errors.New("invalid bulk container kind")
}

func bulkContainerKindToProto(k sqlcgen.BulkContainerKind) stillhousev1.BulkContainerKind {
	switch k {
	case sqlcgen.BulkContainerKindSpiritReceiver:
		return stillhousev1.BulkContainerKind_BULK_CONTAINER_KIND_SPIRIT_RECEIVER
	case sqlcgen.BulkContainerKindTank:
		return stillhousev1.BulkContainerKind_BULK_CONTAINER_KIND_TANK
	case sqlcgen.BulkContainerKindIbc:
		return stillhousev1.BulkContainerKind_BULK_CONTAINER_KIND_IBC
	case sqlcgen.BulkContainerKindTote:
		return stillhousev1.BulkContainerKind_BULK_CONTAINER_KIND_TOTE
	case sqlcgen.BulkContainerKindBlendTank:
		return stillhousev1.BulkContainerKind_BULK_CONTAINER_KIND_BLEND_TANK
	case sqlcgen.BulkContainerKindBottlingTank:
		return stillhousev1.BulkContainerKind_BULK_CONTAINER_KIND_BOTTLING_TANK
	case sqlcgen.BulkContainerKindOther:
		return stillhousev1.BulkContainerKind_BULK_CONTAINER_KIND_OTHER
	}
	return stillhousev1.BulkContainerKind_BULK_CONTAINER_KIND_UNSPECIFIED
}

func bulkMovementReasonToProto(r sqlcgen.BulkMovementReason) stillhousev1.BulkMovementReason {
	switch r {
	case sqlcgen.BulkMovementReasonProductionGauge:
		return stillhousev1.BulkMovementReason_BULK_MOVEMENT_REASON_PRODUCTION_GAUGE
	case sqlcgen.BulkMovementReasonInterTankTransfer:
		return stillhousev1.BulkMovementReason_BULK_MOVEMENT_REASON_INTER_TANK_TRANSFER
	case sqlcgen.BulkMovementReasonBlend:
		return stillhousev1.BulkMovementReason_BULK_MOVEMENT_REASON_BLEND
	case sqlcgen.BulkMovementReasonTransferInBond:
		return stillhousev1.BulkMovementReason_BULK_MOVEMENT_REASON_TRANSFER_IN_BOND
	case sqlcgen.BulkMovementReasonTransferOutInBond:
		return stillhousev1.BulkMovementReason_BULK_MOVEMENT_REASON_TRANSFER_OUT_IN_BOND
	case sqlcgen.BulkMovementReasonTransferToPackaging:
		return stillhousev1.BulkMovementReason_BULK_MOVEMENT_REASON_TRANSFER_TO_PACKAGING
	case sqlcgen.BulkMovementReasonLossEvaporation:
		return stillhousev1.BulkMovementReason_BULK_MOVEMENT_REASON_LOSS_EVAPORATION
	case sqlcgen.BulkMovementReasonLossUnaccounted:
		return stillhousev1.BulkMovementReason_BULK_MOVEMENT_REASON_LOSS_UNACCOUNTED
	case sqlcgen.BulkMovementReasonRegaugeCorrection:
		return stillhousev1.BulkMovementReason_BULK_MOVEMENT_REASON_REGAUGE_CORRECTION
	case sqlcgen.BulkMovementReasonDestruction:
		return stillhousev1.BulkMovementReason_BULK_MOVEMENT_REASON_DESTRUCTION
	}
	return stillhousev1.BulkMovementReason_BULK_MOVEMENT_REASON_UNSPECIFIED
}

// applyDeposit returns the new (volume, abv, laa) after depositing
// (volume, abv) into a container that currently holds (curVol, curABV).
// curABV is ignored when curVol == 0.
func applyDeposit(curVol float64, curABV pgtype.Float8, addVol, addABV float64) (newVol float64, newABV pgtype.Float8, newLAA float64) {
	if curVol <= 0 {
		newVol = addVol
		newABV = pgtype.Float8{Float64: addABV, Valid: true}
	} else {
		newVol = curVol + addVol
		mass := curVol*curABV.Float64 + addVol*addABV
		if newVol == 0 {
			newABV = pgtype.Float8{Valid: false}
		} else {
			newABV = pgtype.Float8{Float64: mass / newVol, Valid: true}
		}
	}
	if newABV.Valid {
		newLAA = newVol * newABV.Float64 / 100
	}
	return
}

// reference to keep linter quiet in v1; will be used by Stage 5/6 helpers.
var _ = bulkMovementToProto
