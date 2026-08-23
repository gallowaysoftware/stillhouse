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
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/excise"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

// EDM3-8-1's range. A vessel outside it is not a marked special
// container, whatever is written on the side.
const (
	markedContainerMinL = 100.0
	markedContainerMaxL = 1500.0
)

type MarkedContainerService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewMarkedContainerService(db *tenantdb.DB, logger *slog.Logger) *MarkedContainerService {
	return &MarkedContainerService{db: db, logger: logger}
}

func (s *MarkedContainerService) fail(op string, err error) error {
	var connectErr *connect.Error
	if errors.As(err, &connectErr) {
		return connectErr
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return connect.NewError(connect.CodeNotFound, errors.New("not found"))
	}
	if dup := uniqueViolation(err, "marked container"); dup != nil {
		return dup
	}
	s.logger.Error(op, "err", err)
	return connect.NewError(connect.CodeInternal, errors.New("internal error"))
}

// MarkContainer fills a container from bulk and marks it.
//
// A packaging act, so the bulk side sees the alcohol leave under
// transfer_to_packaging exactly as a bottling run does, and duty
// crystallises here if the licensee's duty point is at packaging. No
// excise stamp is drawn: these are marked, which is the distinction, and
// a licensee whose circumstances differ will see that none was consumed
// rather than discovering a silent one.
func (s *MarkedContainerService) MarkContainer(
	ctx context.Context,
	req *connect.Request[stillhousev1.MarkContainerRequest],
) (*connect.Response[stillhousev1.MarkContainerResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	mark := strings.TrimSpace(in.GetMark())
	if mark == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("record the mark applied to the container — an unmarked "+
				"container is not a marked special container, and what the mark "+
				"has to say is EDM3-8-1's to specify, not Stillhouse's"))
	}
	if in.GetCapacityL() < markedContainerMinL || in.GetCapacityL() > markedContainerMaxL {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"a marked special container is between %.0f and %.0f litres (EDM3-8-1); "+
				"%.1f L is something else", markedContainerMinL, markedContainerMaxL,
			in.GetCapacityL()))
	}
	if in.GetVolumeL() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say how much went in"))
	}
	if in.GetVolumeL() > in.GetCapacityL() {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"%.1f L will not fit in a %.0f L container", in.GetVolumeL(), in.GetCapacityL()))
	}
	sourceID, err := uuid.Parse(in.GetSourceContainerId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say which vessel it was drawn from"))
	}
	productID, err := parseOptionalUUID(in.GetProductId(), "product_id")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	filledOn, err := parseDateOrToday(in.GetFilledOn())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var (
		out      sqlcgen.MarkedSpecialContainer
		resolved resolvedStrength
		warnings []string
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := assertDateNotInLockedPeriod(ctx, q, filledOn); e != nil {
			return e
		}
		source, e := lockContainerForWrite(ctx, q, sourceID)
		if e != nil {
			return e
		}
		if !source.CurrentAbvPct.Valid || source.CurrentAbvPct.Float64 <= 0 {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("that vessel has no measurable strength to draw from"))
		}
		// Every quantity in Stillhouse lands at 20 °C through the same
		// path, and this one says which of the three ways it got there.
		resolved, e = resolveStrength(strengthInput{
			ObservedVolumeL: in.GetVolumeL(),
			DensityKgM3:     in.GetDensityKgM3(),
			DensityIsSet:    in.GetDensityKgM3Set(),
			TemperatureC:    in.GetTemperatureC(),
			TemperatureSet:  in.GetTemperatureCSet(),
			AbvPct:          in.GetAbvPct(),
		})
		if e != nil {
			return asRateRefusal(e)
		}
		abv := resolved.StrengthPct20C
		if abv <= 0 {
			abv = source.CurrentAbvPct.Float64
			warnings = append(warnings,
				"No strength was given for the fill, so the vessel's own was "+
					"carried forward. Gauge the container if the figure matters.")
		}
		volume := resolved.VolumeL20C
		if volume <= 0 {
			volume = in.GetVolumeL()
		}
		laa := volume * abv / 100
		if laa > source.CurrentLaa+1e-9 {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"%s holds %.4f LAA; this fill asks for %.4f",
				source.Name, source.CurrentLaa, laa))
		}

		// Duty at the licensee's own duty point, decided by the same code
		// a bottling run uses — the two must never disagree about when
		// duty crystallises.
		basis, e := tenantDutyBasis(ctx, q, u.TenantID)
		if e != nil {
			return e
		}
		var dutyRate, dutyAmount pgtype.Float8
		var dutySource string
		if basis.dutiesAtPackaging(filledOn.Time) {
			band, be := excise.RateOn(filledOn.Time)
			if be != nil {
				return asRateRefusal(be)
			}
			rate, amount, oe := excise.Owed(filledOn.Time, volume, abv)
			if oe != nil {
				return asRateRefusal(oe)
			}
			dutyRate = pgtype.Float8{Float64: rate, Valid: true}
			dutyAmount = pgtype.Float8{Float64: amount, Valid: true}
			dutySource = band.Source
		}

		mv, e := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
			TenantID:          u.TenantID,
			SourceContainerID: uuid.NullUUID{UUID: sourceID, Valid: true},
			VolumeL:           volume,
			AbvPct:            abv,
			Laa:               laa,
			Reason:            sqlcgen.BulkMovementReasonTransferToPackaging,
			ReferenceType:     "marked_special_container",
			Notes:             "marked special container fill",
			OccurredAt:        pgtype.Timestamptz{Valid: true, Time: dayStart(filledOn.Time)},
		})
		if e != nil {
			return e
		}
		if _, e = q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
			ID:             sourceID,
			CurrentVolumeL: source.CurrentVolumeL - volume,
			CurrentAbvPct:  source.CurrentAbvPct,
			CurrentLaa:     source.CurrentLaa - laa,
		}); e != nil {
			return e
		}

		if e := q.LockDocumentSequence(ctx, "marked_special_containers"); e != nil {
			return e
		}
		nextNo, e := q.NextMarkedContainerNo(ctx)
		if e != nil {
			return e
		}
		out, e = q.CreateMarkedContainer(ctx, sqlcgen.CreateMarkedContainerParams{
			TenantID: u.TenantID, ContainerNo: nextNo, Mark: mark,
			CapacityL: in.GetCapacityL(), ProductID: productID,
			Description: in.GetDescription(), SourceContainerID: sourceID,
			VolumeL: volume, AbvPct: abv, Laa: laa,
			FilledOn: filledOn, FilledBy: uuid.NullUUID{UUID: u.ID, Valid: true},
			BulkMovementID: uuid.NullUUID{UUID: mv.ID, Valid: true},
			DutyRatePerLaa: dutyRate,
			DutyAmountCad:  dutyAmount,
			DutyRateSource: dutySource,
			Notes:          in.GetNotes(),
			CreatedBy:      u.ID,
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "marked_special_container",
			out.ID.String(), sqlcgen.AuditActionCreate, map[string]any{
				"container_no":        out.ContainerNo,
				"mark":                mark,
				"capacity_l":          in.GetCapacityL(),
				"laa":                 laa,
				"source":              source.Name,
				"duty_cad":            dutyAmount.Float64,
				"dutied_at_packaging": dutyAmount.Valid,
			})
	})
	if err != nil {
		return nil, s.fail("MarkContainer", err)
	}
	return connect.NewResponse(&stillhousev1.MarkContainerResponse{
		Container:       markedContainerToProto(out, "", ""),
		StrengthSource:  resolved.Source,
		StrengthPct_20C: resolved.StrengthPct20C,
		VolumeL_20C:     resolved.VolumeL20C,
		Warnings:        warnings,
	}), nil
}

