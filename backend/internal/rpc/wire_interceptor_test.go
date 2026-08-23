package rpc

import (
	"context"
	"strconv"
	"testing"

	"connectrpc.com/connect"

	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

// The interceptor has to reach the figure wherever it sits in the
// response, and has to leave an error response alone.
func TestFloatRoundingInterceptor(t *testing.T) {
	// One 700 mL bottle at 46.5 %, computed the way recordRemoval computes
	// it. See internal/wire for why this is the F17 case.
	laa := float64(1) * 700 / 1000 * 46.5 / 100

	interceptor := NewFloatRoundingInterceptor()
	next := connect.UnaryFunc(func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&stillhousev1.ListBulkContainersResponse{
			Summary: &stillhousev1.BulkSummary{TotalLaa: laa},
			Containers: []*stillhousev1.BulkContainer{
				{Name: "Tank 1", CurrentLaa: laa},
			},
		}), nil
	})

	res, err := interceptor(next)(context.Background(),
		connect.NewRequest(&stillhousev1.ListBulkContainersRequest{}))
	if err != nil {
		t.Fatalf("interceptor: %v", err)
	}
	msg, ok := res.Any().(*stillhousev1.ListBulkContainersResponse)
	if !ok {
		t.Fatalf("response type = %T", res.Any())
	}
	if got := strconv.FormatFloat(msg.GetSummary().GetTotalLaa(), 'g', -1, 64); got != "0.3255" {
		t.Errorf("summary LAA on the wire = %s, want 0.3255", got)
	}
	if got := strconv.FormatFloat(msg.GetContainers()[0].GetCurrentLaa(), 'g', -1, 64); got != "0.3255" {
		t.Errorf("container LAA on the wire = %s, want 0.3255", got)
	}
}

func TestFloatRoundingInterceptorPassesErrorsThrough(t *testing.T) {
	want := connect.NewError(connect.CodeFailedPrecondition, errTestSentinel)
	next := connect.UnaryFunc(func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
		return nil, want
	})
	_, err := NewFloatRoundingInterceptor()(next)(context.Background(),
		connect.NewRequest(&stillhousev1.ListBulkContainersRequest{}))
	if err != want {
		t.Errorf("err = %v, want the original error unchanged", err)
	}
}

var errTestSentinel = errSentinel{}

type errSentinel struct{}

func (errSentinel) Error() string { return "sentinel" }
