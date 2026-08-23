package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"

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

// countEpsilon is how close two figures have to be to count as agreeing.
// Volumes are doubles all the way down, so an exact comparison would
// report a variance of 1e-13 on a container nobody touched.
const countEpsilon = 1e-6

type StockCountService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewStockCountService(db *tenantdb.DB, logger *slog.Logger) *StockCountService {
	return &StockCountService{db: db, logger: logger}
}

func (s *StockCountService) fail(op string, err error) error {
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return connectErr
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return connect.NewError(connect.CodeNotFound, errors.New("not found"))
	}
	s.logger.Error(op, "err", err)
	return connect.NewError(connect.CodeInternal, errors.New("internal error"))
}

// OpenStockCount takes the sheet: everything in scope, with what the
// ledger says right now.
//
// The book figures are captured here rather than at posting. A count that
// takes a morning while somebody else is shipping would otherwise measure
// the shipping rather than the discrepancy.
func (s *StockCountService) OpenStockCount(
	ctx context.Context,
	req *connect.Request[stillhousev1.OpenStockCountRequest],
) (*connect.Response[stillhousev1.OpenStockCountResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	scope, err := stockScopeToDB(in.GetScope())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	locationID, err := parseOptionalUUID(in.GetLocationId(), "location_id")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var out *stillhousev1.StockCount
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := q.LockDocumentSequence(ctx, "stock_counts"); e != nil {
			return e
		}
		nextNo, e := q.NextStockCountNo(ctx)
		if e != nil {
			return e
		}
		count, e := q.CreateStockCount(ctx, sqlcgen.CreateStockCountParams{
			TenantID: u.TenantID, CountNo: nextNo, Name: in.GetName(),
			Scope: scope, LocationID: locationID, Notes: in.GetNotes(),
			CreatedBy: u.ID,
		})
		if e != nil {
			return e
		}

		add := func(p sqlcgen.AddStockCountLineParams) error {
			p.TenantID, p.StockCountID = u.TenantID, count.ID
			_, ae := q.AddStockCountLine(ctx, p)
			return ae
		}
		if scope == sqlcgen.StockCountScopeBulk || scope == sqlcgen.StockCountScopeAll {
			rows, re := q.StockCountBulkSubjects(ctx, locationID)
			if re != nil {
				return re
			}
			for _, r := range rows {
				if e := add(sqlcgen.AddStockCountLineParams{
					BulkContainerID: uuid.NullUUID{UUID: r.ID, Valid: true},
					BookQuantity:    r.CurrentVolumeL, Uom: "L",
				}); e != nil {
					return e
				}
			}
		}
		if scope == sqlcgen.StockCountScopePackaged || scope == sqlcgen.StockCountScopeAll {
			rows, re := q.StockCountPackagedSubjects(ctx, locationID)
			if re != nil {
				return re
			}
			for _, r := range rows {
				if e := add(sqlcgen.AddStockCountLineParams{
					PackagedInventoryID: uuid.NullUUID{UUID: r.ID, Valid: true},
					BookQuantity:        float64(r.BottlesOnHand), Uom: "bottles",
				}); e != nil {
					return e
				}
			}
		}
		if scope == sqlcgen.StockCountScopeMaterials || scope == sqlcgen.StockCountScopeAll {
			rows, re := q.StockCountMaterialSubjects(ctx)
			if re != nil {
				return re
			}
			for _, r := range rows {
				if e := add(sqlcgen.AddStockCountLineParams{
					MaterialLotID: uuid.NullUUID{UUID: r.ID, Valid: true},
					BookQuantity:  r.QuantityOnHand, Uom: r.Uom,
				}); e != nil {
					return e
				}
			}
		}
		if e := audit.Write(ctx, q, u.TenantID, u.ID, "stock_count", count.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"count_no": count.CountNo, "scope": string(scope),
			}); e != nil {
			return e
		}
		out, e = s.hydrate(ctx, q, count.ID)
		return e
	})
	if err != nil {
		return nil, s.fail("OpenStockCount", err)
	}
	return connect.NewResponse(&stillhousev1.OpenStockCountResponse{Count: out}), nil
}

