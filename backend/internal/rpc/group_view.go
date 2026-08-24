package rpc

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// groupCaution is on the response rather than in a help page. The whole
// risk of this screen is somebody reading a total onto a return.
const groupCaution = "Each licence below files its own B266. The totals are sums across separate returns and are not a line on any of them — they are for planning where to put a week's work, nothing else. Nothing here is a combined return and there will not be one: two licences are two filers, whoever owns them."

// GroupView reports across every licence the caller holds an account at.
// PLAN H7.
//
// Two constraints shape it, and both are about not producing a plausible
// wrong number.
//
// A B266 is filed per licence. Two distilleries under one owner file two
// returns, and a figure spanning both is not a line on either — so the
// rows stay separate, each carries its own licence number, and the totals
// say what they are.
//
// And what a person may see at each licence is what their account THERE
// may see. Holding an owner's account at one distillery does not make
// somebody an owner at another. Every read below is performed as the
// account the caller actually holds at that entity, which is why the
// entity list comes from their own accounts rather than from anything
// resembling a group membership table.
func (s *TenantService) GroupView(
	ctx context.Context,
	_ *connect.Request[stillhousev1.GroupViewRequest],
) (*connect.Response[stillhousev1.GroupViewResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	// The caller's own accounts, and nothing else. There is deliberately
	// no notion of a group that somebody could be added to: membership IS
	// holding an account, so a group view cannot show a licence the
	// person could not already open by signing in.
	accounts, err := s.q.ListUsersByEmail(ctx, u.Email)
	if err != nil {
		s.logger.Error("GroupView: accounts", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	out := &stillhousev1.GroupViewResponse{Caution: groupCaution}
	for _, acct := range accounts {
		t, err := s.q.GetTenantByID(ctx, acct.TenantID)
		if err != nil {
			s.logger.Error("GroupView: tenant", "err", err, "tenant_id", acct.TenantID)
			continue
		}
		e := &stillhousev1.GroupEntity{
			TenantId:                t.ID.String(),
			TenantName:              t.Name,
			CraSpiritsLicenceNumber: t.CraSpiritsLicenceNumber,
			YourRole:                string(acct.Role),
		}

		// Read as that account, in that tenant's context. WithTenantTx
		// sets the RLS GUC, so this cannot see past the licence it is
		// asking about even if the query tried.
		if err := s.db.WithTenantTx(ctx, acct.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
			bulk, e2 := q.BulkAvailableForBottling(ctx)
			if e2 != nil {
				return e2
			}
			e.BulkLaa = round4(bulk)

			counts, e2 := q.GroupPackagedAndCasks(ctx)
			if e2 != nil {
				return e2
			}
			e.PackagedBottles = counts.Bottles
			e.CaskCount = counts.Casks
			return nil
		}); err != nil {
			// One entity failing must not take the screen down. The
			// commonest cause is an account removed between the listing
			// and the read, and the honest answer is a row saying so.
			s.logger.Error("GroupView: figures", "err", err, "tenant_id", acct.TenantID)
			e.Unavailable = "could not read this licence's figures — your account there may have been removed."
			out.Entities = append(out.Entities, e)
			continue
		}

		fillGroupFilingStatus(ctx, s, acct.TenantID, e)
		if e.GetDaysUntilDue() >= 0 && e.GetDaysUntilDue() <= 7 && !e.GetPeriodSubmitted() {
			out.ReturnsDueSoon++
		}

		out.TotalBulkLaa += e.BulkLaa
		out.TotalPackagedBottles += e.PackagedBottles
		out.Entities = append(out.Entities, e)
	}
	out.TotalBulkLaa = round4(out.TotalBulkLaa)
	return connect.NewResponse(out), nil
}

// fillGroupFilingStatus adds where a licence stands with its own return.
//
// The period comes from the same basis.PeriodContaining walk that
// SuggestB266Period uses, so a group row cannot disagree with the entity's
// own returns page — two screens showing different due dates for one
// licence is worse than one screen showing none.
//
// Best-effort by design: a licence whose calendar cannot be computed
// still belongs on the screen with its stock figures. The one thing this
// view is actually for is spotting a return coming due at the entity
// nobody has looked at this month, and a missing due date must not remove
// the row that would have said so.
func fillGroupFilingStatus(
	ctx context.Context, s *TenantService, tenantID uuid.UUID, e *stillhousev1.GroupEntity,
) {
	t, err := s.q.GetTenantByID(ctx, tenantID)
	if err != nil {
		return
	}
	basis := tenantFilingBasis(t)
	if err := basis.Validate(); err != nil {
		return
	}

	on := time.Now().UTC()
	current := basis.PeriodContaining(on)
	p := current
	if !dayOf(on).After(current.End) {
		p = basis.PeriodContaining(dayOf(current.Start).AddDate(0, 0, -1))
	}
	e.NextPeriodStart = p.Start.Format("2006-01-02")
	e.NextPeriodEnd = p.End.Format("2006-01-02")
	e.NextDueOn = p.DueOn.Format("2006-01-02")
	e.DaysUntilDue = daysUntil(p.DueOn, on)

	_ = s.db.WithTenantTx(ctx, tenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		row, e2 := q.GetB266PeriodByDates(ctx, sqlcgen.GetB266PeriodByDatesParams{
			PeriodStart: pgtype.Date{Valid: true, Time: p.Start},
			PeriodEnd:   pgtype.Date{Valid: true, Time: p.End},
		})
		if e2 != nil {
			return nil // not generated yet, which is the common case
		}
		e.PeriodGenerated = true
		e.PeriodSubmitted = row.Status == sqlcgen.B266StatusSubmitted
		return nil
	})
}
