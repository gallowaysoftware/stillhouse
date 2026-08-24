package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type KegService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewKegService(db *tenantdb.DB, logger *slog.Logger) *KegService {
	return &KegService{db: db, logger: logger}
}

// kegTransition is what one event does to a keg.
//
// The cycle is written as a table rather than as a chain of ifs because
// the illegal transitions are the point: filling a keg that is already
// full loses track of the first fill's spirits, and shipping one that is
// already at a customer means the register has two customers holding the
// same physical asset. A table makes "which moves are allowed from here"
// a thing you can read.
type kegTransition struct {
	from []sqlcgen.KegStatus
	to   sqlcgen.KegStatus
	// depositSign is +1 when the customer puts a deposit down, -1 when it
	// comes back, 0 when nothing moves.
	depositSign  int
	needCustomer bool
	needContents bool
	// clearsContents is true where the keg stops holding spirits. Set
	// separately from needContents because returning and condemning both
	// empty it and neither takes a marked container as input.
	clearsContents bool
}

var kegTransitions = map[sqlcgen.KegEventKind]kegTransition{
	sqlcgen.KegEventKindAcquired: {
		from: []sqlcgen.KegStatus{sqlcgen.KegStatusAvailable},
		to:   sqlcgen.KegStatusAvailable,
	},
	sqlcgen.KegEventKindFilled: {
		// Only from clean and empty. A dirty keg is not fillable, which
		// is the entire reason returned_dirty is a status of its own.
		from:         []sqlcgen.KegStatus{sqlcgen.KegStatusAvailable},
		to:           sqlcgen.KegStatusFilled,
		needContents: true,
	},
	sqlcgen.KegEventKindShipped: {
		from:         []sqlcgen.KegStatus{sqlcgen.KegStatusFilled},
		to:           sqlcgen.KegStatusAtCustomer,
		depositSign:  +1,
		needCustomer: true,
	},
	sqlcgen.KegEventKindReturned: {
		from:           []sqlcgen.KegStatus{sqlcgen.KegStatusAtCustomer},
		to:             sqlcgen.KegStatusReturnedDirty,
		depositSign:    -1,
		clearsContents: true,
	},
	sqlcgen.KegEventKindCleaned: {
		from: []sqlcgen.KegStatus{sqlcgen.KegStatusReturnedDirty, sqlcgen.KegStatusOutOfService},
		to:   sqlcgen.KegStatusAvailable,
	},
	sqlcgen.KegEventKindCondemned: {
		from: []sqlcgen.KegStatus{
			sqlcgen.KegStatusAvailable, sqlcgen.KegStatusReturnedDirty, sqlcgen.KegStatusFilled,
		},
		to:             sqlcgen.KegStatusOutOfService,
		clearsContents: true,
	},
	sqlcgen.KegEventKindLost: {
		from: []sqlcgen.KegStatus{
			sqlcgen.KegStatusAtCustomer, sqlcgen.KegStatusAvailable, sqlcgen.KegStatusFilled,
		},
		to: sqlcgen.KegStatusLost,
		// The deposit is forfeit, not refunded — it stays outstanding
		// against the customer, which is what a deposit is for.
		clearsContents: true,
	},
}