func (s *StockCountService) RecordCount(
	ctx context.Context,
	req *connect.Request[stillhousev1.RecordCountRequest],
) (*connect.Response[stillhousev1.RecordCountResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	lineID, err := uuid.Parse(in.GetLineId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid line_id"))
	}
	if in.GetCountedQuantity() < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("a count cannot be negative"))
	}
	reason, err := adjustmentReasonToDBOptional(in.GetReason())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var out *stillhousev1.StockCount
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		line, e := q.GetStockCountLine(ctx, lineID)
		if e != nil {
			return e
		}
		count, e := q.GetStockCount(ctx, line.StockCountID)
		if e != nil {
			return e
		}
		if count.Status != sqlcgen.StockCountStatusOpen &&
			count.Status != sqlcgen.StockCountStatusCounted {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("that count is closed"))
		}
		if line.PostedAt.Valid {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("that line has already been posted"))
		}
		// A volume without a strength says nothing about the alcohol, and
		// the alcohol is the point.
		if line.BulkContainerID.Valid && in.GetCountedQuantity() > 0 &&
			!in.GetCountedAbvPctSet() {
			return connect.NewError(connect.CodeInvalidArgument,
				errors.New("give the strength you gauged as well — a volume with no "+
					"strength says nothing about the alcohol in it"))
		}
		variance := in.GetCountedQuantity() - line.BookQuantity
		// A variance nobody explained is a number.
		if math.Abs(variance) > countEpsilon && reason.Valid == false {
			return connect.NewError(connect.CodeInvalidArgument,
				errors.New("the count differs from the book — say why, so the "+
					"adjustment carries a reason rather than appearing from nowhere"))
		}
		if _, e = q.SetStockCountLineCount(ctx, sqlcgen.SetStockCountLineCountParams{
			ID:              lineID,
			CountedQuantity: in.GetCountedQuantity(),
			CountedAbvPct:   optFloat(in.GetCountedAbvPct(), in.GetCountedAbvPctSet()),
			Reason:          reason,
			Explanation:     in.GetExplanation(),
			CountedBy:       in.GetCountedBy(),
			Notes:           in.GetNotes(),
		}); e != nil {
			return e
		}
		out, e = s.hydrate(ctx, q, count.ID)
		return e
	})
	if err != nil {
		return nil, s.fail("RecordCount", err)
	}
	return connect.NewResponse(&stillhousev1.RecordCountResponse{Count: out}), nil
}

// PostStockCount writes the variances into the ledger.
//
// Each subject goes through the path that already exists for it: a bulk
// container through the reason-coded adjustment stage 149 built, packaged
// stock through the adjustment stage 186 added — which the B266's reverse
// walk was taught about in the same migration, because a balance that
// changed with nothing in the ledger to undo would silently restate a
// period already filed. Material lots are simply set, having no return
// to appear on.
//
// Lines that cannot be posted are named in the response rather than
// skipped. A count that quietly posted half of itself is worse than one
// that posted none.
func (s *StockCountService) PostStockCount(
	ctx context.Context,
	req *connect.Request[stillhousev1.PostStockCountRequest],
) (*connect.Response[stillhousev1.PostStockCountResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	occurredOn, err := parseDateOrToday(req.Msg.GetOccurredOn())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	out := &stillhousev1.PostStockCountResponse{}
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		count, e := q.GetStockCountForUpdate(ctx, id)
		if e != nil {
			return e
		}
		if count.Status == sqlcgen.StockCountStatusPosted {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("that count has already been posted"))
		}
		if count.Status == sqlcgen.StockCountStatusCancelled {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("that count was cancelled"))
		}
		// The adjustments land on whichever return covers this date.
		if e := assertDateNotInLockedPeriod(ctx, q, occurredOn); e != nil {
			return e
		}

		lines, e := q.ListStockCountLines(ctx, id)
		if e != nil {
			return e
		}
		for _, l := range lines {
			if l.PostedAt.Valid {
				continue
			}
			if !l.CountedQuantity.Valid {
				out.Skipped = append(out.Skipped, fmt.Sprintf(
					"%s was never counted, so nothing was posted for it",
					stockSubjectLabel(l)))
				continue
			}
			variance := l.CountedQuantity.Float64 - l.BookQuantity
			if math.Abs(variance) <= countEpsilon {
				// Counted and agreed. Marked posted so the sheet reads as
				// finished rather than half-done.
				if e := q.MarkStockCountLinePosted(ctx,
					sqlcgen.MarkStockCountLinePostedParams{ID: l.ID}); e != nil {
					return e
				}
				continue
			}
			if !l.Reason.Valid {
				out.Skipped = append(out.Skipped, fmt.Sprintf(
					"%s differs from the book by %.4f but carries no reason",
					stockSubjectLabel(l), variance))
				continue
			}

			switch {
			case l.PackagedInventoryID.Valid:
				if e := s.postPackaged(ctx, q, u, l, occurredOn, count.ID); e != nil {
					return e
				}
			case l.MaterialLotID.Valid:
				if _, e := q.SetMaterialLotQuantity(ctx, sqlcgen.SetMaterialLotQuantityParams{
					ID: l.MaterialLotID.UUID, QuantityOnHand: l.CountedQuantity.Float64,
				}); e != nil {
					return e
				}
				if e := q.MarkStockCountLinePosted(ctx,
					sqlcgen.MarkStockCountLinePostedParams{ID: l.ID}); e != nil {
					return e
				}
			default:
				// Bulk. Deliberately not posted from here: the adjustment
				// is a gauge determination, it carries instruments and a
				// 20 °C correction, and the path that does all of that
				// already exists and is tested. Duplicating it here to
				// save a click would be a second implementation of the
				// arithmetic that decides a B266 line.
				out.Skipped = append(out.Skipped, fmt.Sprintf(
					"%s is a vessel — record it from its own page so the gauge "+
						"carries the instruments and the temperature correction",
					stockSubjectLabel(l)))
				continue
			}
			out.AdjustmentsWritten++
		}

		posted, e := q.SetStockCountStatus(ctx, sqlcgen.SetStockCountStatusParams{
			ID: id, Status: sqlcgen.StockCountStatusPosted,
			Actor: uuid.NullUUID{UUID: u.ID, Valid: true},
		})
		if e != nil {
			return e
		}
		if e := audit.Write(ctx, q, u.TenantID, u.ID, "stock_count", id.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"count_no": posted.CountNo,
				"posted":   out.AdjustmentsWritten,
				"skipped":  len(out.Skipped),
				"on":       occurredOn.Time.Format("2006-01-02"),
			}); e != nil {
			return e
		}
		out.Count, e = s.hydrate(ctx, q, id)
		return e
	})
	if err != nil {
		return nil, s.fail("PostStockCount", err)
	}
	return connect.NewResponse(out), nil
}

