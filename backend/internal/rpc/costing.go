package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/costing"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type CostingService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewCostingService(db *tenantdb.DB, logger *slog.Logger) *CostingService {
	return &CostingService{db: db, logger: logger}
}

func (s *CostingService) fail(op string, err error) error {
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

func (s *CostingService) SaveCostRates(
	ctx context.Context,
	req *connect.Request[stillhousev1.SaveCostRatesRequest],
) (*connect.Response[stillhousev1.SaveCostRatesResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	effective, err := parseDateOrToday(in.GetEffectiveFrom())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	labour, err := optionalNumeric(in.GetLabourRateCadPerHour(), "labour_rate_cad_per_hour")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rate, err := optionalNumeric(in.GetOverheadRate(), "overhead_rate")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	basis, err := overheadBasisToDB(in.GetOverheadBasis())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	// A basis without a rate, or a rate without a basis, is half a policy
	// and would silently absorb nothing. The table's CHECK holds the same
	// line; this is the version with a sentence attached.
	if basis.Valid != rate.Valid {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("an overhead basis needs a rate, and a rate needs a basis — "+
				"set both or neither"))
	}

	var out sqlcgen.CostRate
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		out, e = q.SaveCostRates(ctx, sqlcgen.SaveCostRatesParams{
			TenantID:             u.TenantID,
			EffectiveFrom:        effective,
			LabourRateCadPerHour: labour,
			OverheadBasis:        basis,
			OverheadRate:         rate,
			Notes:                in.GetNotes(),
			CreatedBy:            u.ID,
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "cost_rates", out.ID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"effective_from": effective.Time.Format("2006-01-02"),
				"labour_rate":    numericToDecimalString(labour),
				"overhead_basis": string(basis.OverheadBasis),
				"overhead_rate":  numericToDecimalString(rate),
			})
	})
	if err != nil {
		return nil, s.fail("SaveCostRates", err)
	}
	return connect.NewResponse(&stillhousev1.SaveCostRatesResponse{
		Rates: costRatesToProto(out),
	}), nil
}

func (s *CostingService) ListCostRates(
	ctx context.Context,
	_ *connect.Request[stillhousev1.ListCostRatesRequest],
) (*connect.Response[stillhousev1.ListCostRatesResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.CostRate
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListCostRates(ctx)
		return e
	}); err != nil {
		return nil, s.fail("ListCostRates", err)
	}
	out := make([]*stillhousev1.CostRates, 0, len(rows))
	for _, r := range rows {
		out = append(out, costRatesToProto(r))
	}
	return connect.NewResponse(&stillhousev1.ListCostRatesResponse{Rates: out}), nil
}

func (s *CostingService) DeleteCostRates(
	ctx context.Context,
	req *connect.Request[stillhousev1.DeleteCostRatesRequest],
) (*connect.Response[stillhousev1.DeleteCostRatesResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := assertNoLegalHold(ctx, q, "cost rates"); e != nil {
			return e
		}
		if e := q.DeleteCostRates(ctx, id); e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "cost_rates", id.String(),
			sqlcgen.AuditActionDelete, map[string]any{})
	}); err != nil {
		return nil, s.fail("DeleteCostRates", err)
	}
	return connect.NewResponse(&stillhousev1.DeleteCostRatesResponse{}), nil
}

