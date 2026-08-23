package rpc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/excise"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// statementBasis is printed on the document. A statement a customer keeps
// in a drawer for eight years has to say what it is, because the person
// reading it later will not have this conversation to refer to.
const statementBasis = "A statement of Stillhouse's own records for this cask, as at the date shown. It is not a certificate of age or origin — those are signed by a Canadian official (EDM3-1-1 ¶43–46) and Stillhouse does not issue one. Volumes and strengths are as gauged on the dates listed; nothing between two gauges is interpolated."

// CaskStatement assembles what a cask owner is entitled to know. PLAN J3.
//
// Every figure on it is a recorded gauge or arithmetic over recorded
// gauges. Where a figure cannot be computed from what was actually
// written down — a fill with no strength, a duty rate that cannot be
// cited for today — it is reported as unavailable with the reason, rather
// than filled in with something plausible. A customer statement is the
// last place a made-up number should appear: it leaves the building, it
// is kept, and it is read by someone with no way to check it.
func (s *BarrelService) CaskStatement(
	ctx context.Context,
	req *connect.Request[stillhousev1.CaskStatementRequest],
) (*connect.Response[stillhousev1.CaskStatementResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	id, err := uuid.Parse(req.Msg.GetContainerId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid container_id"))
	}

	now := time.Now().UTC()
	out := &stillhousev1.CaskStatementResponse{
		ContainerId: id.String(),
		Basis:       statementBasis,
		GeneratedAt: now.Format("2006-01-02"),
	}

	err = s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		c, e := q.GetBulkContainer(ctx, id)
		if e != nil {
			return e
		}
		if c.Kind != sqlcgen.BulkContainerKindBarrel {
			return connect.NewError(connect.CodeInvalidArgument,
				errors.New("that container is not a cask"))
		}
		out.CaskName = c.Name
		out.CurrentVolumeL = c.CurrentVolumeL
		out.CurrentAbvPct = c.CurrentAbvPct.Float64
		out.CurrentLaa = c.CurrentLaa
		out.Possession = string(c.Possession)
		if c.OwnerCustomerID.Valid {
			out.OwnerCustomerId = c.OwnerCustomerID.UUID.String()
			if cust, e := q.GetCustomer(ctx, c.OwnerCustomerID.UUID); e == nil {
				out.OwnerName = cust.Name
			}
		}

		if a, e := q.GetBarrelAttributes(ctx, id); e == nil {
			out.CooperageSupplier = a.CooperageSupplier
			out.WoodSpecies = a.WoodSpecies
			out.PriorUse = a.PriorUse
			out.Rickhouse = a.Rickhouse
			if a.CharLevel.Valid {
				out.CharLevel = a.CharLevel.Int32
			}
			if a.FillDate.Valid {
				out.FillDate = a.FillDate.Time.Format("2006-01-02")
				out.DaysInWood = int32(now.Sub(a.FillDate.Time).Hours() / 24)
			}
		} else if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}

		events, e := q.ListBarrelEvents(ctx, id)
		if e != nil {
			return e
		}
		var fill *sqlcgen.BarrelEvent
		for i := range events {
			ev := events[i]
			// A voided event describes something that was reversed. Putting
			// it on a customer's statement would show a gauge that the
			// distillery has already said did not stand.
			if ev.VoidedAt.Valid {
				continue
			}
			g := &stillhousev1.CaskStatementGauge{
				Date:  ev.EventDate.Time.Format("2006-01-02"),
				Kind:  string(ev.Kind),
				Notes: ev.Notes,
			}
			if ev.VolumeL.Valid {
				g.VolumeL = ev.VolumeL.Float64
			}
			if ev.AbvPct.Valid {
				g.AbvPct = ev.AbvPct.Float64
			}
			if ev.Laa.Valid {
				g.Laa = ev.Laa.Float64
			}
			if ev.UserID.Valid {
				if who, e := q.GetUserByID(ctx, ev.UserID.UUID); e == nil {
					g.GaugedBy = who.DisplayName
				}
			}
			out.Gauges = append(out.Gauges, g)
			// ListBarrelEvents is newest first, so the last fill seen is
			// the earliest one — which is the fill that put this spirit in
			// this cask.
			if ev.Kind == sqlcgen.BarrelEventKindFill {
				fill = &events[i]
			}
		}

		fillAngelsShare(out, fill, now)
		return nil
	})
	if err != nil {
		var ce *connect.Error
		if errors.As(err, &ce) {
			return nil, ce
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("cask not found"))
		}
		s.logger.Error("CaskStatement", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	// Duty as it would fall today. Outside the table's coverage this
	// refuses, and the statement says so rather than quoting a stale rate
	// — the figure is the one a cask owner will budget against.
	if out.CurrentLaa > 0 {
		rate, total, e := excise.DutyOnLAA(now, out.CurrentLaa)
		if e != nil {
			out.DutyMissing = e.Error()
		} else {
			out.DutyKnown = true
			out.DutyRatePerLaa = rate
			out.DutyIfBottledTodayCad = round2(total)
		}
	} else {
		out.DutyMissing = "the cask is empty, so no duty would fall due."
	}

	return connect.NewResponse(out), nil
}

// fillAngelsShare computes the loss since fill, or states why it cannot.
//
// The loss is the whole reason a cask owner reads one of these, and it is
// also the figure most easily faked: subtracting today's LAA from a fill
// LAA that nobody recorded gives a number that looks exactly like a real
// one. So the fill gauge has to have carried a strength, and if it did
// not, that is what the statement says.
func fillAngelsShare(out *stillhousev1.CaskStatementResponse, fill *sqlcgen.BarrelEvent, now time.Time) {
	if fill == nil {
		out.AngelsShareMissing = "no fill gauge is recorded for this cask, so there is nothing to measure the loss against."
		return
	}
	if fill.VolumeL.Valid {
		out.FilledVolumeL = fill.VolumeL.Float64
	}
	if fill.AbvPct.Valid {
		out.FilledAbvPct = fill.AbvPct.Float64
	}
	if fill.Laa.Valid {
		out.FilledLaa = fill.Laa.Float64
	}
	if !fill.Laa.Valid || fill.Laa.Float64 <= 0 {
		out.AngelsShareMissing = "the fill gauge did not record a strength, so the alcohol that went in is not known and the loss cannot be computed."
		return
	}

	years := now.Sub(fill.EventDate.Time).Hours() / 24 / 365.25
	lost := fill.Laa.Float64 - out.CurrentLaa
	out.AngelsShareKnown = true
	out.AngelsShareLaa = round4(lost)
	if years > 0 {
		out.AngelsSharePctPerYear = round4(lost / fill.Laa.Float64 * 100 / years)
	} else {
		// Filled today. A rate per year over a period of hours is a
		// division that produces a large and meaningless number.
		out.AngelsShareKnown = false
		out.AngelsShareMissing = fmt.Sprintf(
			"this cask was filled on %s, which is too recent for an annual rate to mean anything.",
			fill.EventDate.Time.Format("2006-01-02"))
	}
}