func (s *StockCountService) postPackaged(
	ctx context.Context, q *sqlcgen.Queries, u sqlcgen.User,
	l sqlcgen.ListStockCountLinesRow, occurredOn pgtype.Date, countID uuid.UUID,
) error {
	lot, err := q.GetPackagedInventoryForUpdate(ctx, l.PackagedInventoryID.UUID)
	if err != nil {
		return err
	}
	product, err := q.GetProduct(ctx, lot.ProductID)
	if err != nil {
		return err
	}
	counted := int32(math.Round(l.CountedQuantity.Float64))
	delta := counted - lot.BottlesOnHand
	if delta == 0 {
		return q.MarkStockCountLinePosted(ctx,
			sqlcgen.MarkStockCountLinePostedParams{ID: l.ID})
	}
	// The alcohol the difference represents, at the product's strength —
	// the same approximation every other packaged figure uses, so the
	// two sides subtract cleanly.
	laaDelta := float64(delta) * float64(product.BottleSizeMl) / 1000 *
		product.TargetAbvPct / 100
	if _, err := q.RecordPackagedAdjustment(ctx, sqlcgen.RecordPackagedAdjustmentParams{
		TenantID: u.TenantID, PackagedInventoryID: lot.ID, OccurredOn: occurredOn,
		BottlesDelta: delta, BookBottles: lot.BottlesOnHand, CountedBottles: counted,
		LaaDelta: laaDelta, Reason: l.Reason.InventoryAdjustmentReason,
		Explanation:  l.Explanation,
		StockCountID: uuid.NullUUID{UUID: countID, Valid: true},
		RecordedBy:   u.ID,
	}); err != nil {
		return err
	}
	if _, err := q.SetPackagedBottles(ctx, sqlcgen.SetPackagedBottlesParams{
		ID: lot.ID, BottlesOnHand: counted,
	}); err != nil {
		return err
	}
	if err := audit.Write(ctx, q, u.TenantID, u.ID, "packaged_inventory",
		lot.ID.String(), sqlcgen.AuditActionUpdate, map[string]any{
			"lot_code":    lot.LotCode,
			"book":        lot.BottlesOnHand,
			"counted":     counted,
			"delta":       delta,
			"laa_delta":   laaDelta,
			"reason":      string(l.Reason.InventoryAdjustmentReason),
			"explanation": l.Explanation,
		}); err != nil {
		return err
	}
	return q.MarkStockCountLinePosted(ctx,
		sqlcgen.MarkStockCountLinePostedParams{ID: l.ID})
}

