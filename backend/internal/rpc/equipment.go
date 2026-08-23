package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/money"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type EquipmentService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewEquipmentService(db *tenantdb.DB, logger *slog.Logger) *EquipmentService {
	return &EquipmentService{db: db, logger: logger}
}

func (s *EquipmentService) fail(op string, err error) error {
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return connectErr
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return connect.NewError(connect.CodeNotFound, errors.New("not found"))
	}
	if dup := uniqueViolation(err, "piece of equipment"); dup != nil {
		return dup
	}
	s.logger.Error(op, "err", err)
	return connect.NewError(connect.CodeInternal, errors.New("internal error"))
}

func (s *EquipmentService) SaveEquipment(
	ctx context.Context,
	req *connect.Request[stillhousev1.SaveEquipmentRequest],
) (*connect.Response[stillhousev1.SaveEquipmentResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	if strings.TrimSpace(in.GetName()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("give it a name — the one written on it, ideally"))
	}
	id, err := parseOptionalUUID(in.GetId(), "id")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	kind, err := equipmentKindToDB(in.GetKind())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	status, err := equipmentStatusToDB(in.GetStatus())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	locationID, err := parseOptionalUUID(in.GetLocationId(), "location_id")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	commissioned, err := parseOptionalDate(in.GetCommissionedOn(), "commissioned_on")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	retiredOn, err := parseOptionalDate(in.GetRetiredOn(), "retired_on")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	// A figure set to zero is not a figure. The columns' CHECKs would
	// reject it with a constraint name; this rejects it with a sentence,
	// and says why leaving it unset is the right answer.
	for _, f := range []struct {
		name  string
		value float64
		set   bool
	}{
		{"capacity", in.GetCapacityL(), in.GetCapacityLSet()},
		{"typical run time", in.GetTypicalRunHours(), in.GetTypicalRunHoursSet()},
		{"service interval", in.GetServiceIntervalHours(), in.GetServiceIntervalHoursSet()},
	} {
		if f.set && f.value <= 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"a %s of zero is not a %s — leave it blank if it is not known, and "+
					"anything that depends on it will say so rather than assume",
				f.name, f.name))
		}
	}

	// Retiring a still is a licensing fact, not a tidy-up. The table's
	// CHECK holds the same line; this is the version with a sentence.
	if status == sqlcgen.EquipmentStatusRetired &&
		strings.TrimSpace(in.GetRetiredReason()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say why it was retired — a still leaving service is a fact "+
				"about the premises, and the register is where an inspector looks"))
	}

	var out sqlcgen.Equipment
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		out, e = q.SaveEquipment(ctx, sqlcgen.SaveEquipmentParams{
			ID: id, TenantID: u.TenantID, Name: in.GetName(),
			Kind: kind, Status: status, LocationID: locationID,
			Manufacturer: in.GetManufacturer(), Model: in.GetModel(),
			SerialNo: in.GetSerialNo(), CommissionedOn: commissioned,
			CapacityL:            optFloat(in.GetCapacityL(), in.GetCapacityLSet()),
			TypicalRunHours:      optFloat(in.GetTypicalRunHours(), in.GetTypicalRunHoursSet()),
			ServiceIntervalHours: optFloat(in.GetServiceIntervalHours(), in.GetServiceIntervalHoursSet()),
			ServiceIntervalDays:  optInt(in.GetServiceIntervalDays(), in.GetServiceIntervalDaysSet()),
			Notes:                in.GetNotes(),
			RetiredOn:            retiredOn,
			RetiredReason:        in.GetRetiredReason(),
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "equipment", out.ID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"name": out.Name, "kind": string(kind), "status": string(status),
			})
	})
	if err != nil {
		return nil, s.fail("SaveEquipment", err)
	}
	return connect.NewResponse(&stillhousev1.SaveEquipmentResponse{
		Equipment: equipmentToProto(out, "", pgtype.Date{}, 0),
	}), nil
}