func (s *MarkedContainerService) ListMarkedContainers(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListMarkedContainersRequest],
) (*connect.Response[stillhousev1.ListMarkedContainersResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.ListMarkedContainersRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListMarkedContainers(ctx, req.Msg.GetOnHandOnly())
		return e
	}); err != nil {
		return nil, s.fail("ListMarkedContainers", err)
	}
	out := &stillhousev1.ListMarkedContainersResponse{}
	for _, r := range rows {
		c := markedContainerToProto(sqlcgen.MarkedSpecialContainer{
			ID: r.ID, ContainerNo: r.ContainerNo, Mark: r.Mark,
			CapacityL: r.CapacityL, ProductID: r.ProductID,
			Description: r.Description, Status: r.Status,
			SourceContainerID: r.SourceContainerID, VolumeL: r.VolumeL,
			AbvPct: r.AbvPct, Laa: r.Laa, FilledOn: r.FilledOn,
			DutyRatePerLaa: r.DutyRatePerLaa, DutyAmountCad: r.DutyAmountCad,
			DutyRateSource: r.DutyRateSource, Notes: r.Notes,
			UnmarkedOn: r.UnmarkedOn, UnmarkedReason: r.UnmarkedReason,
			CreatedAt: r.CreatedAt,
		}, r.ProductName, r.SourceContainerName)
		out.Containers = append(out.Containers, c)
		if r.Status == sqlcgen.MarkedContainerStatusMarked {
			out.OnHandLaa += r.Laa
			out.OnHandCount++
		}
	}
	return connect.NewResponse(out), nil
}