func (s *CostingService) RecordLabour(
	ctx context.Context,
	req *connect.Request[stillhousev1.RecordLabourRequest],
) (*connect.Response[stillhousev1.RecordLabourResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	subject, err := labourSubjectToDB(in.GetSubject())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if in.GetHours() <= 0 || in.GetHours() > 24 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("hours must be more than zero and no more than 24 — "+
				"split a longer stretch across the days it actually covered"))
	}
	workedOn, err := parseDateOrToday(in.GetWorkedOn())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	rate, err := optionalNumeric(in.GetRateCadPerHour(), "rate_cad_per_hour")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	workedBy, err := parseOptionalUUID(in.GetWorkedByUserId(), "worked_by_user_id")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var out sqlcgen.LabourEntry
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var hours pgtype.Numeric
		if e := hours.Scan(fmt.Sprintf("%.2f", in.GetHours())); e != nil {
			return e
		}
		var e error
		out, e = q.RecordLabour(ctx, sqlcgen.RecordLabourParams{
			TenantID:          u.TenantID,
			MashRunID:         subject.mash,
			DistillationRunID: subject.distillation,
			BottlingRunID:     subject.bottling,
			WorkOrderID:       subject.workOrder,
			WorkedOn:          workedOn,
			Hours:             hours,
			WorkedBy:          workedBy,
			WorkedByName:      in.GetWorkedByName(),
			RateCadPerHour:    rate,
			Notes:             in.GetNotes(),
			RecordedBy:        u.ID,
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "labour_entry", out.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"subject":   subject.label,
				"hours":     in.GetHours(),
				"worked_on": workedOn.Time.Format("2006-01-02"),
				"worked_by": in.GetWorkedByName(),
			})
	})
	if err != nil {
		return nil, s.fail("RecordLabour", err)
	}
	return connect.NewResponse(&stillhousev1.RecordLabourResponse{
		Entry: labourEntryToProto(out, ""),
	}), nil
}

func (s *CostingService) DeleteLabourEntry(
	ctx context.Context,
	req *connect.Request[stillhousev1.DeleteLabourEntryRequest],
) (*connect.Response[stillhousev1.DeleteLabourEntryResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := assertNoLegalHold(ctx, q, "a labour entry"); e != nil {
			return e
		}
		if e := q.DeleteLabourEntry(ctx, id); e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "labour_entry", id.String(),
			sqlcgen.AuditActionDelete, map[string]any{})
	}); err != nil {
		return nil, s.fail("DeleteLabourEntry", err)
	}
	return connect.NewResponse(&stillhousev1.DeleteLabourEntryResponse{}), nil
}

func (s *CostingService) ListLabour(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListLabourRequest],
) (*connect.Response[stillhousev1.ListLabourResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	subject, err := labourSubjectToDB(req.Msg.GetSubject())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	var rows []sqlcgen.ListLabourForSubjectRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListLabourForSubject(ctx, sqlcgen.ListLabourForSubjectParams{
			MashRunID:         subject.mash,
			DistillationRunID: subject.distillation,
			BottlingRunID:     subject.bottling,
			WorkOrderID:       subject.workOrder,
		})
		return e
	}); err != nil {
		return nil, s.fail("ListLabour", err)
	}
	out := &stillhousev1.ListLabourResponse{}
	for _, r := range rows {
		e := labourEntryToProto(sqlcgen.LabourEntry{
			ID: r.ID, MashRunID: r.MashRunID, DistillationRunID: r.DistillationRunID,
			BottlingRunID: r.BottlingRunID, WorkOrderID: r.WorkOrderID,
			WorkedOn: r.WorkedOn, Hours: r.Hours, WorkedBy: r.WorkedBy,
			WorkedByName: r.WorkedByName, RateCadPerHour: r.RateCadPerHour,
			Notes: r.Notes, CreatedAt: r.CreatedAt,
		}, r.WorkedByDisplay)
		out.Entries = append(out.Entries, e)
		out.TotalHours += e.GetHours()
	}
	return connect.NewResponse(out), nil
}

func (s *CostingService) BottlingRunFullCost(
	ctx context.Context,
	req *connect.Request[stillhousev1.BottlingRunFullCostRequest],
) (*connect.Response[stillhousev1.BottlingRunFullCostResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	runID, err := uuid.Parse(req.Msg.GetBottlingRunId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid bottling_run_id"))
	}
	var res costing.FullResult
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		res, e = costing.BottlingRunFullCost(ctx, q, runID)
		return e
	}); err != nil {
		return nil, s.fail("BottlingRunFullCost", err)
	}
	return connect.NewResponse(&stillhousev1.BottlingRunFullCostResponse{
		BottlingRunId:         runID.String(),
		BottleCount:           res.BottleCount,
		Materials:             componentToProto(res.MaterialsComponent),
		Labour:                componentToProto(res.Labour),
		Overhead:              componentToProto(res.Overhead),
		TotalCad:              res.TotalCAD,
		PerBottleCad:          res.PerBottleCAD(),
		Complete:              res.Complete,
		LabourHours:           res.LabourHours,
		UnpricedMaterialLines: int32(res.Materials.UnpricedLines),
	}), nil
}

