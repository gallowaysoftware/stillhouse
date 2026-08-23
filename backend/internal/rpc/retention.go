package rpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

// assertNoLegalHold refuses a real deletion while a hold is open.
//
// One function, called from every path that actually removes a row, so a
// delete added later cannot quietly escape a hold. It is deliberately not
// applied to voids: voiding is how Stillhouse reverses almost everything,
// it keeps the row and the reason, and a hold that stopped operators
// correcting mistakes would be lifted within a week.
func assertNoLegalHold(ctx context.Context, q *sqlcgen.Queries, what string) error {
	n, err := q.OpenLegalHoldCount(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
		"a legal hold is open, so %s cannot be deleted. Release the hold first, "+
			"under Settings → Retention, and the release is recorded with its "+
			"reason.", what))
}

type RetentionService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewRetentionService(db *tenantdb.DB, logger *slog.Logger) *RetentionService {
	return &RetentionService{db: db, logger: logger}
}

func (s *RetentionService) fail(op string, err error) error {
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

func (s *RetentionService) RetentionStatus(
	ctx context.Context,
	_ *connect.Request[stillhousev1.RetentionStatusRequest],
) (*connect.Response[stillhousev1.RetentionStatusResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	out := &stillhousev1.RetentionStatusResponse{
		Basis: "Stillhouse keeps what it is given: movements, gauges, runs, " +
			"removals and returns are append-only or void-and-keep, and the " +
			"handful of things that can really be deleted are listed on this " +
			"page. It cannot know how your backups are taken or whether a " +
			"restore has been exercised, so those are your words, not a " +
			"guarantee it makes for you.",
	}
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		policy, e := q.GetRetentionPolicy(ctx, u.TenantID)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		havePolicy := e == nil
		out.Policy = &stillhousev1.RetentionPolicy{}
		if havePolicy {
			out.Policy = retentionPolicyToProto(policy, "")
		}

		coverage, e := q.RetentionCoverage(ctx)
		if e != nil {
			return e
		}
		for _, c := range coverage {
			item := &stillhousev1.RecordClassCoverage{
				RecordClass: c.RecordClass, Rows: c.Rows,
			}
			if c.Oldest.Valid {
				item.Oldest = c.Oldest.Time.Format("2006-01-02")
				item.YearsHeld = time.Since(c.Oldest.Time).Hours() / 24 / 365.25
				if out.Policy.GetRetentionYearsSet() {
					item.ShorterThanPolicy =
						item.YearsHeld < float64(out.Policy.GetRetentionYears())
				}
			}
			out.Coverage = append(out.Coverage, item)
		}

		holds, e := q.ListLegalHolds(ctx)
		if e != nil {
			return e
		}
		for _, h := range holds {
			item := &stillhousev1.LegalHold{
				Id: h.ID.String(), Reason: h.Reason,
				InstructedBy: h.InstructedBy, Reference: h.Reference,
				PlacedOn: formatDate(h.PlacedOn), PlacedByName: h.PlacedByName,
				ReleasedOn: formatDate(h.ReleasedOn), ReleasedByName: h.ReleasedByName,
				ReleaseReason: h.ReleaseReason,
				Open:          !h.ReleasedOn.Valid,
			}
			if item.GetOpen() {
				out.OpenHolds++
			}
			out.Holds = append(out.Holds, item)
		}
		return nil
	})
	if err != nil {
		return nil, s.fail("RetentionStatus", err)
	}
	return connect.NewResponse(out), nil
}

func (s *RetentionService) SaveRetentionPolicy(
	ctx context.Context,
	req *connect.Request[stillhousev1.SaveRetentionPolicyRequest],
) (*connect.Response[stillhousev1.SaveRetentionPolicyResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	in := req.Msg
	if in.GetRetentionYearsSet() && in.GetRetentionYears() <= 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("a retention window of zero years is not a policy — leave it "+
				"unset if you have not decided, and the screens will say so"))
	}
	reviewedOn, err := parseOptionalDate(in.GetReviewedOn(), "reviewed_on")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var out sqlcgen.RetentionPolicy
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		// An empty review date leaves the previous one alone rather than
		// silently re-dating a review that did not happen.
		reviewedBy := uuid.NullUUID{}
		if reviewedOn.Valid {
			reviewedBy = uuid.NullUUID{UUID: u.ID, Valid: true}
		} else if prev, pe := q.GetRetentionPolicy(ctx, u.TenantID); pe == nil {
			reviewedOn, reviewedBy = prev.ReviewedOn, prev.ReviewedBy
		} else if !errors.Is(pe, pgx.ErrNoRows) {
			return pe
		}
		var e error
		out, e = q.SaveRetentionPolicy(ctx, sqlcgen.SaveRetentionPolicyParams{
			TenantID:       u.TenantID,
			RetentionYears: optInt(in.GetRetentionYears(), in.GetRetentionYearsSet()),
			BackupCadence:  in.GetBackupCadence(),
			RestoreNotes:   in.GetRestoreNotes(),
			ReviewedOn:     reviewedOn,
			ReviewedBy:     reviewedBy,
			Notes:          in.GetNotes(),
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "retention_policy",
			u.TenantID.String(), sqlcgen.AuditActionUpdate, map[string]any{
				"retention_years": in.GetRetentionYears(),
				"reviewed_on":     formatDate(reviewedOn),
			})
	})
	if err != nil {
		return nil, s.fail("SaveRetentionPolicy", err)
	}
	return connect.NewResponse(&stillhousev1.SaveRetentionPolicyResponse{
		Policy: retentionPolicyToProto(out, u.DisplayName),
	}), nil
}