func (s *MarkedContainerService) GetMarkedContainer(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetMarkedContainerRequest],
) (*connect.Response[stillhousev1.GetMarkedContainerResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	var row sqlcgen.GetMarkedContainerRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		row, e = q.GetMarkedContainer(ctx, id)
		return e
	}); err != nil {
		return nil, s.fail("GetMarkedContainer", err)
	}
	return connect.NewResponse(&stillhousev1.GetMarkedContainerResponse{
		Container: markedContainerToProto(sqlcgen.MarkedSpecialContainer{
			ID: row.ID, ContainerNo: row.ContainerNo, Mark: row.Mark,
			CapacityL: row.CapacityL, ProductID: row.ProductID,
			Description: row.Description, Status: row.Status,
			SourceContainerID: row.SourceContainerID, VolumeL: row.VolumeL,
			AbvPct: row.AbvPct, Laa: row.Laa, FilledOn: row.FilledOn,
			DutyRatePerLaa: row.DutyRatePerLaa, DutyAmountCad: row.DutyAmountCad,
			DutyRateSource: row.DutyRateSource, Notes: row.Notes,
			UnmarkedOn: row.UnmarkedOn, UnmarkedReason: row.UnmarkedReason,
			CreatedAt: row.CreatedAt,
		}, row.ProductName, row.SourceContainerName),
	}), nil
}