func (s *EquipmentService) ListEquipment(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListEquipmentRequest],
) (*connect.Response[stillhousev1.ListEquipmentResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.ListEquipmentRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListEquipment(ctx, req.Msg.GetIncludeRetired())
		return e
	}); err != nil {
		return nil, s.fail("ListEquipment", err)
	}
	out := &stillhousev1.ListEquipmentResponse{}
	for _, r := range rows {
		e := equipmentToProto(sqlcgen.Equipment{
			ID: r.ID, Name: r.Name, Kind: r.Kind, Status: r.Status,
			LocationID: r.LocationID, Manufacturer: r.Manufacturer,
			Model: r.Model, SerialNo: r.SerialNo,
			CommissionedOn: r.CommissionedOn, CapacityL: r.CapacityL,
			TypicalRunHours:      r.TypicalRunHours,
			ServiceIntervalHours: r.ServiceIntervalHours,
			ServiceIntervalDays:  r.ServiceIntervalDays,
			Notes:                r.Notes, RetiredOn: r.RetiredOn,
			RetiredReason: r.RetiredReason, UpdatedAt: r.UpdatedAt,
		}, r.LocationName, r.LastServicedOn, r.RunCount)
		out.Equipment = append(out.Equipment, e)
		switch r.Status {
		case sqlcgen.EquipmentStatusInService:
			out.InService++
		case sqlcgen.EquipmentStatusDown:
			out.Down++
		}
		if e.GetServiceDue() {
			out.ServiceDue++
		}
		// Named, because scheduling cannot use a capacity nobody has
		// recorded and the operator is the only one who knows it.
		if r.Status != sqlcgen.EquipmentStatusRetired && !r.CapacityL.Valid {
			out.CapacityUnknown++
		}
	}
	return connect.NewResponse(out), nil
}

func (s *EquipmentService) GetEquipment(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetEquipmentRequest],
) (*connect.Response[stillhousev1.GetEquipmentResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	out := &stillhousev1.GetEquipmentResponse{}
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		row, e := q.GetEquipment(ctx, id)
		if e != nil {
			return e
		}
		services, e := q.ListEquipmentService(ctx, id)
		if e != nil {
			return e
		}
		var last pgtype.Date
		if len(services) > 0 {
			last = services[0].PerformedOn
		}
		out.Equipment = equipmentToProto(sqlcgen.Equipment{
			ID: row.ID, Name: row.Name, Kind: row.Kind, Status: row.Status,
			LocationID: row.LocationID, Manufacturer: row.Manufacturer,
			Model: row.Model, SerialNo: row.SerialNo,
			CommissionedOn: row.CommissionedOn, CapacityL: row.CapacityL,
			TypicalRunHours:      row.TypicalRunHours,
			ServiceIntervalHours: row.ServiceIntervalHours,
			ServiceIntervalDays:  row.ServiceIntervalDays,
			Notes:                row.Notes, RetiredOn: row.RetiredOn,
			RetiredReason: row.RetiredReason, UpdatedAt: row.UpdatedAt,
		}, row.LocationName, last, 0)
		for _, sv := range services {
			out.Services = append(out.Services, serviceEventToProto(sv))
		}
		// What runs on it actually took. The median rather than the mean,
		// because one run that was left going over a weekend should not
		// move the estimate a scheduler builds on.
		durations, e := q.EquipmentRunDurations(ctx, uuid.NullUUID{UUID: id, Valid: true})
		if e != nil {
			return e
		}
		hours := make([]float64, 0, len(durations))
		for _, d := range durations {
			if h, ok := numericHours(d.Hours); ok && h > 0 {
				hours = append(hours, h)
			}
		}
		out.ObservedRuns = int32(len(hours))
		if len(hours) > 0 {
			sort.Float64s(hours)
			out.ObservedMedianHours = hours[len(hours)/2]
		}
		return nil
	})
	if err != nil {
		return nil, s.fail("GetEquipment", err)
	}
	return connect.NewResponse(out), nil
}

func (s *EquipmentService) DeleteEquipment(
	ctx context.Context,
	req *connect.Request[stillhousev1.DeleteEquipmentRequest],
) (*connect.Response[stillhousev1.DeleteEquipmentResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := q.DeleteEquipment(ctx, id); e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "equipment", id.String(),
			sqlcgen.AuditActionDelete, map[string]any{})
	}); err != nil {
		return nil, s.fail("DeleteEquipment", err)
	}
	return connect.NewResponse(&stillhousev1.DeleteEquipmentResponse{}), nil
}

