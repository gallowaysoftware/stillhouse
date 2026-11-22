package rpc

import (
	"testing"
	"time"

	"connectrpc.com/connect"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/zone"
)

func newAtlanticFixture(t *testing.T) (*stillFixture, *time.Location) {
	t.Helper()
	s := newStillFixture(t)
	_, err := s.pool.Exec(s.ctx, "UPDATE tenants SET default_jurisdiction = 'CA-PE' WHERE id = $1", s.tenant.ID)
	must(t, err)
	return s, zone.For("CA-PE")
}

// A month ends at the licensee's midnight.
func TestAnEveningGaugeIsOnItsOwnMonthsReturn(t *testing.T) {
	s, pei := newAtlanticFixture(t)
	must(t, s.gauge(s.run(t), 100, 60, time.Date(2026, 10, 31, 22, 30, 0, 0, pei)))
	must(t, s.gauge(s.run(t), 10, 60, time.Date(2026, 11, 1, 0, 30, 0, 0, pei)))
	for _, p := range []struct {
		start, end           string
		opening, made, close float64
	}{
		{"2026-10-01", "2026-10-31", 0, 60, 60},
		{"2026-11-01", "2026-11-30", 60, 6, 66},
	} {
		r := s.period(t, p.start, p.end)
		if !near(r.GetBulkOpeningLaa(), p.opening, 1e-4) || !near(r.GetBulkProductionLaa(), p.made, 1e-4) || !near(r.GetBulkClosingLaa(), p.close, 1e-4) {
			t.Errorf("%s: opening %v, production %v, closing %v; want %v, %v, %v", p.start,
				r.GetBulkOpeningLaa(), r.GetBulkProductionLaa(), r.GetBulkClosingLaa(), p.opening, p.made, p.close)
		}
	}
}

func TestTheLockClosesAtLocalMidnight(t *testing.T) {
	s, pei := newAtlanticFixture(t)
	s.file(t, "2026-10-01", "2026-10-31")
	refusedWith(t, "22:30 on the last day of a filed month",
		s.gauge(s.run(t), 10, 60, time.Date(2026, 10, 31, 22, 30, 0, 0, pei)), connect.CodeFailedPrecondition)
	must(t, s.gauge(s.run(t), 10, 60, time.Date(2026, 11, 1, 0, 30, 0, 0, pei)))
	must(t, s.gauge(s.run(t), 10, 60, time.Date(2026, 9, 30, 22, 30, 0, 0, pei)))
}

// A date with no time is that date on the return and on the screen.
func TestADateOnlyMovementIsOnItsDay(t *testing.T) {
	s, pei := newAtlanticFixture(t)
	must(t, s.gauge(s.run(t), 100, 60, time.Date(2026, 9, 5, 12, 0, 0, 0, pei)))
	_, err := NewRedistillationService(s.db, testLogger()).StartRedistillation(s.ctx, connect.NewRequest(
		&stillhousev1.StartRedistillationRequest{SourceContainerId: s.tank.ID.String(), VolumeL: 10, AbvPct: 60,
			Reason: stillhousev1.RedistillationReason_REDISTILLATION_REASON_OFF_SPEC, TakenOn: "2026-11-01"}))
	must(t, err)
	if got := s.period(t, "2026-10-01", "2026-10-31").GetBulkReturnedToProductionLaa(); got != 0 {
		t.Errorf("1 November is on October's return: %v", got)
	}
	if got := s.period(t, "2026-11-01", "2026-11-30").GetBulkReturnedToProductionLaa(); !near(got, 6, 1e-4) {
		t.Errorf("1 November is not on November's return: %v", got)
	}
	var at time.Time
	must(t, s.pool.QueryRow(s.ctx, "SELECT occurred_at FROM bulk_movements WHERE reference_type = 'redistillation' AND tenant_id = $1", s.tenant.ID).Scan(&at))
	if got := at.In(pei).Format("2006-01-02"); got != "2026-11-01" {
		t.Errorf("shown as %s", got)
	}
}

func TestABlankDateIsTheDistillerysToday(t *testing.T) {
	s, pei := newAtlanticFixture(t)
	run, err := s.dist.CreateDistillationRun(s.ctx, connect.NewRequest(&stillhousev1.CreateDistillationRunRequest{}))
	must(t, err)
	if got, want := run.Msg.GetRun().GetRunDate(), time.Now().In(pei).Format("2006-01-02"); got != want {
		t.Errorf("run date %s, want %s", got, want)
	}
}