// UnmarkContainer is s.156: the mark comes off and the contents go back
// to bulk.
//
// A movement in the ledger, not a correction. The alcohol really did go
// back, so the bulk side receives it under packaged_returned_to_bulk —
// the reason that already exists for exactly this shape of event — and
// the container drops out of the packaging figures rather than being
// deleted from them.
func (s *MarkedContainerService) UnmarkContainer(
	ctx context.Context,
	req *connect.Request[stillhousev1.UnmarkContainerRequest],
) (*connect.Response[stillhousev1.UnmarkContainerResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	id, err := uuid.Parse(in.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	destID, err := uuid.Parse(in.GetDestinationContainerId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say which vessel the contents go back to"))
	}
	reason := strings.TrimSpace(in.GetReason())
	if reason == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say why the container was unmarked — it moves alcohol back "+
				"out of the packaging figures and the return has to explain it"))
	}
	unmarkedOn, err := parseDateOrToday(in.GetUnmarkedOn())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var (
		out      sqlcgen.MarkedSpecialContainer
		returned float64
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := assertDateNotInLockedPeriod(ctx, q, unmarkedOn); e != nil {
			return e
		}
		c, e := q.GetMarkedContainerForUpdate(ctx, id)
		if e != nil {
			return e
		}
		if c.Status != sqlcgen.MarkedContainerStatusMarked {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"container %d is %s — only one still on the premises can be unmarked",
				c.ContainerNo, c.Status))
		}
		dest, e := lockContainerForWrite(ctx, q, destID)
		if e != nil {
			return e
		}
		returned = c.Laa

		mv, e := q.InsertBulkMovement(ctx, sqlcgen.InsertBulkMovementParams{
			TenantID:               u.TenantID,
			DestinationContainerID: uuid.NullUUID{UUID: destID, Valid: true},
			VolumeL:                c.VolumeL,
			AbvPct:                 c.AbvPct,
			Laa:                    c.Laa,
			Reason:                 sqlcgen.BulkMovementReasonPackagedReturnedToBulk,
			ReferenceType:          "marked_special_container_unmark",
			ReferenceID:            uuid.NullUUID{UUID: id, Valid: true},
			Notes:                  reason,
			OccurredAt:             pgtype.Timestamptz{Valid: true, Time: dayStart(unmarkedOn.Time)},
		})
		if e != nil {
			return e
		}
		// Blending back into a vessel that already holds spirit: the
		// strength is the volume-weighted result, the same arithmetic
		// every other receipt into a container uses.
		newVolume := dest.CurrentVolumeL + c.VolumeL
		newLAA := dest.CurrentLaa + c.Laa
		newABV := dest.CurrentAbvPct.Float64
		if newVolume > 0 {
			newABV = newLAA / newVolume * 100
		}
		if _, e = q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
			ID:             destID,
			CurrentVolumeL: newVolume,
			CurrentAbvPct:  pgtype.Float8{Float64: newABV, Valid: true},
			CurrentLaa:     newLAA,
		}); e != nil {
			return e
		}
		out, e = q.UnmarkMarkedContainer(ctx, sqlcgen.UnmarkMarkedContainerParams{
			ID: id, UnmarkedOn: unmarkedOn, UnmarkedReason: reason,
			UnmarkMovementID: uuid.NullUUID{UUID: mv.ID, Valid: true},
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "marked_special_container",
			id.String(), sqlcgen.AuditActionUpdate, map[string]any{
				"container_no": out.ContainerNo,
				"unmarked":     true,
				"reason":       reason,
				"laa_returned": c.Laa,
				"destination":  dest.Name,
			})
	})
	if err != nil {
		return nil, s.fail("UnmarkContainer", err)
	}
	return connect.NewResponse(&stillhousev1.UnmarkContainerResponse{
		Container:   markedContainerToProto(out, "", ""),
		ReturnedLaa: returned,
	}), nil
}

