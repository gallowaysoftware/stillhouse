package rpc

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

// Readings at the still: what was taken is kept as taken, an empty reading
// is refused, and a deleted one is gone from the run.
//
// Needs a database: STILLHOUSE_INTEGRATION_TEST_ADMIN_DSN.
func TestDistillationReadings(t *testing.T) {
	pool, q := integrationPool(t)
	ctx := context.Background()
	tenant := testTenant(t, ctx, q, pool, "ReadingsTest")
	user, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
		TenantID: tenant.ID, Email: "readings@example.com", DisplayName: "Readings",
		Role: sqlcgen.UserRoleOperator, PasswordHash: "x",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	dist, err := q.CreateDistillationRun(ctx, sqlcgen.CreateDistillationRunParams{
		TenantID: tenant.ID, RunNo: 1, StillLabel: "200 L",
		RunDate: pgtype.Date{Valid: true, Time: time.Now()},
		Status:  sqlcgen.DistillationStatusDistilling,
	})
	if err != nil {
		t.Fatalf("create distillation: %v", err)
	}

	svc := NewDistillationService(tenantdb.New(pool), testLogger())
	uctx := WithUser(ctx, user)

	t.Run("an empty reading is refused", func(t *testing.T) {
		_, err := svc.AddDistillationReading(uctx, connect.NewRequest(&stillhousev1.AddDistillationReadingRequest{
			DistillationRunId: dist.ID.String(),
		}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("want invalid_argument, got %v", err)
		}
	})

	t.Run("a strength past 100 is refused", func(t *testing.T) {
		_, err := svc.AddDistillationReading(uctx, connect.NewRequest(&stillhousev1.AddDistillationReadingRequest{
			DistillationRunId: dist.ID.String(), DistillateAbvPct: 101, DistillateAbvPctSet: true,
		}))
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("want invalid_argument, got %v", err)
		}
	})

	first, err := svc.AddDistillationReading(uctx, connect.NewRequest(&stillhousev1.AddDistillationReadingRequest{
		DistillationRunId: dist.ID.String(),
		DistillateAbvPct:  75.5, DistillateAbvPctSet: true,
		DistillateTemperatureC: 24, DistillateTemperatureCSet: true,
		HeatSettingPct: 100, HeatSettingPctSet: true,
		Notes: "first drop, solvent on the glass",
	}))
	if err != nil {
		t.Fatalf("add reading: %v", err)
	}
	second, err := svc.AddDistillationReading(uctx, connect.NewRequest(&stillhousev1.AddDistillationReadingRequest{
		DistillationRunId: dist.ID.String(),
		DistillateAbvPct:  0, DistillateAbvPctSet: true, // a literal zero is a measurement
		HeatSettingPct: 60, HeatSettingPctSet: true,
	}))
	if err != nil {
		t.Fatalf("add second reading: %v", err)
	}

	got, err := svc.GetDistillationRun(uctx, connect.NewRequest(&stillhousev1.GetDistillationRunRequest{Id: dist.ID.String()}))
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	rs := got.Msg.GetRun().GetReadings()
	if len(rs) != 2 {
		t.Fatalf("want 2 readings on the run, got %d", len(rs))
	}
	if rs[0].GetId() != first.Msg.GetReading().GetId() || rs[0].GetDistillateAbvPct() != 75.5 || !rs[0].GetDistillateTemperatureCSet() || rs[0].GetPotTemperatureCSet() {
		t.Errorf("first reading came back wrong: %+v", rs[0])
	}
	if !rs[1].GetDistillateAbvPctSet() || rs[1].GetDistillateAbvPct() != 0 {
		t.Errorf("a measured zero must come back as measured: %+v", rs[1])
	}

	if _, err := svc.DeleteDistillationReading(uctx, connect.NewRequest(&stillhousev1.DeleteDistillationReadingRequest{Id: second.Msg.GetReading().GetId()})); err != nil {
		t.Fatalf("delete reading: %v", err)
	}
	got, err = svc.GetDistillationRun(uctx, connect.NewRequest(&stillhousev1.GetDistillationRunRequest{Id: dist.ID.String()}))
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if n := len(got.Msg.GetRun().GetReadings()); n != 1 {
		t.Errorf("want 1 reading after delete, got %d", n)
	}
	if _, err := svc.DeleteDistillationReading(uctx, connect.NewRequest(&stillhousev1.DeleteDistillationReadingRequest{Id: second.Msg.GetReading().GetId()})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("deleting twice must say not found, got %v", err)
	}
}