func (s *CostingService) InventoryValue(
	ctx context.Context,
	_ *connect.Request[stillhousev1.InventoryValueRequest],
) (*connect.Response[stillhousev1.InventoryValueResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var v costing.InventoryValue
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		v, e = costing.ValueInventory(ctx, q)
		return e
	}); err != nil {
		return nil, s.fail("InventoryValue", err)
	}
	return connect.NewResponse(&stillhousev1.InventoryValueResponse{
		Wip:           bucketToProto(v.WIP),
		FinishedGoods: bucketToProto(v.FinishedGoods),
		TotalCad:      v.TotalCAD,
		Basis:         v.Basis,
	}), nil
}

// --- converters ---

func componentToProto(c costing.Component) *stillhousev1.CostComponent {
	return &stillhousev1.CostComponent{
		Name:      c.Name,
		AmountCad: c.AmountCAD,
		Basis:     c.Basis,
		Available: c.Available,
		Missing:   c.Missing,
	}
}

func bucketToProto(b costing.Bucket) *stillhousev1.InventoryBucket {
	out := &stillhousev1.InventoryBucket{
		ValueCad:  b.ValueCAD,
		TotalLaa:  b.TotalLAA,
		ValuedLaa: b.ValuedLAA,
		Unvalued:  int32(b.Unvalued),
	}
	for _, l := range b.Lines {
		out.Lines = append(out.Lines, &stillhousev1.InventoryValueLine{
			Name: l.Name, Detail: l.Detail, Laa: l.LAA, Bottles: l.Bottles,
			UnitCad: l.UnitCAD, ValueCad: l.ValueCAD, Valued: l.Valued, Why: l.Why,
		})
	}
	return out
}

func costRatesToProto(r sqlcgen.CostRate) *stillhousev1.CostRates {
	out := &stillhousev1.CostRates{
		Id:                   r.ID.String(),
		EffectiveFrom:        formatDate(r.EffectiveFrom),
		LabourRateCadPerHour: numericToDecimalString(r.LabourRateCadPerHour),
		OverheadRate:         numericToDecimalString(r.OverheadRate),
		Notes:                r.Notes,
	}
	if r.OverheadBasis.Valid {
		out.OverheadBasis = overheadBasisToProto(r.OverheadBasis.OverheadBasis)
	}
	if r.CreatedAt.Valid {
		out.CreatedAt = timestamppb.New(r.CreatedAt.Time)
	}
	return out
}

func labourEntryToProto(e sqlcgen.LabourEntry, display string) *stillhousev1.LabourEntry {
	name := e.WorkedByName
	if name == "" {
		name = display
	}
	hours, _ := e.Hours.Float64Value()
	out := &stillhousev1.LabourEntry{
		Id: e.ID.String(),
		Subject: &stillhousev1.LabourSubject{
			MashRunId:         nullUUIDString(e.MashRunID),
			DistillationRunId: nullUUIDString(e.DistillationRunID),
			BottlingRunId:     nullUUIDString(e.BottlingRunID),
			WorkOrderId:       nullUUIDString(e.WorkOrderID),
		},
		WorkedOn:       formatDate(e.WorkedOn),
		Hours:          hours.Float64,
		WorkedByUserId: nullUUIDString(e.WorkedBy),
		WorkedByName:   name,
		RateCadPerHour: numericToDecimalString(e.RateCadPerHour),
		Notes:          e.Notes,
	}
	if e.CreatedAt.Valid {
		out.CreatedAt = timestamppb.New(e.CreatedAt.Time)
	}
	return out
}