func (s *MarkedContainerService) DeliverMarkedContainer(
	ctx context.Context,
	req *connect.Request[stillhousev1.DeliverMarkedContainerRequest],
) (*connect.Response[stillhousev1.DeliverMarkedContainerResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	id, err := uuid.Parse(in.GetContainerId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid container_id"))
	}
	customerID, err := parseOptionalUUID(in.GetCustomerId(), "customer_id")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	deliveryDate, err := parseDateOrToday(in.GetDeliveryDate())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	destName := strings.TrimSpace(in.GetDestinationName())
	if !customerID.Valid && destName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say who it went to — a marked container is delivered to a "+
				"registered user or to bottle-your-own premises, and the return "+
				"has to name them"))
	}

	var (
		delivery  sqlcgen.MarkedContainerDelivery
		container sqlcgen.MarkedSpecialContainer
		dutyCAD   float64
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := assertDateNotInLockedPeriod(ctx, q, deliveryDate); e != nil {
			return e
		}
		c, e := q.GetMarkedContainerForUpdate(ctx, id)
		if e != nil {
			return e
		}
		if c.Status != sqlcgen.MarkedContainerStatusMarked {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"container %d is %s", c.ContainerNo, c.Status))
		}
		if customerID.Valid {
			cust, ce := q.GetCustomer(ctx, customerID.UUID)
			if ce != nil {
				if errors.Is(ce, pgx.ErrNoRows) {
					return connect.NewError(connect.CodeNotFound, errors.New("customer not found"))
				}
				return ce
			}
			if cust.ArchivedAt.Valid {
				return connect.NewError(connect.CodeFailedPrecondition,
					fmt.Errorf("%s is archived", cust.Name))
			}
			destName = cust.Name
		}

		// Duty here only if it was not taken at the fill. The test is the
		// container's own duty amount rather than a date comparison, so
		// the two sides can never drift — the same argument recordRemoval
		// makes about a bottling run.
		var ratePerLAA float64
		if !c.DutyAmountCad.Valid {
			rate, amount, oe := excise.Owed(deliveryDate.Time, c.VolumeL, c.AbvPct)
			if oe != nil {
				return asRateRefusal(oe)
			}
			ratePerLAA, dutyCAD = rate, amount
		}

		if e := q.LockDocumentSequence(ctx, "marked_container_deliveries"); e != nil {
			return e
		}
		nextNo, e := q.NextMarkedDeliveryNo(ctx)
		if e != nil {
			return e
		}
		delivery, e = q.CreateMarkedDelivery(ctx, sqlcgen.CreateMarkedDeliveryParams{
			TenantID: u.TenantID, DeliveryNo: nextNo, ContainerID: id,
			DeliveryDate: deliveryDate, CustomerID: customerID,
			DestinationName: destName, Reference: in.GetReference(),
			VolumeL: c.VolumeL, AbvPct: c.AbvPct, Laa: c.Laa,
			DutyRatePerLaa: ratePerLAA, DutyAmountCad: dutyCAD,
			Notes: in.GetNotes(), CreatedBy: u.ID,
		})
		if e != nil {
			return e
		}
		container, e = q.SetMarkedContainerStatus(ctx, sqlcgen.SetMarkedContainerStatusParams{
			ID: id, Status: sqlcgen.MarkedContainerStatusDelivered,
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "marked_container_delivery",
			delivery.ID.String(), sqlcgen.AuditActionCreate, map[string]any{
				"delivery_no":         delivery.DeliveryNo,
				"container_no":        c.ContainerNo,
				"mark":                c.Mark,
				"destination":         destName,
				"laa":                 c.Laa,
				"duty_cad":            dutyCAD,
				"dutied_at_packaging": c.DutyAmountCad.Valid,
			})
	})
	if err != nil {
		return nil, s.fail("DeliverMarkedContainer", err)
	}
	return connect.NewResponse(&stillhousev1.DeliverMarkedContainerResponse{
		Delivery:  markedDeliveryToProto(delivery, container.ContainerNo, container.Mark, destName),
		Container: markedContainerToProto(container, "", ""),
		DutyCad:   dutyCAD,
	}), nil
}

func (s *MarkedContainerService) ListMarkedDeliveries(
	ctx context.Context,
	_ *connect.Request[stillhousev1.ListMarkedDeliveriesRequest],
) (*connect.Response[stillhousev1.ListMarkedDeliveriesResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.ListMarkedDeliveriesRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListMarkedDeliveries(ctx)
		return e
	}); err != nil {
		return nil, s.fail("ListMarkedDeliveries", err)
	}
	out := &stillhousev1.ListMarkedDeliveriesResponse{}
	for _, r := range rows {
		name := r.CustomerName
		if name == "" {
			name = r.DestinationName
		}
		out.Deliveries = append(out.Deliveries, markedDeliveryToProto(
			sqlcgen.MarkedContainerDelivery{
				ID: r.ID, DeliveryNo: r.DeliveryNo, ContainerID: r.ContainerID,
				DeliveryDate: r.DeliveryDate, CustomerID: r.CustomerID,
				DestinationName: r.DestinationName, Reference: r.Reference,
				VolumeL: r.VolumeL, AbvPct: r.AbvPct, Laa: r.Laa,
				DutyRatePerLaa: r.DutyRatePerLaa, DutyAmountCad: r.DutyAmountCad,
				Notes: r.Notes,
			}, r.ContainerNo, r.Mark, name))
	}
	return connect.NewResponse(out), nil
}