func (s *KegService) ListKegs(
	ctx context.Context,
	_ *connect.Request[stillhousev1.ListKegsRequest],
) (*connect.Response[stillhousev1.ListKegsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	out := &stillhousev1.ListKegsResponse{}
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		rows, e := q.ListKegs(ctx)
		if e != nil {
			return e
		}
		for _, r := range rows {
			out.Kegs = append(out.Kegs, kegRowToProto(r))
		}
		sum, e := q.KegRegisterSummary(ctx)
		if e != nil {
			return e
		}
		out.Total, out.Available, out.Filled = sum.Total, sum.Available, sum.Filled
		out.NonKeg = sum.NonKeg
		out.AtCustomer, out.ReturnedDirty = sum.AtCustomer, sum.ReturnedDirty
		out.OutOfService, out.Lost = sum.OutOfService, sum.Lost

		deps, e := q.KegDepositLiability(ctx)
		if e != nil {
			return e
		}
		for _, d := range deps {
			amt := numericToFloat(d.OutstandingCad)
			out.Deposits = append(out.Deposits, &stillhousev1.KegDepositLine{
				CustomerId:     d.CustomerID.UUID.String(),
				CustomerName:   d.CustomerName,
				OutstandingCad: amt,
				KegsShipped:    d.KegsShipped,
				KegsReturned:   d.KegsReturned,
			})
			out.TotalOutstandingDepositsCad += amt
		}
		out.TotalOutstandingDepositsCad = round2(out.TotalOutstandingDepositsCad)
		return nil
	}); err != nil {
		s.logger.Error("ListKegs", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(out), nil
}

func (s *KegService) CreateKeg(
	ctx context.Context,
	req *connect.Request[stillhousev1.CreateKegRequest],
) (*connect.Response[stillhousev1.CreateKegResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	serial := strings.TrimSpace(req.Msg.GetSerial())
	if serial == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("a keg needs its serial — the register is useless without one"))
	}
	// Capacity is a keg's defining number — it decides which register its
	// contents live in (EDM3-8-1's 100 L threshold, stage 199). A pallet
	// has no meaningful capacity and is not asked for one.
	kind := returnableKindFromProto(req.Msg.GetKind())
	if kind == sqlcgen.ReturnableKindKeg && req.Msg.GetCapacityL() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("a keg's capacity decides whether its contents are a marked special container or packaged spirits, so it is required"))
	}

	var cost, deposit pgtype.Numeric
	if req.Msg.GetPurchaseCostSet() {
		_ = cost.Scan(strconv.FormatFloat(req.Msg.GetPurchaseCostCad(), 'f', 2, 64))
	}
	if req.Msg.GetDepositSet() {
		_ = deposit.Scan(strconv.FormatFloat(req.Msg.GetDepositCad(), 'f', 2, 64))
	}
	var purchased pgtype.Date
	if v := req.Msg.GetPurchasedOn(); v != "" {
		d, err := parseDate(v, "purchased_on")
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		purchased = pgtype.Date{Valid: true, Time: d}
	}

	var row sqlcgen.Keg
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		r, e := q.CreateKeg(ctx, sqlcgen.CreateKegParams{
			TenantID: u.TenantID, Serial: serial,
			CapacityL: pgtype.Float8{Float64: req.Msg.GetCapacityL(), Valid: req.Msg.GetCapacityL() > 0},
			Material:  req.Msg.GetMaterial(), PurchaseCostCad: cost, DepositCad: deposit,
			PurchasedOn: purchased, Notes: req.Msg.GetNotes(),
			Kind: returnableKindFromProto(req.Msg.GetKind()),
		})
		if e != nil {
			return e
		}
		row = r
		if _, e := q.RecordKegEvent(ctx, sqlcgen.RecordKegEventParams{
			TenantID: u.TenantID, KegID: r.ID, Kind: sqlcgen.KegEventKindAcquired,
			OccurredOn: firstValidDate(purchased), UserID: uuid.NullUUID{UUID: u.ID, Valid: true},
		}); e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "keg", r.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{"serial": serial})
	}); err != nil {
		if ce := uniqueViolation(err, "keg serial"); ce != nil {
			return nil, ce
		}
		s.logger.Error("CreateKeg", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.CreateKegResponse{Keg: kegToProto(row)}), nil
}

