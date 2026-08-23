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

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

// RedistillationService keeps the account of spirit that went back into
// the still.
//
// A redistillation is the one operation where alcohol legitimately
// disappears in bulk and nobody is obliged to notice: it leaves stock as
// a reportable withdrawal, and weeks later a run produces less than went
// in. With nothing joining the two, the difference is not a loss anybody
// has classified — it is just a smaller number.
type RedistillationService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewRedistillationService(db *tenantdb.DB, logger *slog.Logger) *RedistillationService {
	return &RedistillationService{db: db, logger: logger}
}

// StartRedistillation takes spirit out of a container and back into
// production.
//
// The reportable bulk movement and the redistillation record are written
// in one transaction. Separately, they would be two accounts of the same
// event that could disagree — and the one that reaches the B266 is the
// movement, so a record that failed to write would leave a withdrawal on
// the return with nothing owning its output.
func (s *RedistillationService) StartRedistillation(
	ctx context.Context,
	req *connect.Request[stillhousev1.StartRedistillationRequest],
) (*connect.Response[stillhousev1.StartRedistillationResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	containerID, err := uuid.Parse(in.GetSourceContainerId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid source_container_id"))
	}
	reason, err := redistillationReasonToDB(in.GetReason())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if in.GetVolumeL() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("volume_l must be greater than zero"))
	}
	if in.GetAbvPct() <= 0 || in.GetAbvPct() > 100 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("abv_pct must be in (0, 100]"))
	}
	takenOn, err := parseDateOrToday(in.GetTakenOn())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("taken_on must be YYYY-MM-DD"))
	}
	laa := in.GetVolumeL() * in.GetAbvPct() / 100

	var (
		row       sqlcgen.Redistillation
		container sqlcgen.BulkContainer
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		if e := assertDateNotInLockedPeriod(ctx, q, takenOn); e != nil {
			return e
		}
		// Locked before the balance is read: two withdrawals against one
		// container otherwise both see enough and both subtract, the lost
		// update fixed for bulk containers in stage 131.
		vessel, e := q.GetBulkContainerForUpdate(ctx, containerID)
		if e != nil {
			return e
		}
		if vessel.CurrentLaa+1e-9 < laa {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
				"%s holds %.2f LAA; this would take %.2f", vessel.Name, vessel.CurrentLaa, laa))
		}

		movement, e := q.InsertExternalBulkMovement(ctx, sqlcgen.InsertExternalBulkMovementParams{
			TenantID:          u.TenantID,
			SourceContainerID: uuid.NullUUID{UUID: containerID, Valid: true},
			VolumeL:           in.GetVolumeL(),
			AbvPct:            in.GetAbvPct(),
			Laa:               laa,
			// The page 3 vocabulary stage 146 added. This is the
			// reportable half of the event.
			Reason:          sqlcgen.BulkMovementReasonReturnedToProduction,
			ReferenceType:   "redistillation",
			Notes:           in.GetNotes(),
			OccurredAt:      pgtype.Timestamptz{Valid: true, Time: takenOn.Time},
			ObservedVolumeL: pgtype.Float8{Float64: in.GetVolumeL(), Valid: true},
			VolumeFactorC:   1,
			StrengthSource:  sqlcgen.StrengthSourceUncorrected,
			RecordedBy:      uuid.NullUUID{UUID: u.ID, Valid: true},
			// The withdrawal itself is not a loss — the alcohol went
			// into the still, it did not evaporate. Whatever fails to
			// come back is the loss, and it is ruled on against the
			// redistillation record once the output is known.
			LossDutyTreatment: sqlcgen.LossDutyTreatmentUnclassified,
		})
		if e != nil {
			return e
		}

		newVol := vessel.CurrentVolumeL - in.GetVolumeL()
		newLAA := vessel.CurrentLaa - laa
		newABV := pgtype.Float8{}
		if newVol > 1e-9 {
			newABV = pgtype.Float8{Float64: newLAA / newVol * 100, Valid: true}
		} else {
			newVol, newLAA = 0, 0
		}
		container, e = q.UpdateBulkContainerBalance(ctx, sqlcgen.UpdateBulkContainerBalanceParams{
			ID: containerID, CurrentVolumeL: newVol, CurrentAbvPct: newABV, CurrentLaa: newLAA,
		})
		if e != nil {
			return e
		}

		row, e = q.CreateRedistillation(ctx, sqlcgen.CreateRedistillationParams{
			TenantID:          u.TenantID,
			SourceContainerID: containerID,
			BulkMovementID:    uuid.NullUUID{UUID: movement.ID, Valid: true},
			Reason:            reason,
			TakenOn:           takenOn,
			VolumeTakenL:      in.GetVolumeL(),
			AbvTakenPct:       in.GetAbvPct(),
			LaaTaken:          laa,
			Notes:             in.GetNotes(),
			RecordedBy:        u.ID,
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "redistillation", row.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"container": vessel.Name, "reason": string(reason),
				"laa_taken": laa, "bulk_movement_id": movement.ID.String(),
			})
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("container not found"))
		}
		s.logger.Error("StartRedistillation", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.StartRedistillationResponse{
		Redistillation: redistillationToProto(row, container.Name, 0, u.DisplayName),
	}), nil
}

