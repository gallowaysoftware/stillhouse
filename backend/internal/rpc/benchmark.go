package rpc

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"

	"github.com/gallowaysoftware/stillhouse/backend/internal/audit"
	"github.com/gallowaysoftware/stillhouse/backend/internal/benchmark"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

type BenchmarkService struct {
	db     *tenantdb.DB
	logger *slog.Logger
}

func NewBenchmarkService(db *tenantdb.DB, logger *slog.Logger) *BenchmarkService {
	return &BenchmarkService{db: db, logger: logger}
}

const benchmarkPrivacyNote = "Figures are quartiles over every licensee who has opted in, never minimums or maximums — a maximum is one participant's exact number. Nothing is reported until at least " +
	"five distinct distilleries have contributed, counted as distilleries rather than as casks or runs, and nothing is reported where one participant supplies most of the sample. Your own figures leave this " +
	"installation in the same form: as one anonymous measurement among others."

const benchmarkNotOptedIn = "Benchmarks are for licensees who contribute to them. Nothing about your distillery is shared until you opt in, and nothing is shown until you do — a reader who contributes nothing is a data tap rather than a network. You can opt out again at any time, and your figures stop being counted immediately."

// Benchmarks compares the caller against the cohort. PLAN J2.
//
// The reciprocity check is first and is not negotiable: a caller who has
// not opted in sees nothing at all, because the alternative is a feature
// that lets one distillery read the industry without ever appearing in
// it.
func (s *BenchmarkService) Benchmarks(
	ctx context.Context,
	_ *connect.Request[stillhousev1.BenchmarksRequest],
) (*connect.Response[stillhousev1.BenchmarksResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	out := &stillhousev1.BenchmarksResponse{PrivacyNote: benchmarkPrivacyNote}
	err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		optIn, e := q.GetBenchmarkOptIn(ctx, u.TenantID)
		if e != nil {
			return e
		}
		out.OptedIn = optIn.BenchmarkOptIn
		if optIn.BenchmarkOptInAt.Valid {
			out.OptedInAt = optIn.BenchmarkOptInAt.Time.Format("2006-01-02")
		}
		if !optIn.BenchmarkOptIn {
			out.Refused = benchmarkNotOptedIn
			return nil
		}

		angels, e := q.BenchmarkAngelsShare(ctx)
		if e != nil {
			return e
		}
		obs := make([]benchmark.Observation, 0, len(angels))
		for _, a := range angels {
			obs = append(obs, benchmark.Observation{TenantID: a.TenantID.String(), Value: a.PctPerYear})
		}
		mine, e := q.MyAngelsShare(ctx)
		if e != nil {
			return e
		}
		out.Metrics = append(out.Metrics, &stillhousev1.BenchmarkMetric{
			Key:  "angels_share",
			Name: "Angel's share",
			Unit: "% of original alcohol per year",
			Basis: "Per cask: the alcohol lost since fill, as a share of what went in, annualised over the days in wood. " +
				"Only casks with a recorded fill strength and at least 90 days in wood — a cask filled last week has an annual rate that is arithmetic noise.",
			Cohort:           cohortToProto(benchmark.Summarise(obs)),
			You:              mine.PctPerYear,
			YouSet:           mine.Casks > 0,
			YourObservations: mine.Casks,
		})

		cuts, e := q.BenchmarkCutRatio(ctx)
		if e != nil {
			return e
		}
		cobs := make([]benchmark.Observation, 0, len(cuts))
		for _, c := range cuts {
			cobs = append(cobs, benchmark.Observation{TenantID: c.TenantID.String(), Value: c.HeartsPct})
		}
		out.Metrics = append(out.Metrics, &stillhousev1.BenchmarkMetric{
			Key:  "cut_ratio",
			Name: "Hearts cut",
			Unit: "% of a run's cut alcohol",
			Basis: "Per distillation run: the alcohol taken as hearts, as a share of everything cut. " +
				"Voided runs excluded.",
			Cohort: cohortToProto(benchmark.Summarise(cobs)),
		})
		return nil
	})
	if err != nil {
		s.logger.Error("Benchmarks", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(out), nil
}

// SetBenchmarkOptIn records the decision, both ways.
//
// Opting out clears the stamp as well as the flag, so the record never
// says somebody consented when they have since withdrawn — and it takes
// effect immediately, because consent that persists after withdrawal is
// not consent.
func (s *BenchmarkService) SetBenchmarkOptIn(
	ctx context.Context,
	req *connect.Request[stillhousev1.SetBenchmarkOptInRequest],
) (*connect.Response[stillhousev1.SetBenchmarkOptInResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var row sqlcgen.SetBenchmarkOptInRow
	if err := s.db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		r, e := q.SetBenchmarkOptIn(ctx, sqlcgen.SetBenchmarkOptInParams{
			ID: u.TenantID, OptIn: req.Msg.GetOptIn(),
			UserID: u.ID,
		})
		if e != nil {
			return e
		}
		row = r
		return audit.Write(ctx, q, u.TenantID, u.ID, "tenant", u.TenantID.String(),
			sqlcgen.AuditActionUpdate, map[string]any{"benchmark_opt_in": req.Msg.GetOptIn()})
	}); err != nil {
		s.logger.Error("SetBenchmarkOptIn", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	resp := &stillhousev1.SetBenchmarkOptInResponse{OptedIn: row.BenchmarkOptIn}
	if row.BenchmarkOptInAt.Valid {
		resp.OptedInAt = row.BenchmarkOptInAt.Time.Format("2006-01-02")
	}
	return connect.NewResponse(resp), nil
}

func cohortToProto(c benchmark.Cohort) *stillhousev1.BenchmarkCohort {
	return &stillhousev1.BenchmarkCohort{
		Available:    c.Available,
		Missing:      c.Missing,
		P25:          round4(c.P25),
		Median:       round4(c.Median),
		P75:          round4(c.P75),
		Tenants:      int32(c.Tenants),
		Observations: int32(c.Observations),
	}
}