func (s *EquipmentService) RecordService(
	ctx context.Context,
	req *connect.Request[stillhousev1.RecordServiceRequest],
) (*connect.Response[stillhousev1.RecordServiceResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	equipID, err := uuid.Parse(in.GetEquipmentId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid equipment_id"))
	}
	if strings.TrimSpace(in.GetDescription()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say what was done — a service record with no description "+
				"records that somebody was there, not what they did"))
	}
	performedOn, err := parseDateOrToday(in.GetPerformedOn())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	cost, err := optionalNumeric(in.GetCostCad(), "cost_cad")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	workOrderID, err := parseOptionalUUID(in.GetWorkOrderId(), "work_order_id")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var (
		out  sqlcgen.Equipment
		last pgtype.Date
		loc  string
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if _, e := q.RecordEquipmentService(ctx, sqlcgen.RecordEquipmentServiceParams{
			TenantID: u.TenantID, EquipmentID: equipID, PerformedOn: performedOn,
			Description: in.GetDescription(), PerformedBy: in.GetPerformedBy(),
			HoursAtService: optFloat(in.GetHoursAtService(), in.GetHoursAtServiceSet()),
			CostCad:        cost, WorkOrderID: workOrderID,
			Notes: in.GetNotes(), RecordedBy: u.ID,
		}); e != nil {
			return e
		}
		row, e := q.GetEquipment(ctx, equipID)
		if e != nil {
			return e
		}
		loc = row.LocationName
		out = sqlcgen.Equipment{
			ID: row.ID, Name: row.Name, Kind: row.Kind, Status: row.Status,
			LocationID: row.LocationID, Manufacturer: row.Manufacturer,
			Model: row.Model, SerialNo: row.SerialNo,
			CommissionedOn: row.CommissionedOn, CapacityL: row.CapacityL,
			TypicalRunHours:      row.TypicalRunHours,
			ServiceIntervalHours: row.ServiceIntervalHours,
			ServiceIntervalDays:  row.ServiceIntervalDays,
			Notes:                row.Notes, RetiredOn: row.RetiredOn,
			RetiredReason: row.RetiredReason, UpdatedAt: row.UpdatedAt,
		}
		last = performedOn
		return audit.Write(ctx, q, u.TenantID, u.ID, "equipment", equipID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"serviced":     performedOn.Time.Format("2006-01-02"),
				"description":  in.GetDescription(),
				"performed_by": in.GetPerformedBy(),
			})
	})
	if err != nil {
		return nil, s.fail("RecordService", err)
	}
	return connect.NewResponse(&stillhousev1.RecordServiceResponse{
		Equipment: equipmentToProto(out, loc, last, 0),
	}), nil
}

// --- converters ---

func equipmentToProto(
	e sqlcgen.Equipment, locationName string, lastServiced pgtype.Date, runCount int32,
) *stillhousev1.Equipment {
	out := &stillhousev1.Equipment{
		Id: e.ID.String(), Name: e.Name,
		Kind: equipmentKindToProto(e.Kind), Status: equipmentStatusToProto(e.Status),
		LocationId: nullUUIDString(e.LocationID), LocationName: locationName,
		Manufacturer: e.Manufacturer, Model: e.Model, SerialNo: e.SerialNo,
		CommissionedOn: formatDate(e.CommissionedOn),
		Notes:          e.Notes,
		RetiredOn:      formatDate(e.RetiredOn),
		RetiredReason:  e.RetiredReason,
		LastServicedOn: formatDate(lastServiced),
		RunCount:       runCount,
	}
	if e.CapacityL.Valid {
		out.CapacityL, out.CapacityLSet = e.CapacityL.Float64, true
	}
	if e.TypicalRunHours.Valid {
		out.TypicalRunHours, out.TypicalRunHoursSet = e.TypicalRunHours.Float64, true
	}
	if e.ServiceIntervalHours.Valid {
		out.ServiceIntervalHours, out.ServiceIntervalHoursSet = e.ServiceIntervalHours.Float64, true
	}
	if e.ServiceIntervalDays.Valid {
		out.ServiceIntervalDays, out.ServiceIntervalDaysSet = e.ServiceIntervalDays.Int32, true
	}
	if e.UpdatedAt.Valid {
		out.UpdatedAt = timestamppb.New(e.UpdatedAt.Time)
	}
	// Nothing is due without a recorded interval. A schedule Stillhouse
	// invented is one nobody agreed to, and the register shows plainly
	// that none is set.
	if e.ServiceIntervalDays.Valid && e.Status != sqlcgen.EquipmentStatusRetired {
		since := lastServiced
		if !since.Valid {
			since = e.CommissionedOn
		}
		if since.Valid {
			days := int32(time.Since(since.Time).Hours() / 24)
			if days < 0 {
				days = 0
			}
			out.DaysSinceService = days
			out.ServiceDue = days >= e.ServiceIntervalDays.Int32
		}
	}
	return out
}