// RecordRedistillationOutput closes the loop once the run is gauged.
//
// The response says the loss out loud rather than leaving it to be
// noticed on a report. Alcohol that went into the still and did not come
// out is a loss like any other and has to be ruled relieved or
// duty-payable (stage 147) — until this record exists there was nothing
// to rule on, which is what A8 was actually about.
func (s *RedistillationService) RecordRedistillationOutput(
	ctx context.Context,
	req *connect.Request[stillhousev1.RecordRedistillationOutputRequest],
) (*connect.Response[stillhousev1.RecordRedistillationOutputResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	id, err := uuid.Parse(in.GetId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid id"))
	}
	if in.GetLaaProduced() < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("laa_produced cannot be negative"))
	}
	var runID uuid.NullUUID
	if v := in.GetDistillationRunId(); v != "" {
		rid, perr := uuid.Parse(v)
		if perr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid distillation_run_id"))
		}
		runID = uuid.NullUUID{UUID: rid, Valid: true}
	}
	producedOn, err := parseDateOrToday(in.GetProducedOn())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("produced_on must be YYYY-MM-DD"))
	}

	var (
		row  sqlcgen.Redistillation
		name string
	)
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		existing, e := q.GetRedistillation(ctx, id)
		if e != nil {
			return e
		}
		// More out than in is not a yield, it is a mistake — usually a
		// figure typed in litres where LAA was wanted. Refusing it here
		// stops a negative loss reaching the return.
		if in.GetLaaProduced() > existing.LaaTaken+1e-9 {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
				"%.2f LAA out of a %.2f LAA charge — a still does not create alcohol; "+
					"check whether that figure is litres rather than LAA",
				in.GetLaaProduced(), existing.LaaTaken))
		}
		row, e = q.RecordRedistillationOutput(ctx, sqlcgen.RecordRedistillationOutputParams{
			ID: id, DistillationRunID: runID,
			LaaProduced: pgtype.Float8{Float64: in.GetLaaProduced(), Valid: true},
			ProducedOn:  producedOn,
		})
		if errors.Is(e, pgx.ErrNoRows) {
			// The UPDATE is scoped to records with no output yet.
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("this redistillation already has its output recorded"))
		}
		if e != nil {
			return e
		}
		vessel, e := q.GetBulkContainer(ctx, row.SourceContainerID)
		if e == nil {
			name = vessel.Name
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "redistillation", id.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"event": "output_recorded", "laa_taken": row.LaaTaken,
				"laa_produced": in.GetLaaProduced(), "loss_laa": row.LossLaa.Float64,
			})
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("redistillation not found"))
		}
		s.logger.Error("RecordRedistillationOutput", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.RecordRedistillationOutputResponse{
		Redistillation: redistillationToProto(row, name, 0, ""),
		LossLaa:        row.LossLaa.Float64,
		// A zero loss is a real and unremarkable outcome for a short
		// reprocessing run; anything above it wants ruling on.
		NeedsLossClassification: row.LossLaa.Valid && row.LossLaa.Float64 > 1e-9 &&
			!row.LossClassifiedAt.Valid,
	}), nil
}

func (s *RedistillationService) ListRedistillations(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListRedistillationsRequest],
) (*connect.Response[stillhousev1.ListRedistillationsResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	limit := int32(100)
	if v := req.Msg.GetLimit(); v > 0 && v <= 500 {
		limit = v
	}
	var rows []sqlcgen.ListRedistillationsRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListRedistillations(ctx, sqlcgen.ListRedistillationsParams{
			OpenOnly: req.Msg.GetOpenOnly(), RowLimit: limit,
		})
		return e
	}); err != nil {
		s.logger.Error("ListRedistillations", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.Redistillation, 0, len(rows))
	for _, r := range rows {
		out = append(out, redistillationToProto(sqlcgen.Redistillation{
			ID: r.ID, SourceContainerID: r.SourceContainerID, Reason: r.Reason,
			TakenOn: r.TakenOn, VolumeTakenL: r.VolumeTakenL, AbvTakenPct: r.AbvTakenPct,
			LaaTaken: r.LaaTaken, DistillationRunID: r.DistillationRunID,
			LaaProduced: r.LaaProduced, ProducedOn: r.ProducedOn, LossLaa: r.LossLaa,
			LossClassifiedAt: r.LossClassifiedAt, Notes: r.Notes,
		}, r.SourceContainerName, r.DistillationRunNo, r.RecordedByName))
	}
	return connect.NewResponse(&stillhousev1.ListRedistillationsResponse{Redistillations: out}), nil
}