func (s *RetentionService) PlaceLegalHold(
	ctx context.Context,
	req *connect.Request[stillhousev1.PlaceLegalHoldRequest],
) (*connect.Response[stillhousev1.PlaceLegalHoldResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	reason := strings.TrimSpace(req.Msg.GetReason())
	if reason == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("say what the hold is about — one with no reason is one "+
				"nobody can lift with confidence"))
	}
	placedOn, err := parseDateOrToday(req.Msg.GetPlacedOn())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var out sqlcgen.LegalHold
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		out, e = q.PlaceLegalHold(ctx, sqlcgen.PlaceLegalHoldParams{
			TenantID: u.TenantID, Reason: reason,
			InstructedBy: req.Msg.GetInstructedBy(),
			Reference:    req.Msg.GetReference(),
			PlacedOn:     placedOn, PlacedBy: u.ID,
		})
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "legal_hold", out.ID.String(),
			sqlcgen.AuditActionCreate, map[string]any{
				"reason": reason, "instructed_by": req.Msg.GetInstructedBy(),
				"reference": req.Msg.GetReference(),
			})
	})
	if err != nil {
		return nil, s.fail("PlaceLegalHold", err)
	}
	return connect.NewResponse(&stillhousev1.PlaceLegalHoldResponse{
		Hold: legalHoldToProto(out, u.DisplayName, ""),
	}), nil
}

func (s *RetentionService) ReleaseLegalHold(
	ctx context.Context,
	req *connect.Request[stillhousev1.ReleaseLegalHoldRequest],
) (*connect.Response[stillhousev1.ReleaseLegalHoldResponse], error) {
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
			errors.New("say why the hold is being lifted — it is the record that "+
				"deletion became permissible again, and when"))
	}
	releasedOn, err := parseDateOrToday(req.Msg.GetReleasedOn())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	var out sqlcgen.LegalHold
	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var e error
		out, e = q.ReleaseLegalHold(ctx, sqlcgen.ReleaseLegalHoldParams{
			ID: id, ReleasedOn: releasedOn,
			ReleasedBy:    uuid.NullUUID{UUID: u.ID, Valid: true},
			ReleaseReason: reason,
		})
		if errors.Is(e, pgx.ErrNoRows) {
			return connect.NewError(connect.CodeFailedPrecondition,
				errors.New("that hold has already been released"))
		}
		if e != nil {
			return e
		}
		return audit.Write(ctx, q, u.TenantID, u.ID, "legal_hold", id.String(),
			sqlcgen.AuditActionUpdate, map[string]any{
				"released": true, "reason": reason,
			})
	})
	if err != nil {
		return nil, s.fail("ReleaseLegalHold", err)
	}
	return connect.NewResponse(&stillhousev1.ReleaseLegalHoldResponse{
		Hold: legalHoldToProto(out, "", u.DisplayName),
	}), nil
}

func retentionPolicyToProto(
	p sqlcgen.RetentionPolicy, reviewerName string,
) *stillhousev1.RetentionPolicy {
	out := &stillhousev1.RetentionPolicy{
		BackupCadence: p.BackupCadence, RestoreNotes: p.RestoreNotes,
		ReviewedOn: formatDate(p.ReviewedOn), ReviewedByName: reviewerName,
		Notes: p.Notes,
	}
	if p.RetentionYears.Valid {
		out.RetentionYears, out.RetentionYearsSet = p.RetentionYears.Int32, true
	}
	if p.ReviewedOn.Valid {
		out.Reviewed = true
		out.DaysSinceReview = int32(time.Since(p.ReviewedOn.Time).Hours() / 24)
	}
	return out
}

func legalHoldToProto(h sqlcgen.LegalHold, placedBy, releasedBy string) *stillhousev1.LegalHold {
	return &stillhousev1.LegalHold{
		Id: h.ID.String(), Reason: h.Reason, InstructedBy: h.InstructedBy,
		Reference: h.Reference, PlacedOn: formatDate(h.PlacedOn),
		PlacedByName: placedBy, ReleasedOn: formatDate(h.ReleasedOn),
		ReleasedByName: releasedBy, ReleaseReason: h.ReleaseReason,
		Open: !h.ReleasedOn.Valid,
	}
}