func serviceEventToProto(s sqlcgen.EquipmentServiceEvent) *stillhousev1.ServiceEvent {
	out := &stillhousev1.ServiceEvent{
		Id: s.ID.String(), PerformedOn: formatDate(s.PerformedOn),
		Description: s.Description, PerformedBy: s.PerformedBy,
		WorkOrderId: nullUUIDString(s.WorkOrderID), Notes: s.Notes,
	}
	if s.HoursAtService.Valid {
		out.HoursAtService, out.HoursAtServiceSet = s.HoursAtService.Float64, true
	}
	if s.CostCad.Valid {
		out.CostCad = money.FromNumeric(s.CostCad).String(2)
	}
	return out
}

func optInt(v int32, set bool) pgtype.Int4 {
	return pgtype.Int4{Int32: v, Valid: set && v > 0}
}

func numericHours(v any) (float64, bool) {
	switch h := v.(type) {
	case float64:
		return h, true
	case pgtype.Numeric:
		return money.FromNumeric(h).Float(), h.Valid
	default:
		return 0, false
	}
}

func equipmentKindToProto(k sqlcgen.EquipmentKind) stillhousev1.EquipmentKind {
	if v, ok := equipmentKindProto[k]; ok {
		return v
	}
	return stillhousev1.EquipmentKind_EQUIPMENT_KIND_UNSPECIFIED
}

func equipmentKindToDB(k stillhousev1.EquipmentKind) (sqlcgen.EquipmentKind, error) {
	for db, pb := range equipmentKindProto {
		if pb == k {
			return db, nil
		}
	}
	return "", errors.New("say what kind of equipment it is")
}

var equipmentKindProto = map[sqlcgen.EquipmentKind]stillhousev1.EquipmentKind{
	sqlcgen.EquipmentKindStill:           stillhousev1.EquipmentKind_EQUIPMENT_KIND_STILL,
	sqlcgen.EquipmentKindMashTun:         stillhousev1.EquipmentKind_EQUIPMENT_KIND_MASH_TUN,
	sqlcgen.EquipmentKindFermenterVessel: stillhousev1.EquipmentKind_EQUIPMENT_KIND_FERMENTER_VESSEL,
	sqlcgen.EquipmentKindFiller:          stillhousev1.EquipmentKind_EQUIPMENT_KIND_FILLER,
	sqlcgen.EquipmentKindPump:            stillhousev1.EquipmentKind_EQUIPMENT_KIND_PUMP,
	sqlcgen.EquipmentKindChiller:         stillhousev1.EquipmentKind_EQUIPMENT_KIND_CHILLER,
	sqlcgen.EquipmentKindBoiler:          stillhousev1.EquipmentKind_EQUIPMENT_KIND_BOILER,
	sqlcgen.EquipmentKindCondenser:       stillhousev1.EquipmentKind_EQUIPMENT_KIND_CONDENSER,
	sqlcgen.EquipmentKindBottlingLine:    stillhousev1.EquipmentKind_EQUIPMENT_KIND_BOTTLING_LINE,
	sqlcgen.EquipmentKindOther:           stillhousev1.EquipmentKind_EQUIPMENT_KIND_OTHER,
}

func equipmentStatusToProto(s sqlcgen.EquipmentStatus) stillhousev1.EquipmentStatus {
	switch s {
	case sqlcgen.EquipmentStatusInService:
		return stillhousev1.EquipmentStatus_EQUIPMENT_STATUS_IN_SERVICE
	case sqlcgen.EquipmentStatusDown:
		return stillhousev1.EquipmentStatus_EQUIPMENT_STATUS_DOWN
	case sqlcgen.EquipmentStatusRetired:
		return stillhousev1.EquipmentStatus_EQUIPMENT_STATUS_RETIRED
	default:
		return stillhousev1.EquipmentStatus_EQUIPMENT_STATUS_UNSPECIFIED
	}
}

func equipmentStatusToDB(s stillhousev1.EquipmentStatus) (sqlcgen.EquipmentStatus, error) {
	switch s {
	case stillhousev1.EquipmentStatus_EQUIPMENT_STATUS_IN_SERVICE,
		stillhousev1.EquipmentStatus_EQUIPMENT_STATUS_UNSPECIFIED:
		return sqlcgen.EquipmentStatusInService, nil
	case stillhousev1.EquipmentStatus_EQUIPMENT_STATUS_DOWN:
		return sqlcgen.EquipmentStatusDown, nil
	case stillhousev1.EquipmentStatus_EQUIPMENT_STATUS_RETIRED:
		return sqlcgen.EquipmentStatusRetired, nil
	default:
		return "", errors.New("unknown status")
	}
}