func overheadBasisToProto(b sqlcgen.OverheadBasis) stillhousev1.OverheadBasis {
	switch b {
	case sqlcgen.OverheadBasisPerMaterialDollar:
		return stillhousev1.OverheadBasis_OVERHEAD_BASIS_PER_MATERIAL_DOLLAR
	case sqlcgen.OverheadBasisPerLabourHour:
		return stillhousev1.OverheadBasis_OVERHEAD_BASIS_PER_LABOUR_HOUR
	case sqlcgen.OverheadBasisPerLaa:
		return stillhousev1.OverheadBasis_OVERHEAD_BASIS_PER_LAA
	default:
		return stillhousev1.OverheadBasis_OVERHEAD_BASIS_UNSPECIFIED
	}
}

func overheadBasisToDB(b stillhousev1.OverheadBasis) (sqlcgen.NullOverheadBasis, error) {
	switch b {
	case stillhousev1.OverheadBasis_OVERHEAD_BASIS_UNSPECIFIED:
		return sqlcgen.NullOverheadBasis{}, nil
	case stillhousev1.OverheadBasis_OVERHEAD_BASIS_PER_MATERIAL_DOLLAR:
		return sqlcgen.NullOverheadBasis{OverheadBasis: sqlcgen.OverheadBasisPerMaterialDollar, Valid: true}, nil
	case stillhousev1.OverheadBasis_OVERHEAD_BASIS_PER_LABOUR_HOUR:
		return sqlcgen.NullOverheadBasis{OverheadBasis: sqlcgen.OverheadBasisPerLabourHour, Valid: true}, nil
	case stillhousev1.OverheadBasis_OVERHEAD_BASIS_PER_LAA:
		return sqlcgen.NullOverheadBasis{OverheadBasis: sqlcgen.OverheadBasisPerLaa, Valid: true}, nil
	default:
		return sqlcgen.NullOverheadBasis{}, errors.New("unknown overhead basis")
	}
}

type labourSubject struct {
	mash, distillation, bottling, workOrder uuid.NullUUID
	label                                   string
}

func labourSubjectToDB(s *stillhousev1.LabourSubject) (labourSubject, error) {
	var out labourSubject
	if s == nil {
		return out, errors.New("say what the hours were worked on")
	}
	var err error
	if out.mash, err = parseOptionalUUID(s.GetMashRunId(), "mash_run_id"); err != nil {
		return out, err
	}
	if out.distillation, err = parseOptionalUUID(s.GetDistillationRunId(), "distillation_run_id"); err != nil {
		return out, err
	}
	if out.bottling, err = parseOptionalUUID(s.GetBottlingRunId(), "bottling_run_id"); err != nil {
		return out, err
	}
	if out.workOrder, err = parseOptionalUUID(s.GetWorkOrderId(), "work_order_id"); err != nil {
		return out, err
	}
	n := 0
	for _, pair := range []struct {
		id    uuid.NullUUID
		label string
	}{
		{out.mash, "mash"}, {out.distillation, "distillation"},
		{out.bottling, "bottling run"}, {out.workOrder, "work order"},
	} {
		if pair.id.Valid {
			n++
			out.label = pair.label
		}
	}
	if n != 1 {
		return out, errors.New("hours are worked on exactly one thing — a mash, a " +
			"distillation, a bottling run or a work order")
	}
	return out, nil
}

// optionalNumeric parses a decimal string, treating empty as not set.
// Money and policy rates cross the wire as text for the reason set out in
// numericToDecimalString: a rate rendered through a double is a rate that
// can lose a fraction of a cent per bottle, every bottle.
func optionalNumeric(v, field string) (pgtype.Numeric, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return pgtype.Numeric{}, nil
	}
	var n pgtype.Numeric
	if err := n.Scan(v); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("%s must be a decimal amount", field)
	}
	return n, nil
}
