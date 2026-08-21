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
	"github.com/gallowaysoftware/stillhouse/backend/internal/excise"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type RemovalService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewRemovalService(db *tenantdb.DB, logger *slog.Logger) *RemovalService {
	return &RemovalService{db: db, logger: logger}
}

func (s *RemovalService) CreateRemoval(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreateRemovalRequest],
) (*connect.Response[stillhousev1.CreateRemovalResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	piID, err := uuid.Parse(in.GetPackagedInventoryId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid packaged_inventory_id"))
	}
	if in.GetBottlesRemoved() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("bottles_removed must be > 0"))
	}
	dest, err := removalDestinationKindToDB(in.GetDestinationKind())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	removalDate, err := parseDateOrToday(in.GetRemovalDate())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var (
		removal sqlcgen.PackagingRemoval
		product sqlcgen.Product
		pkg     sqlcgen.PackagedInventory
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := assertDateNotInLockedPeriod(ctx, q, removalDate); e != nil {
			return e
		}
		// Read the lot with the intent to change it. FOR UPDATE is what
		// makes the stock check below mean anything: without it two
		// removals against the same lot both read the same on-hand count,
		// both decide there are enough bottles, and both decrement. The
		// table CHECK caught the resulting negative and turned it into an
		// opaque error, which is the same lost update fixed for bulk
		// containers in stage 131 — two people on two tablets is the
		// ordinary case at any distillery with staff.
		//
		// It also replaces a full ListPackagedInventory scan that pulled
		// every lot in the tenant to find one row, and which could not be
		// locked because of its LEFT JOIN.
		matched, e := q.GetPackagedInventoryForUpdate(ctx, piID)
		if e != nil {
			return e
		}
		if matched.BottlesOnHand < in.GetBottlesRemoved() {
			return connect.NewError(connect.CodeFailedPrecondition,
				fmt.Errorf("only %d bottles on hand", matched.BottlesOnHand))
		}
		product, e = q.GetProduct(ctx, matched.ProductID)
		if e != nil {
			return e
		}

		totalLitres := float64(in.GetBottlesRemoved()) * float64(product.BottleSizeMl) / 1000
		totalLAA := totalLitres * product.TargetAbvPct / 100
		// The rate is the one in force on the removal date, not today's.
		// A date the table cannot source refuses rather than being priced
		// at whatever the current band happens to be — see internal/excise.
		var ratePerLAA, dutyCAD float64
		ratePerLAA, dutyCAD, e = excise.Owed(removalDate.Time, totalLitres, product.TargetAbvPct)
		if e != nil {
			var unknown *excise.UnknownRateError
			if errors.As(e, &unknown) {
				return connect.NewError(connect.CodeFailedPrecondition, e)
			}
			return e
		}

		// Serialise number allocation before reading the maximum — see
		// LockDocumentSequence. Without it two removals started at the
		// same moment claim the same removal_no and one dies on the
		// UNIQUE constraint with the shipment unrecorded.
		if e := q.LockDocumentSequence(ctx, "packaging_removals"); e != nil {
			return e
		}
		nextNo, e := q.NextRemovalNo(ctx)
		if e != nil {
			return e
		}
		removal, e = q.CreateRemoval(ctx, sqlcgen.CreateRemovalParams{
			TenantID:            u.TenantID,
			RemovalNo:           nextNo,
			PackagedInventoryID: piID,
			RemovalDate:         removalDate,
			BottlesRemoved:      in.GetBottlesRemoved(),
			DestinationKind:     dest,
			DestinationName:     in.GetDestinationName(),
			Reference:           in.GetReference(),
			BottleSizeMl:        product.BottleSizeMl,
			BottleAbvPct:        product.TargetAbvPct,
			TotalLitres:         totalLitres,
			TotalLaa:            totalLAA,
			DutyRatePerLaa:      ratePerLAA,
			DutyAmountCad:       dutyCAD,
			Notes:               in.GetNotes(),
		})
		if e != nil {
			return e
		}
		pkg, e = q.DecrementPackagedOnHand(ctx, sqlcgen.DecrementPackagedOnHandParams{
			ID:            piID,
			BottlesOnHand: in.GetBottlesRemoved(),
		})
		if errors.Is(e, pgx.ErrNoRows) {
			// Unreachable while the lock above is held — kept so that a
			// future caller that forgets the lock fails with a sentence an
			// operator can act on rather than a 500.
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("those bottles are no longer on hand — someone else removed them; reload and try again"))
		}
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "removal", removal.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"removal_no":   removal.RemovalNo,
				"product_name": product.Name,
				"lot_code":     pkg.LotCode,
				"jurisdiction": pkg.Jurisdiction,
				"bottles":      removal.BottlesRemoved,
				"total_laa":    removal.TotalLaa,
				"duty_cad":     removal.DutyAmountCad,
				"destination":  removal.DestinationName,
			})
	})
	if err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("packaged inventory not found"))
		}
		s.logger.Error("CreateRemoval", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	out := packagingRemovalToProto(removal, product, pkg.LotCode, pkg.Jurisdiction)
	return connect.NewResponse(&stillhousev1.CreateRemovalResponse{Removal: out}), nil
}