func (s *StockCountService) ListStockCounts(
	ctx context.Context,
	_ *connect.Request[stillhousev1.ListStockCountsRequest],
) (*connect.Response[stillhousev1.ListStockCountsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.ListStockCountsRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListStockCounts(ctx)
		return e
	}); err != nil {
		return nil, s.fail("ListStockCounts", err)
	}
	out := make([]*stillhousev1.StockCount, 0, len(rows))
	for _, r := range rows {
		c := stockCountToProto(sqlcgen.StockCount{
			ID: r.ID, CountNo: r.CountNo, Name: r.Name, Scope: r.Scope,
			LocationID: r.LocationID, Status: r.Status, OpenedAt: r.OpenedAt,
			CountedAt: r.CountedAt, PostedAt: r.PostedAt,
			CancelReason: r.CancelReason, Notes: r.Notes,
		}, r.LocationName)
		c.LineCount, c.CountedLines, c.PostedLines = r.LineCount, r.CountedLines, r.PostedLines
		out = append(out, c)
	}
	return connect.NewResponse(&stillhousev1.ListStockCountsResponse{Counts: out}), nil
}

func (s *StockCountService) GetStockCount(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetStockCountRequest],
) (*connect.Response[stillhousev1.GetStockCountResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	var out *stillhousev1.StockCount
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		out, e = s.hydrate(ctx, q, id)
		return e
	}); err != nil {
		return nil, s.fail("GetStockCount", err)
	}
	return connect.NewResponse(&stillhousev1.GetStockCountResponse{Count: out}), nil
}

func (s *StockCountService) CancelStockCount(
	ctx context.Context,
	req *connect.Request[stillhousev1.CancelStockCountRequest],
) (*connect.Response[stillhousev1.CancelStockCountResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	reason := strings.TrimSpace(req.Msg.GetReason())
	if reason == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say why the count was abandoned"))
	}
	var out *stillhousev1.StockCount
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		existing, e := q.GetStockCountForUpdate(ctx, id)
		if e != nil {
			return e
		}
		if existing.Status == sqlcgen.StockCountStatusPosted {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("that count has been posted — its adjustments are in the "+
					"ledger and reversing them is a new adjustment, not a cancellation"))
		}
		if _, e := q.SetStockCountStatus(ctx, sqlcgen.SetStockCountStatusParams{
			ID: id, Status: sqlcgen.StockCountStatusCancelled, CancelReason: reason,
		}); e != nil {
			return e
		}
		out, e = s.hydrate(ctx, q, id)
		return e
	})
	if err != nil {
		return nil, s.fail("CancelStockCount", err)
	}
	return connect.NewResponse(&stillhousev1.CancelStockCountResponse{Count: out}), nil
}

// --- helpers ---