func (s *MarkedContainerService) VoidMarkedDelivery(
	ctx context.Context,
	req *connect.Request[stillhousev1.VoidMarkedDeliveryRequest],
) (*connect.Response[stillhousev1.VoidMarkedDeliveryResponse], error) {
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
			errors.New("say why the delivery was voided"))
	}
	var out sqlcgen.MarkedContainerDelivery
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		out, e = q.VoidMarkedDelivery(ctx, sqlcgen.VoidMarkedDeliveryParams{
			ID: id, VoidReason: reason,
		})
		if errors.Is(e, pgx.ErrNoRows) {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("that delivery is already void"))
		}
		if e != nil {
			return e
		}
		// The container comes back on hand: a voided delivery says it
		// never left, and leaving it marked delivered would put it in
		// neither the on-hand figure nor the delivered one.
		if _, e := q.SetMarkedContainerStatusForce(ctx, sqlcgen.SetMarkedContainerStatusForceParams{
			ID: out.ContainerID, Status: sqlcgen.MarkedContainerStatusMarked,
		}); e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "marked_container_delivery",
			id.String(), sqlcgen.AuditActionUpdate, map[string]any{
				"delivery_no": out.DeliveryNo, "voided": true, "reason": reason,
			})
	})
	if err != nil {
		return nil, s.fail("VoidMarkedDelivery", err)
	}
	return connect.NewResponse(&stillhousev1.VoidMarkedDeliveryResponse{
		Delivery: markedDeliveryToProto(out, 0, "", out.DestinationName),
	}), nil
}

// --- converters ---

func markedContainerToProto(
	c sqlcgen.MarkedSpecialContainer, productName, sourceName string,
) *stillhousev1.MarkedContainer {
	out := &stillhousev1.MarkedContainer{
		Id: c.ID.String(), ContainerNo: c.ContainerNo, Mark: c.Mark,
		CapacityL: c.CapacityL, ProductId: nullUUIDString(c.ProductID),
		ProductName: productName, Description: c.Description,
		Status:              markedStatusToProto(c.Status),
		SourceContainerId:   c.SourceContainerID.String(),
		SourceContainerName: sourceName,
		VolumeL:             round4(c.VolumeL),
		AbvPct:              round2(c.AbvPct),
		Laa:                 round4(c.Laa),
		FilledOn:            formatDate(c.FilledOn),
		Notes:               c.Notes,
		UnmarkedOn:          formatDate(c.UnmarkedOn),
		UnmarkedReason:      c.UnmarkedReason,
		DutyRateSource:      c.DutyRateSource,
	}
	// Absent is not zero: a container that was not a duty event and one
	// that cost nothing are different statements.
	if c.DutyAmountCad.Valid {
		out.DutySet = true
		out.DutyAmountCad = c.DutyAmountCad.Float64
		out.DutyRatePerLaa = c.DutyRatePerLaa.Float64
	}
	if c.CreatedAt.Valid {
		out.CreatedAt = timestamppb.New(c.CreatedAt.Time)
	}
	return out
}

func markedDeliveryToProto(
	d sqlcgen.MarkedContainerDelivery, containerNo int32, mark, customerName string,
) *stillhousev1.MarkedDelivery {
	return &stillhousev1.MarkedDelivery{
		Id: d.ID.String(), DeliveryNo: d.DeliveryNo,
		ContainerId: d.ContainerID.String(), ContainerNo: containerNo, Mark: mark,
		DeliveryDate: formatDate(d.DeliveryDate),
		CustomerId:   nullUUIDString(d.CustomerID),
		CustomerName: customerName, DestinationName: d.DestinationName,
		Reference: d.Reference,
		VolumeL:   round4(d.VolumeL), AbvPct: round2(d.AbvPct), Laa: round4(d.Laa),
		DutyRatePerLaa: d.DutyRatePerLaa, DutyAmountCad: d.DutyAmountCad,
		Notes: d.Notes,
	}
}

func markedStatusToProto(s sqlcgen.MarkedContainerStatus) stillhousev1.MarkedContainerStatus {
	switch s {
	case sqlcgen.MarkedContainerStatusMarked:
		return stillhousev1.MarkedContainerStatus_MARKED_CONTAINER_STATUS_MARKED
	case sqlcgen.MarkedContainerStatusDelivered:
		return stillhousev1.MarkedContainerStatus_MARKED_CONTAINER_STATUS_DELIVERED
	case sqlcgen.MarkedContainerStatusUnmarked:
		return stillhousev1.MarkedContainerStatus_MARKED_CONTAINER_STATUS_UNMARKED
	case sqlcgen.MarkedContainerStatusDestroyed:
		return stillhousev1.MarkedContainerStatus_MARKED_CONTAINER_STATUS_DESTROYED
	default:
		return stillhousev1.MarkedContainerStatus_MARKED_CONTAINER_STATUS_UNSPECIFIED
	}
}