// MoveKeg advances one keg through the cycle. Every transition writes its
// event in the same transaction as the status change, so the register can
// never say where a keg is without saying how it got there.
func (s *KegService) MoveKeg(
	ctx context.Context,
	req *connect.Request[stillhousev1.MoveKegRequest],
) (*connect.Response[stillhousev1.MoveKegResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	kegID, err := uuid.Parse(req.Msg.GetKegId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid keg_id"))
	}
	kind, err := kegEventKindFromProto(req.Msg.GetKind())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	tr, ok := kegTransitions[kind]
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown keg event"))
	}
	on, err := parseDate(req.Msg.GetOccurredOn(), "occurred_on")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var (
		row   sqlcgen.Keg
		delta pgtype.Numeric
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		keg, e := q.GetKeg(ctx, kegID)
		if e != nil {
			return e
		}
		if !kegAllowed(tr.from, keg.Status) {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"a keg that is %s cannot be %s — allowed from: %s",
				keg.Status, kind, kegStatusList(tr.from)))
		}

		var customer uuid.NullUUID
		if tr.needCustomer {
			id, e := uuid.Parse(req.Msg.GetCustomerId())
			if e != nil {
				return connect.NewError(connect.CodeInvalidArgument,
					errors.New("shipping a keg needs the customer it went to — the deposit is owed by somebody"))
			}
			customer = uuid.NullUUID{UUID: id, Valid: true}
		} else if kind != sqlcgen.KegEventKindReturned {
			customer = uuid.NullUUID{}
		} else {
			// A return keeps the customer on the event so the deposit
			// refund is attributable, but clears it from the keg.
			customer = keg.CurrentCustomerID
		}

		marked, packaged := keg.MarkedContainerID, keg.PackagedInventoryID
		if tr.needContents {
			var e error
			marked, packaged, e = kegContentsFor(keg, req.Msg)
			if e != nil {
				return e
			}
		}
		if tr.clearsContents {
			marked, packaged = uuid.NullUUID{}, uuid.NullUUID{}
		}

		// The deposit, from the keg's own figure. Zero-value deposits
		// write no event amount at all rather than a zero: a keg with no
		// deposit set is not a keg with a nil deposit.
		if tr.depositSign != 0 && keg.DepositCad.Valid {
			amt := numericToFloat(keg.DepositCad) * float64(tr.depositSign)
			_ = delta.Scan(strconv.FormatFloat(amt, 'f', 2, 64))
		}

		var filled, returned pgtype.Date
		if kind == sqlcgen.KegEventKindFilled {
			filled = pgtype.Date{Valid: true, Time: on}
		}
		if kind == sqlcgen.KegEventKindReturned {
			returned = pgtype.Date{Valid: true, Time: on}
		}

		keg, e = q.SetKegState(ctx, sqlcgen.SetKegStateParams{
			ID:                  kegID,
			Status:              tr.to,
			CustomerID:          kegCustomerFor(kind, customer),
			LocationID:          keg.CurrentLocationID,
			MarkedContainerID:   marked,
			PackagedInventoryID: packaged,
			LastFilledOn:        filled,
			LastReturnedOn:      returned,
		})
		if e != nil {
			return e
		}
		row = keg

		if _, e := q.RecordKegEvent(ctx, sqlcgen.RecordKegEventParams{
			TenantID: u.TenantID, KegID: kegID, Kind: kind,
			OccurredOn:          pgtype.Date{Valid: true, Time: on},
			CustomerID:          customer,
			MarkedContainerID:   marked,
			PackagedInventoryID: packaged,
			DepositDeltaCad:     delta,
			Notes:               req.Msg.GetNotes(),
			UserID:              uuid.NullUUID{UUID: u.ID, Valid: true},
		}); e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "keg", kegID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"serial": keg.Serial, "event": string(kind), "status": string(tr.to),
			})
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("keg not found"))
		}
		s.logger.Error("MoveKeg", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	resp := &stillhousev1.MoveKegResponse{Keg: kegToProto(row)}
	if delta.Valid {
		resp.DepositDeltaCad = numericToFloat(delta)
		resp.DepositDeltaSet = true
	}
	return connect.NewResponse(resp), nil
}

func (s *KegService) ListKegEvents(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListKegEventsRequest],
) (*connect.Response[stillhousev1.ListKegEventsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetKegId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid keg_id"))
	}
	out := &stillhousev1.ListKegEventsResponse{}
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		rows, e := q.ListKegEvents(ctx, id)
		if e != nil {
			return e
		}
		for _, r := range rows {
			ev := &stillhousev1.KegEvent{
				Id:           r.ID.String(),
				Kind:         kegEventKindToProto(r.Kind),
				OccurredOn:   r.OccurredOn.Time.Format("2006-01-02"),
				CustomerName: r.CustomerName,
				Notes:        r.Notes,
			}
			if r.DepositDeltaCad.Valid {
				ev.DepositDeltaCad = numericToFloat(r.DepositDeltaCad)
				ev.DepositDeltaSet = true
			}
			out.Events = append(out.Events, ev)
		}
		return nil
	}); err != nil {
		s.logger.Error("ListKegEvents", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(out), nil
}

// kegCustomerFor decides what goes on the KEG (not the event). A return
// clears the customer — the keg is back — while the event keeps them, so
// the refund stays attributable.
func kegCustomerFor(kind sqlcgen.KegEventKind, eventCustomer uuid.NullUUID) uuid.NullUUID {
	if kind == sqlcgen.KegEventKindShipped {
		return eventCustomer
	}
	if kind == sqlcgen.KegEventKindLost {
		// Whoever had it still has it, as far as anyone knows.
		return eventCustomer
	}
	return uuid.NullUUID{}
}

func kegAllowed(from []sqlcgen.KegStatus, cur sqlcgen.KegStatus) bool {
	for _, f := range from {
		if f == cur {
			return true
		}
	}
	return false
}

func kegStatusList(ss []sqlcgen.KegStatus) string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, string(s))
	}
	return strings.Join(out, ", ")
}

func firstValidDate(d pgtype.Date) pgtype.Date {
	if d.Valid {
		return d
	}
	return pgtype.Date{Valid: true, Time: nowUTCDate()}
}