func (s *StockCountService) hydrate(
	ctx context.Context, q *sqlcgen.Queries, id uuid.UUID,
) (*stillhousev1.StockCount, error) {
	c, err := q.GetStockCount(ctx, id)
	if err != nil {
		return nil, err
	}
	out := stockCountToProto(c, "")
	lines, err := q.ListStockCountLines(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, l := range lines {
		line := &stillhousev1.StockCountLine{
			Id:                  l.ID.String(),
			BulkContainerId:     nullUUIDString(l.BulkContainerID),
			PackagedInventoryId: nullUUIDString(l.PackagedInventoryID),
			MaterialLotId:       nullUUIDString(l.MaterialLotID),
			Subject:             stockSubjectLabel(l),
			Detail:              stockSubjectDetail(l),
			Uom:                 l.Uom,
			BookQuantity:        l.BookQuantity,
			Explanation:         l.Explanation,
			CountedBy:           l.CountedBy,
			Notes:               l.Notes,
			Posted:              l.PostedAt.Valid,
		}
		if l.Reason.Valid {
			line.Reason = adjustmentReasonToProto(l.Reason.InventoryAdjustmentReason)
		}
		if l.CountedQuantity.Valid {
			line.Counted = true
			line.CountedQuantity = l.CountedQuantity.Float64
			line.Variance = l.CountedQuantity.Float64 - l.BookQuantity
			if math.Abs(line.Variance) > countEpsilon {
				out.VarianceLines++
			}
			out.CountedLines++
		}
		if l.CountedAbvPct.Valid {
			line.CountedAbvPct, line.CountedAbvPctSet = l.CountedAbvPct.Float64, true
		}
		if l.PostedAt.Valid {
			out.PostedLines++
		}
		out.Lines = append(out.Lines, line)
		out.LineCount++
	}
	return out, nil
}

func stockSubjectLabel(l sqlcgen.ListStockCountLinesRow) string {
	switch {
	case l.ContainerName != "":
		return l.ContainerName
	case l.LotCode != "":
		return l.LotCode
	case l.MaterialName != "":
		return l.MaterialName
	default:
		return "unnamed subject"
	}
}

func stockSubjectDetail(l sqlcgen.ListStockCountLinesRow) string {
	switch {
	case l.BulkContainerID.Valid:
		return "vessel"
	case l.PackagedInventoryID.Valid:
		return l.LotProductName
	case l.MaterialLotID.Valid:
		if l.SupplierLot != "" {
			return "lot " + l.SupplierLot
		}
		return "material"
	default:
		return ""
	}
}

func stockCountToProto(c sqlcgen.StockCount, locationName string) *stillhousev1.StockCount {
	out := &stillhousev1.StockCount{
		Id: c.ID.String(), CountNo: c.CountNo, Name: c.Name,
		Scope: stockScopeToProto(c.Scope), LocationId: nullUUIDString(c.LocationID),
		LocationName: locationName, Status: stockStatusToProto(c.Status),
		CancelReason: c.CancelReason, Notes: c.Notes,
	}
	if c.OpenedAt.Valid {
		out.OpenedAt = timestamppb.New(c.OpenedAt.Time)
	}
	if c.CountedAt.Valid {
		out.CountedAt = timestamppb.New(c.CountedAt.Time)
	}
	if c.PostedAt.Valid {
		out.PostedAt = timestamppb.New(c.PostedAt.Time)
	}
	return out
}

func stockScopeToProto(s sqlcgen.StockCountScope) stillhousev1.StockCountScope {
	switch s {
	case sqlcgen.StockCountScopeBulk:
		return stillhousev1.StockCountScope_STOCK_COUNT_SCOPE_BULK
	case sqlcgen.StockCountScopePackaged:
		return stillhousev1.StockCountScope_STOCK_COUNT_SCOPE_PACKAGED
	case sqlcgen.StockCountScopeMaterials:
		return stillhousev1.StockCountScope_STOCK_COUNT_SCOPE_MATERIALS
	default:
		return stillhousev1.StockCountScope_STOCK_COUNT_SCOPE_ALL
	}
}

func stockScopeToDB(s stillhousev1.StockCountScope) (sqlcgen.StockCountScope, error) {
	switch s {
	case stillhousev1.StockCountScope_STOCK_COUNT_SCOPE_BULK:
		return sqlcgen.StockCountScopeBulk, nil
	case stillhousev1.StockCountScope_STOCK_COUNT_SCOPE_PACKAGED:
		return sqlcgen.StockCountScopePackaged, nil
	case stillhousev1.StockCountScope_STOCK_COUNT_SCOPE_MATERIALS:
		return sqlcgen.StockCountScopeMaterials, nil
	case stillhousev1.StockCountScope_STOCK_COUNT_SCOPE_ALL,
		stillhousev1.StockCountScope_STOCK_COUNT_SCOPE_UNSPECIFIED:
		return sqlcgen.StockCountScopeAll, nil
	default:
		return "", errors.New("unknown scope")
	}
}

func stockStatusToProto(s sqlcgen.StockCountStatus) stillhousev1.StockCountStatus {
	switch s {
	case sqlcgen.StockCountStatusOpen:
		return stillhousev1.StockCountStatus_STOCK_COUNT_STATUS_OPEN
	case sqlcgen.StockCountStatusCounted:
		return stillhousev1.StockCountStatus_STOCK_COUNT_STATUS_COUNTED
	case sqlcgen.StockCountStatusPosted:
		return stillhousev1.StockCountStatus_STOCK_COUNT_STATUS_POSTED
	case sqlcgen.StockCountStatusCancelled:
		return stillhousev1.StockCountStatus_STOCK_COUNT_STATUS_CANCELLED
	default:
		return stillhousev1.StockCountStatus_STOCK_COUNT_STATUS_UNSPECIFIED
	}
}

func adjustmentReasonToDBOptional(
	r stillhousev1.InventoryAdjustmentReason,
) (sqlcgen.NullInventoryAdjustmentReason, error) {
	if r == stillhousev1.InventoryAdjustmentReason_INVENTORY_ADJUSTMENT_REASON_UNSPECIFIED {
		return sqlcgen.NullInventoryAdjustmentReason{}, nil
	}
	db, err := adjustmentReasonToDB(r)
	if err != nil {
		return sqlcgen.NullInventoryAdjustmentReason{}, err
	}
	return sqlcgen.NullInventoryAdjustmentReason{
		InventoryAdjustmentReason: db, Valid: true,
	}, nil
}
