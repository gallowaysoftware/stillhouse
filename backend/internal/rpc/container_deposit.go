package rpc

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/pricing"
)

// depositCaution is on every response. A deposit report looks like an
// invoice and will be treated as one, so what it is has to travel with
// it rather than sit in a help page.
const depositCaution = "Container counts come from removals and are as reliable as those are. The rates are not ours: each carries where it came from, and a rate marked indicative is a planning figure rather than a remittance. Quoting an aggregator's number to a stewardship programme is the same kind of mistake as quoting an uncited excise rate to CRA — the amounts are smaller and the mistake is not."

// ContainerDepositReport counts containers into each market and applies
// the deposit rate on file. PLAN I4.
func (s *ProvincialService) ContainerDepositReport(
	ctx context.Context,
	req *connect.Request[stillhousev1.ContainerDepositReportRequest],
) (*connect.Response[stillhousev1.ContainerDepositReportResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	start, err := parseDate(req.Msg.GetPeriodStart(), "period_start")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	end, err := parseDate(req.Msg.GetPeriodEnd(), "period_end")
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if end.Before(start) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("period_end is before period_start"))
	}

	out := &stillhousev1.ContainerDepositReportResponse{
		Caution:    depositCaution,
		Remittable: true,
	}
	needs := map[string]bool{}

	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		rows, e := q.ContainersRemovedByJurisdiction(ctx, sqlcgen.ContainersRemovedByJurisdictionParams{
			PeriodStart: pgtype.Date{Valid: true, Time: start},
			PeriodEnd:   pgtype.Date{Valid: true, Time: end},
		})
		if e != nil {
			return e
		}

		// The query groups per lot so returns can be attributed; the
		// report wants one line per jurisdiction and size.
		type key struct {
			jurisdiction string
			size         int32
		}
		agg := map[key]*stillhousev1.ContainerDepositLine{}
		var order []key
		for _, r := range rows {
			k := key{r.Jurisdiction, r.BottleSizeMl}
			line, ok := agg[k]
			if !ok {
				line = &stillhousev1.ContainerDepositLine{
					Jurisdiction: r.Jurisdiction, BottleSizeMl: r.BottleSizeMl,
				}
				agg[k] = line
				order = append(order, k)
			}
			line.ContainersOut += r.Containers
			line.ContainersReturned += r.Returned
		}

		sort.Slice(order, func(i, j int) bool {
			if order[i].jurisdiction != order[j].jurisdiction {
				return order[i].jurisdiction < order[j].jurisdiction
			}
			return order[i].size < order[j].size
		})

		for _, k := range order {
			line := agg[k]
			line.ContainersNet = line.ContainersOut - line.ContainersReturned
			if line.ContainersNet < 0 {
				// More came back than went out in this window, which is
				// normal at a period boundary and is not a negative
				// deposit liability.
				line.ContainersNet = 0
			}
			out.TotalContainersNet += line.ContainersNet

			applyDepositRate(line, needs)
			if line.AmountAvailable {
				out.TotalDepositCad += line.DepositTotalCad
			}
			out.Lines = append(out.Lines, line)
		}
		return nil
	}); err != nil {
		s.logger.Error("ContainerDepositReport", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	out.TotalDepositCad = round2cents(out.TotalDepositCad)
	for j := range needs {
		out.NeedsASourcedRate = append(out.NeedsASourcedRate, j)
	}
	sort.Strings(out.NeedsASourcedRate)
	// One indicative rate makes the whole total a planning figure. A
	// total nobody can decompose is only as good as its worst line, and
	// the person remitting will not decompose it.
	out.Remittable = len(out.NeedsASourcedRate) == 0 && len(out.Lines) > 0
	return connect.NewResponse(out), nil
}

// applyDepositRate puts the rate on a line, with where it came from.
//
// The provenance travels with the money on purpose. internal/pricing
// already grades every rate it carries — unknown refuses, indicative is
// for planning, sourced came from the board's own material — and a
// deposit remittance is closer to quoting a customer than to planning,
// so anything short of sourced is reported and excluded from the
// remittable total rather than quietly summed.
func applyDepositRate(line *stillhousev1.ContainerDepositLine, needs map[string]bool) {
	j := pricing.Find(line.Jurisdiction)
	if j == nil {
		line.AmountMissing = fmt.Sprintf(
			"Stillhouse carries no rates for %q, so the deposit on these containers is not known. The count above still stands.",
			line.Jurisdiction)
		needs[line.Jurisdiction] = true
		return
	}
	r := j.ContainerDepositCAD
	line.RateProvenance = r.Provenance.String()
	line.RateSource = r.Source
	line.RateAsOf = r.AsOf
	line.RateNote = r.Note

	if r.Provenance == pricing.Unknown {
		line.AmountMissing = fmt.Sprintf(
			"no container deposit rate is on file for %s. %s", line.Jurisdiction, r.Note)
		needs[line.Jurisdiction] = true
		return
	}
	line.DepositPerContainerCad = r.Value
	line.DepositTotalCad = round2cents(r.Value * float64(line.ContainersNet))
	line.AmountAvailable = true
	if r.Provenance != pricing.Sourced {
		needs[line.Jurisdiction] = true
	}
}
