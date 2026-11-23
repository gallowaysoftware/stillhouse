package rpc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1/stillhousev1connect"
)

// A phone that lost the answer sends the reading again with the same key.
func TestARepeatedKeyIsRecordedOnce(t *testing.T) {
	b := newFloorBatch(t)
	as := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			return next(WithUser(ctx, b.user), req)
		}
	})
	opts := connect.WithInterceptors(as, NewIdempotencyInterceptor(b.db))
	mux := http.NewServeMux()
	mux.Handle(stillhousev1connect.NewFermentationServiceHandler(b.ferm, opts))
	mux.Handle(stillhousev1connect.NewMaterialServiceHandler(b.mat, opts))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	ferm := stillhousev1connect.NewFermentationServiceClient(srv.Client(), srv.URL)
	mat := stillhousev1connect.NewMaterialServiceClient(srv.Client(), srv.URL)

	log := func(key string, sg float64) (string, error) {
		req := connect.NewRequest(&stillhousev1.AddFermentationLogRequest{
			FermentationRunId: b.fermID, SpecificGravity: sg, SpecificGravitySet: true})
		req.Header().Set(IdempotencyKeyHeader, key)
		res, err := ferm.AddFermentationLog(context.Background(), req)
		if err != nil {
			return "", err
		}
		return res.Msg.GetLog().GetId(), nil
	}
	rows := func() (n int) {
		must(t, b.pool.QueryRow(b.ctx, "SELECT count(*) FROM fermentation_logs WHERE fermentation_run_id = $1", b.fermID).Scan(&n))
		return n
	}

	const key = "phone-0001-aaaa-bbbb"
	first, err := log(key, 1.040)
	must(t, err)
	again, err := log(key, 1.040)
	must(t, err)
	if again != first || rows() != 1 {
		t.Errorf("a repeat answered %s (first %s) and left %d rows", again, first, rows())
	}

	var wg sync.WaitGroup
	ids := make([]string, 6)
	for i := range ids {
		wg.Add(1)
		go func() { defer wg.Done(); ids[i], _ = log("phone-0002-racing-key", 1.030) }()
	}
	wg.Wait()
	if rows() != 2 {
		t.Errorf("six sends of one key at once left %d new rows", rows()-1)
	}

	_, err = log("phone-0003-refused-key", 7)
	refusedWith(t, "a gravity of 7", err, connect.CodeInvalidArgument)
	if _, err := log("phone-0003-refused-key", 1.020); err != nil {
		t.Errorf("a refused entry kept its key: %v", err)
	}

	req := connect.NewRequest(&stillhousev1.ListMaterialsRequest{})
	req.Header().Set(IdempotencyKeyHeader, "phone-0004-not-a-write")
	_, err = mat.ListMaterials(context.Background(), req)
	refusedWith(t, "a key on a call that is not replayable", err, connect.CodeInvalidArgument)

	st := connect.NewRequest(&stillhousev1.UpdateFermentationStatusRequest{Id: b.fermID,
		Status: stillhousev1.FermentationStatus_FERMENTATION_STATUS_ACTIVE})
	st.Header().Set(IdempotencyKeyHeader, key)
	_, err = ferm.UpdateFermentationStatus(context.Background(), st)
	refusedWith(t, "a key reused for another call", err, connect.CodeInvalidArgument)
}