func (s *RemovalService) VoidRemoval(
	ctx context.Context,
	req *connect.Request[stillhousev1.VoidRemovalRequest],
) (*connect.Response[stillhousev1.VoidRemovalResponse], error) {
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
		voided  sqlcgen.PackagingRemoval
		pkg     sqlcgen.PackagedInventory
		product sqlcgen.Product
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		existing, e := q.GetRemoval(ctx, id)
		if e != nil {
			return e
		}
		if existing.VoidedAt.Valid {
			return connect.NewError(connect.CodeFailedPrecondition, errors.New("removal is already voided"))
		}
		if e := assertDateNotInLockedPeriod(ctx, q, existing.RemovalDate); e != nil {
			return e
		}
		voided, e = q.VoidRemoval(ctx, sqlcgen.VoidRemovalParams{
			ID:           id,
			VoidedBy:     uuid.NullUUID{UUID: u.ID, Valid: true},
			VoidedReason: reason,
		})
		if e != nil {
			return e
		}
		// Refund the bottles to packaged_inventory so the on-hand count and the
		// running bottles_removed counter match physical reality again.
		pkg, e = q.IncrementPackagedOnHand(ctx, sqlcgen.IncrementPackagedOnHandParams{
			ID:            existing.PackagedInventoryID,
			BottlesOnHand: existing.BottlesRemoved,
		})
		if e != nil {
			return e
		}
		product, e = q.GetProduct(ctx, pkg.ProductID)
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "removal", voided.ID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"event":        "voided",
				"removal_no":   voided.RemovalNo,
				"bottles":      voided.BottlesRemoved,
				"refund_to_pi": existing.PackagedInventoryID.String(),
				"reason":       reason,
			})
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("removal not found"))
		}
		s.logger.Error("VoidRemoval", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.VoidRemovalResponse{
		Removal: packagingRemovalToProto(voided, product, pkg.LotCode, pkg.Jurisdiction),
	}), nil
}

func (s *RemovalService) ListRemovals(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListRemovalsRequest],
) (*connect.Response[stillhousev1.ListRemovalsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
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
	var (
		rows  []sqlcgen.ListRemovalsRow
		total int32
	)
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListRemovals(ctx, sqlcgen.ListRemovalsParams{
			PeriodStart: pStart,
			PeriodEnd:   pEnd,
			Limit:       limit,
			Offset:      offset,
		})
		if e != nil {
			return e
		}
		total, e = q.CountRemovals(ctx, sqlcgen.CountRemovalsParams{
			PeriodStart: pStart,
			PeriodEnd:   pEnd,
		})
		return e
	})
	if err != nil {
		s.logger.Error("ListRemovals", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.PackagingRemoval, 0, len(rows))
	for _, r := range rows {
		p := sqlcgen.Product{Name: r.ProductName, BottleSizeMl: r.BottleSizeMl}
		removal := sqlcgen.PackagingRemoval{
			ID:                  r.ID,
			TenantID:            r.TenantID,
			RemovalNo:           r.RemovalNo,
			PackagedInventoryID: r.PackagedInventoryID,
			RemovalDate:         r.RemovalDate,
			BottlesRemoved:      r.BottlesRemoved,
			DestinationKind:     r.DestinationKind,
			DestinationName:     r.DestinationName,
			Reference:           r.Reference,
			BottleSizeMl:        r.BottleSizeMl,
			BottleAbvPct:        r.BottleAbvPct,
			TotalLitres:         r.TotalLitres,
			TotalLaa:            r.TotalLaa,
			DutyRatePerLaa:      r.DutyRatePerLaa,
			DutyAmountCad:       r.DutyAmountCad,
			Notes:               r.Notes,
			CreatedAt:           r.CreatedAt,
			VoidedAt:            r.VoidedAt,
			VoidedBy:            r.VoidedBy,
			VoidedReason:        r.VoidedReason,
		}
		out = append(out, packagingRemovalToProto(removal, p, r.LotCode, r.Jurisdiction))
	}
	return connect.NewResponse(&stillhousev1.ListRemovalsResponse{Removals: out, TotalCount: total}), nil
}

