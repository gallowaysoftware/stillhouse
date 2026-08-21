package rpc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/excise"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// Losses and their duty treatment.
//
// `bulk_losses_laa` was one number. Under EDM3-4-1 the treatment diverges
// sharply: a destruction approved by CRA is relieved, while spirits that
// cannot be accounted for are duty-payable and cost real money. Collapsing
// the two produces a plausible total and the wrong duty.
//
// Three states, not two. `unclassified` is the honest default — Stillhouse
// does not know whether a given evaporation loss is relieved, and the
// barrel regauge that wrote it did not ask. Guessing either way would put
// a figure on a return that nobody chose, so an unclassified loss is
// reported as unclassified and the period is not ready to file.

// ListLosses returns the losses in a period, optionally only the ones
// nobody has ruled on yet — which is the list an operator works through at
// period end.
func (s *BulkService) ListLosses(
	ctx context.Context,
	req *connect.Request[stillhousev1.ListLossesRequest],
) (*connect.Response[stillhousev1.ListLossesResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	params := sqlcgen.ListLossesParams{UnclassifiedOnly: req.Msg.GetUnclassifiedOnly()}
	var err error
	if params.PeriodStart, err = optionalDate(req.Msg.GetPeriodStart(), "period_start"); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if params.PeriodEnd, err = optionalDate(req.Msg.GetPeriodEnd(), "period_end"); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var rows []sqlcgen.ListLossesRow
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		rows, e = q.ListLosses(ctx, params)
		return e
	})
	if err != nil {
		s.logger.Error("ListLosses", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	out := &stillhousev1.ListLossesResponse{
		Losses: make([]*stillhousev1.ClassifiableLoss, 0, len(rows)),
	}
	for _, r := range rows {
		l := classifiableLossToProto(r)
		out.Losses = append(out.Losses, l)
		switch r.LossDutyTreatment {
		case sqlcgen.LossDutyTreatmentRelieved:
			out.RelievedLaa += r.Laa
		case sqlcgen.LossDutyTreatmentDutiable:
			out.DutiableLaa += r.Laa
		default:
			out.UnclassifiedLaa += r.Laa
		}
	}
	out.RelievedLaa = round4(out.RelievedLaa)
	out.DutiableLaa = round4(out.DutiableLaa)
	out.UnclassifiedLaa = round4(out.UnclassifiedLaa)
	return connect.NewResponse(out), nil
}

// ClassifyLosses rules on several losses at once. An operator resolving a
// period's evaporation losses is making one decision about a dozen rows,
// not a dozen decisions.
func (s *BulkService) ClassifyLosses(
	ctx context.Context,
	req *connect.Request[stillhousev1.ClassifyLossesRequest],
) (*connect.Response[stillhousev1.ClassifyLossesResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	treatment, authority, err := lossTreatmentToDB(req.Msg.GetTreatment(), req.Msg.GetAuthority())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	ids := req.Msg.GetMovementIds()
	if len(ids) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("movement_ids is required"))
	}
	parsed := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		v, perr := uuid.Parse(id)
		if perr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("invalid movement id %q", id))
		}
		parsed = append(parsed, v)
	}

	var updated []sqlcgen.BulkMovement
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		for _, id := range parsed {
			// The period lock applies: reclassifying a loss inside a filed
			// period changes the duty on a return CRA already has.
			existing, e := q.GetBulkMovementForBarrelEvent(ctx, id)
			if e != nil {
				if errors.Is(e, pgx.ErrNoRows) {
					return connect.NewError(connect.CodeNotFound,
						fmt.Errorf("movement %s not found", id))
				}
				return e
			}
			if e := assertMovementDateNotLocked(ctx, q, existing.OccurredAt.Time); e != nil {
				return e
			}
			row, e := q.ClassifyLoss(ctx, sqlcgen.ClassifyLossParams{
				ID:                     id,
				LossDutyTreatment:      treatment,
				LossTreatmentAuthority: authority,
				LossClassifiedBy:       uuid.NullUUID{UUID: u.ID, Valid: true},
			})
			if e != nil {
				if errors.Is(e, pgx.ErrNoRows) {
					// The UPDATE's own reason guard: this movement exists
					// but is not a loss.
					return connect.NewError(connect.CodeFailedPrecondition,
						fmt.Errorf("movement %s is not a loss or a destruction", id))
				}
				return e
			}
			updated = append(updated, row)
			if e := audit.Write(ctx, q, u.TenantID, u.ID, "bulk_movement", id.String(),
				sqlcgen.AuditActionUpdate, map[string]any{
					"event":     "loss_classified",
					"treatment": string(treatment),
					"authority": authority,
					"laa":       row.Laa,
					"reason":    string(row.Reason),
				}); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		if ce := classifyWriteErr(err, "movement not found"); ce != nil {
			return nil, ce
		}
		s.logger.Error("ClassifyLosses", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	out := &stillhousev1.ClassifyLossesResponse{
		Losses: make([]*stillhousev1.ClassifiableLoss, 0, len(updated)),
	}
	for _, m := range updated {
		out.Losses = append(out.Losses, classifiableLossToProto(sqlcgen.ListLossesRow{
			ID: m.ID, Reason: m.Reason, Laa: m.Laa, OccurredAt: m.OccurredAt,
			Notes: m.Notes, DocumentReference: m.DocumentReference,
			LossDutyTreatment: m.LossDutyTreatment, LossTreatmentAuthority: m.LossTreatmentAuthority,
			LossClassifiedBy: m.LossClassifiedBy, LossClassifiedAt: m.LossClassifiedAt,
		}))
	}
	return connect.NewResponse(out), nil
}

// assertMovementDateNotLocked refuses a change whose effective date lands
// inside a submitted period. Same guard the mutation paths use, reached
// from a timestamp rather than a date.
func assertMovementDateNotLocked(ctx context.Context, q *sqlcgen.Queries, at time.Time) error {
	return assertDateNotInLockedPeriod(ctx, q, pgtype.Date{Valid: true, Time: at})
}

func classifiableLossToProto(r sqlcgen.ListLossesRow) *stillhousev1.ClassifiableLoss {
	out := &stillhousev1.ClassifiableLoss{
		MovementId:         r.ID.String(),
		Reason:             bulkMovementReasonToProto(r.Reason),
		ContainerName:      r.ContainerName,
		Laa:                round4(r.Laa),
		OccurredAt:         timestamppb.New(r.OccurredAt.Time),
		Notes:              r.Notes,
		DocumentReference:  r.DocumentReference,
		Treatment:          lossTreatmentToProto(r.LossDutyTreatment),
		TreatmentAuthority: r.LossTreatmentAuthority,
	}
	if r.LossClassifiedBy.Valid {
		out.ClassifiedBy = r.LossClassifiedBy.UUID.String()
	}
	if r.LossClassifiedAt.Valid {
		out.ClassifiedAt = timestamppb.New(r.LossClassifiedAt.Time)
	}
	// What these litres would cost if ruled dutiable, at the rate in force
	// on the day of the loss — so the decision is made with its price
	// visible rather than discovered on the return. A date outside the
	// rate table leaves it at zero rather than refusing the whole listing:
	// this is decoration, and the return itself refuses loudly.
	if _, duty, err := excise.DutyOnLAA(r.OccurredAt.Time, r.Laa); err == nil {
		out.DutyIfDutiableCad = round2cents(duty)
	}
	return out
}

// lossTreatmentToDB validates a treatment and the authority it rests on.
//
// Relief that rests on nothing is not relief, so an authority is required
// for RELIEVED — the CRA approval reference for a destruction, or the
// provision relied on. The database carries the same rule as a CHECK, so
// it holds for paths that do not come through here.
func lossTreatmentToDB(t stillhousev1.LossDutyTreatment, authority string) (
	sqlcgen.LossDutyTreatment, string, error) {
	authority = strings.TrimSpace(authority)
	switch t {
	case stillhousev1.LossDutyTreatment_LOSS_DUTY_TREATMENT_UNCLASSIFIED:
		// Clearing a classification is allowed — somebody ruled wrongly and
		// wants it back on the list — and it drops the authority with it.
		return sqlcgen.LossDutyTreatmentUnclassified, "", nil
	case stillhousev1.LossDutyTreatment_LOSS_DUTY_TREATMENT_RELIEVED:
		if authority == "" {
			return "", "", errors.New(
				"relieved losses need the authority the relief rests on: the CRA approval reference, or the provision relied on")
		}
		return sqlcgen.LossDutyTreatmentRelieved, authority, nil
	case stillhousev1.LossDutyTreatment_LOSS_DUTY_TREATMENT_DUTIABLE:
		return sqlcgen.LossDutyTreatmentDutiable, authority, nil
	}
	return "", "", errors.New("treatment is required")
}

func lossTreatmentToProto(t sqlcgen.LossDutyTreatment) stillhousev1.LossDutyTreatment {
	switch t {
	case sqlcgen.LossDutyTreatmentUnclassified:
		return stillhousev1.LossDutyTreatment_LOSS_DUTY_TREATMENT_UNCLASSIFIED
	case sqlcgen.LossDutyTreatmentRelieved:
		return stillhousev1.LossDutyTreatment_LOSS_DUTY_TREATMENT_RELIEVED
	case sqlcgen.LossDutyTreatmentDutiable:
		return stillhousev1.LossDutyTreatment_LOSS_DUTY_TREATMENT_DUTIABLE
	}
	return stillhousev1.LossDutyTreatment_LOSS_DUTY_TREATMENT_UNSPECIFIED
}