func nowUTCDate() time.Time {
	return time.Now().UTC()
}

func kegRowToProto(r sqlcgen.ListKegsRow) *stillhousev1.Keg {
	out := kegToProto(sqlcgen.Keg{
		ID: r.ID, Serial: r.Serial, CapacityL: r.CapacityL, Material: r.Material,
		PurchaseCostCad: r.PurchaseCostCad, DepositCad: r.DepositCad,
		PurchasedOn: r.PurchasedOn, Status: r.Status,
		CurrentCustomerID: r.CurrentCustomerID, MarkedContainerID: r.MarkedContainerID,
		PackagedInventoryID: r.PackagedInventoryID, Kind: r.Kind,
		LastFilledOn: r.LastFilledOn, LastReturnedOn: r.LastReturnedOn, Notes: r.Notes,
	})
	out.CustomerName = r.CustomerName
	out.LocationName = r.LocationName
	if r.ContainerNo.Valid {
		out.MarkedContainerNo = r.ContainerNo.Int32
	}
	// Reported for convenience; owned by the row the keg points at. An
	// empty keg reports zeros because it holds nothing, which here is the
	// truth rather than a missing value.
	out.ContentsVolumeL = r.ContentsVolumeL
	out.ContentsAbvPct = r.ContentsAbvPct
	out.ContentsLaa = r.ContentsLaa
	out.ContentsLotCode = r.ContentsLotCode
	if r.DaysSinceFillKnown {
		out.DaysSinceFill = r.DaysSinceFill
		out.DaysSinceFillSet = true
	}
	return out
}

func kegToProto(r sqlcgen.Keg) *stillhousev1.Keg {
	out := &stillhousev1.Keg{
		Id:        r.ID.String(),
		Serial:    r.Serial,
		CapacityL: r.CapacityL.Float64,
		Material:  r.Material,
		Status:    kegStatusToProto(r.Status),
		Kind:      returnableKindToProto(r.Kind),
		Notes:     r.Notes,
	}
	if r.PurchaseCostCad.Valid {
		out.PurchaseCostCad = numericToFloat(r.PurchaseCostCad)
		out.PurchaseCostSet = true
	}
	if r.DepositCad.Valid {
		out.DepositCad = numericToFloat(r.DepositCad)
		out.DepositSet = true
	}
	if r.PurchasedOn.Valid {
		out.PurchasedOn = r.PurchasedOn.Time.Format("2006-01-02")
	}
	if r.CurrentCustomerID.Valid {
		out.CustomerId = r.CurrentCustomerID.UUID.String()
	}
	if r.MarkedContainerID.Valid {
		out.MarkedContainerId = r.MarkedContainerID.UUID.String()
	}
	if r.PackagedInventoryID.Valid {
		out.PackagedInventoryId = r.PackagedInventoryID.UUID.String()
	}
	if r.LastFilledOn.Valid {
		out.LastFilledOn = r.LastFilledOn.Time.Format("2006-01-02")
	}
	if r.LastReturnedOn.Valid {
		out.LastReturnedOn = r.LastReturnedOn.Time.Format("2006-01-02")
	}
	return out
}

var kegStatusNames = map[sqlcgen.KegStatus]stillhousev1.KegStatus{
	sqlcgen.KegStatusAvailable:     stillhousev1.KegStatus_KEG_STATUS_AVAILABLE,
	sqlcgen.KegStatusFilled:        stillhousev1.KegStatus_KEG_STATUS_FILLED,
	sqlcgen.KegStatusAtCustomer:    stillhousev1.KegStatus_KEG_STATUS_AT_CUSTOMER,
	sqlcgen.KegStatusReturnedDirty: stillhousev1.KegStatus_KEG_STATUS_RETURNED_DIRTY,
	sqlcgen.KegStatusOutOfService:  stillhousev1.KegStatus_KEG_STATUS_OUT_OF_SERVICE,
	sqlcgen.KegStatusLost:          stillhousev1.KegStatus_KEG_STATUS_LOST,
}

func kegStatusToProto(s sqlcgen.KegStatus) stillhousev1.KegStatus {
	if v, ok := kegStatusNames[s]; ok {
		return v
	}
	return stillhousev1.KegStatus_KEG_STATUS_UNSPECIFIED
}