// --- converters ---

func packagingRemovalToProto(r sqlcgen.PackagingRemoval, p sqlcgen.Product, lotCode, jurisdiction string) *stillhousev1.PackagingRemoval {
	out := &stillhousev1.PackagingRemoval{
		Id:                  r.ID.String(),
		TenantId:            r.TenantID.String(),
		RemovalNo:           r.RemovalNo,
		PackagedInventoryId: r.PackagedInventoryID.String(),
		ProductName:         p.Name,
		LotCode:             lotCode,
		Jurisdiction:        jurisdiction,
		RemovalDate:         formatDate(r.RemovalDate),
		BottlesRemoved:      r.BottlesRemoved,
		DestinationKind:     removalDestinationKindToProto(r.DestinationKind),
		DestinationName:     r.DestinationName,
		Reference:           r.Reference,
		BottleSizeMl:        r.BottleSizeMl,
		BottleAbvPct:        r.BottleAbvPct,
		TotalLitres:         r.TotalLitres,
		TotalLaa:            r.TotalLaa,
		DutyRatePerLaa:      r.DutyRatePerLaa,
		DutyAmountCad:       r.DutyAmountCad,
		Notes:               r.Notes,
		CreatedAt:           timestamppb.New(r.CreatedAt.Time),
		VoidedReason:        r.VoidedReason,
	}
	if r.VoidedAt.Valid {
		out.VoidedAt = timestamppb.New(r.VoidedAt.Time)
	}
	if r.VoidedBy.Valid {
		out.VoidedBy = r.VoidedBy.UUID.String()
	}
	return out
}

func removalDestinationKindToDB(k stillhousev1.RemovalDestinationKind) (sqlcgen.RemovalDestinationKind, error) {
	switch k {
	case stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_UNSPECIFIED,
		stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_DUTY_PAID_CUSTOMER:
		return sqlcgen.RemovalDestinationKindDutyPaidCustomer, nil
	case stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_EXPORT:
		return sqlcgen.RemovalDestinationKindExport, nil
	case stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_SAMPLE:
		return sqlcgen.RemovalDestinationKindSample, nil
	case stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_DESTROYED:
		return sqlcgen.RemovalDestinationKindDestroyed, nil
	case stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_TRANSFER_OUT_IN_BOND:
		return sqlcgen.RemovalDestinationKindTransferOutInBond, nil
	case stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_OTHER:
		return sqlcgen.RemovalDestinationKindOther, nil
	}
	return "", errors.New("invalid removal destination kind")
}

func removalDestinationKindToProto(k sqlcgen.RemovalDestinationKind) stillhousev1.RemovalDestinationKind {
	switch k {
	case sqlcgen.RemovalDestinationKindDutyPaidCustomer:
		return stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_DUTY_PAID_CUSTOMER
	case sqlcgen.RemovalDestinationKindExport:
		return stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_EXPORT
	case sqlcgen.RemovalDestinationKindSample:
		return stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_SAMPLE
	case sqlcgen.RemovalDestinationKindDestroyed:
		return stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_DESTROYED
	case sqlcgen.RemovalDestinationKindTransferOutInBond:
		return stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_TRANSFER_OUT_IN_BOND
	case sqlcgen.RemovalDestinationKindOther:
		return stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_OTHER
	}
	return stillhousev1.RemovalDestinationKind_REMOVAL_DESTINATION_KIND_UNSPECIFIED
}