func (s *RedistillationService) RedistillationSummary(
	ctx context.Context,
	req *connect.Request[stillhousev1.RedistillationSummaryRequest],
) (*connect.Response[stillhousev1.RedistillationSummaryResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	start, end, err := parseJournalPeriod(req.Msg.GetPeriodStart(), req.Msg.GetPeriodEnd())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	var row sqlcgen.RedistillationPeriodSummaryRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		row, e = q.RedistillationPeriodSummary(ctx, sqlcgen.RedistillationPeriodSummaryParams{
			PeriodStart: pgtype.Date{Valid: true, Time: start},
			PeriodEnd:   pgtype.Date{Valid: true, Time: end},
		})
		return e
	}); err != nil {
		s.logger.Error("RedistillationSummary", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.RedistillationSummaryResponse{
		EventCount:  row.EventCount,
		LaaTaken:    row.LaaTaken,
		LaaProduced: row.LaaProduced,
		LossLaa:     row.LossLaa,
		StillOpen:   row.StillOpen,
	}), nil
}

func redistillationToProto(
	r sqlcgen.Redistillation, containerName string, runNo int32, recordedBy string,
) *stillhousev1.Redistillation {
	out := &stillhousev1.Redistillation{
		Id:                  r.ID.String(),
		SourceContainerId:   r.SourceContainerID.String(),
		SourceContainerName: containerName,
		Reason:              redistillationReasonToProto(r.Reason),
		TakenOn:             formatDate(r.TakenOn),
		VolumeTakenL:        r.VolumeTakenL,
		AbvTakenPct:         r.AbvTakenPct,
		LaaTaken:            r.LaaTaken,
		DistillationRunId:   nullUUIDString(r.DistillationRunID),
		DistillationRunNo:   runNo,
		ProducedOn:          formatDate(r.ProducedOn),
		LossClassified:      r.LossClassifiedAt.Valid,
		Notes:               r.Notes,
		RecordedByName:      recordedBy,
	}
	if r.LaaProduced.Valid {
		out.LaaProduced, out.LaaProducedSet = r.LaaProduced.Float64, true
	}
	if r.LossLaa.Valid {
		out.LossLaa, out.LossLaaSet = r.LossLaa.Float64, true
	}
	return out
}

func redistillationReasonToDB(r stillhousev1.RedistillationReason) (sqlcgen.RedistillationReason, error) {
	switch r {
	case stillhousev1.RedistillationReason_REDISTILLATION_REASON_OFF_SPEC:
		return sqlcgen.RedistillationReasonOffSpec, nil
	case stillhousev1.RedistillationReason_REDISTILLATION_REASON_FEINTS_RECOVERY:
		return sqlcgen.RedistillationReasonFeintsRecovery, nil
	case stillhousev1.RedistillationReason_REDISTILLATION_REASON_REPROCESSING:
		return sqlcgen.RedistillationReasonReprocessing, nil
	case stillhousev1.RedistillationReason_REDISTILLATION_REASON_OTHER:
		return sqlcgen.RedistillationReasonOther, nil
	}
	return "", errors.New("say why it is going back through the still")
}

func redistillationReasonToProto(r sqlcgen.RedistillationReason) stillhousev1.RedistillationReason {
	switch r {
	case sqlcgen.RedistillationReasonOffSpec:
		return stillhousev1.RedistillationReason_REDISTILLATION_REASON_OFF_SPEC
	case sqlcgen.RedistillationReasonFeintsRecovery:
		return stillhousev1.RedistillationReason_REDISTILLATION_REASON_FEINTS_RECOVERY
	case sqlcgen.RedistillationReasonReprocessing:
		return stillhousev1.RedistillationReason_REDISTILLATION_REASON_REPROCESSING
	}
	return stillhousev1.RedistillationReason_REDISTILLATION_REASON_OTHER
}