var kegEventKinds = map[stillhousev1.KegEventKind]sqlcgen.KegEventKind{
	stillhousev1.KegEventKind_KEG_EVENT_KIND_ACQUIRED:  sqlcgen.KegEventKindAcquired,
	stillhousev1.KegEventKind_KEG_EVENT_KIND_FILLED:    sqlcgen.KegEventKindFilled,
	stillhousev1.KegEventKind_KEG_EVENT_KIND_SHIPPED:   sqlcgen.KegEventKindShipped,
	stillhousev1.KegEventKind_KEG_EVENT_KIND_RETURNED:  sqlcgen.KegEventKindReturned,
	stillhousev1.KegEventKind_KEG_EVENT_KIND_CLEANED:   sqlcgen.KegEventKindCleaned,
	stillhousev1.KegEventKind_KEG_EVENT_KIND_CONDEMNED: sqlcgen.KegEventKindCondemned,
	stillhousev1.KegEventKind_KEG_EVENT_KIND_LOST:      sqlcgen.KegEventKindLost,
}

func kegEventKindFromProto(k stillhousev1.KegEventKind) (sqlcgen.KegEventKind, error) {
	if v, ok := kegEventKinds[k]; ok {
		return v, nil
	}
	return "", errors.New("say what happened to the keg")
}

func kegEventKindToProto(k sqlcgen.KegEventKind) stillhousev1.KegEventKind {
	for p, s := range kegEventKinds {
		if s == k {
			return p
		}
	}
	return stillhousev1.KegEventKind_KEG_EVENT_KIND_UNSPECIFIED
}

// kegContentsFor decides which table a fill points at, from the keg's own
// capacity rather than from what the caller sent.
//
// The threshold is the Act's: a marked special container under EDM3-8-1
// is 100 to 1500 litres, and spirits in anything smaller are packaged
// exactly as a bottle is. Refusing the wrong one rather than accepting it
// keeps the two registers from disagreeing about where a keg's alcohol
// is counted — which is the whole point of not copying the figures onto
// the keg in the first place.
func kegContentsFor(keg sqlcgen.Keg, in *stillhousev1.MoveKegRequest) (marked, packaged uuid.NullUUID, err error) {
	const specialContainerMinL = 100

	// A keg with no capacity cannot reach here: the schema requires one.
	// The guard is on the value rather than on Valid so a non-keg — which
	// cannot hold contents at all — never takes the marked-container
	// branch by accident.
	if keg.CapacityL.Float64 >= specialContainerMinL {
		id, e := uuid.Parse(in.GetMarkedContainerId())
		if e != nil {
			return marked, packaged, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"this keg is %.0f L, so its contents are a marked special container (EDM3-8-1, 100–1500 L) — "+
					"give marked_container_id", keg.CapacityL.Float64))
		}
		return uuid.NullUUID{UUID: id, Valid: true}, uuid.NullUUID{}, nil
	}
	id, e := uuid.Parse(in.GetPackagedInventoryId())
	if e != nil {
		return marked, packaged, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"this keg is %.0f L, which is under the 100 L a marked special container starts at — "+
				"its contents are packaged spirits, so give packaged_inventory_id", keg.CapacityL.Float64))
	}
	return uuid.NullUUID{}, uuid.NullUUID{UUID: id, Valid: true}, nil
}

var returnableKinds = map[stillhousev1.ReturnableKind]sqlcgen.ReturnableKind{
	stillhousev1.ReturnableKind_RETURNABLE_KIND_KEG:          sqlcgen.ReturnableKindKeg,
	stillhousev1.ReturnableKind_RETURNABLE_KIND_PALLET:       sqlcgen.ReturnableKindPallet,
	stillhousev1.ReturnableKind_RETURNABLE_KIND_CRATE:        sqlcgen.ReturnableKindCrate,
	stillhousev1.ReturnableKind_RETURNABLE_KIND_GAS_CYLINDER: sqlcgen.ReturnableKindGasCylinder,
	stillhousev1.ReturnableKind_RETURNABLE_KIND_OTHER:        sqlcgen.ReturnableKindOther,
}

// returnableKindFromProto defaults to keg, and that default is safe in a
// way the other enums in this file are not: the register was a keg
// register, every row in it is a keg, and a caller written before this
// existed means keg. The schema still refuses a non-keg holding spirits,
// so a wrong guess here cannot put alcohol in a crate.
func returnableKindFromProto(k stillhousev1.ReturnableKind) sqlcgen.ReturnableKind {
	if v, ok := returnableKinds[k]; ok {
		return v
	}
	return sqlcgen.ReturnableKindKeg
}

func returnableKindToProto(k sqlcgen.ReturnableKind) stillhousev1.ReturnableKind {
	for p, s := range returnableKinds {
		if s == k {
			return p
		}
	}
	return stillhousev1.ReturnableKind_RETURNABLE_KIND_UNSPECIFIED
}
