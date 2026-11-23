package rpc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/proto"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1/stillhousev1connect"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

// IdempotencyKeyHeader names a write the client may send more than once.
const IdempotencyKeyHeader = "Idempotency-Key"

// unknownAfter is how long a key may sit unanswered before the request it
// names is taken to have died between its commit and its answer.
const unknownAfter = 5 * time.Minute

// replayable are the floor writes a phone queues offline, with how to
// rebuild each answer. A key on anything else is refused rather than
// ignored, so no caller believes it is protected when it is not.
var replayable = map[string]func([]byte) (connect.AnyResponse, error){
	stillhousev1connect.MashServiceCreateMashRunProcedure:                    replay[stillhousev1.CreateMashRunResponse],
	stillhousev1connect.MashServiceUpdateMashStatusProcedure:                 replay[stillhousev1.UpdateMashStatusResponse],
	stillhousev1connect.MashServiceAddMashIngredientProcedure:                replay[stillhousev1.AddMashIngredientResponse],
	stillhousev1connect.MashServiceAddMashMetricProcedure:                    replay[stillhousev1.AddMashMetricResponse],
	stillhousev1connect.FermentationServiceCreateFermentationRunProcedure:    replay[stillhousev1.CreateFermentationRunResponse],
	stillhousev1connect.FermentationServiceUpdateFermentationStatusProcedure: replay[stillhousev1.UpdateFermentationStatusResponse],
	stillhousev1connect.FermentationServiceAddFermentationLogProcedure:       replay[stillhousev1.AddFermentationLogResponse],
	stillhousev1connect.DistillationServiceCreateDistillationRunProcedure:    replay[stillhousev1.CreateDistillationRunResponse],
	stillhousev1connect.DistillationServiceUpdateDistillationStatusProcedure: replay[stillhousev1.UpdateDistillationStatusResponse],
	stillhousev1connect.DistillationServiceAddDistillationChargeProcedure:    replay[stillhousev1.AddDistillationChargeResponse],
	stillhousev1connect.DistillationServiceAddDistillationCutProcedure:       replay[stillhousev1.AddDistillationCutResponse],
	stillhousev1connect.DistillationServiceAddDistillationReadingProcedure:   replay[stillhousev1.AddDistillationReadingResponse],
}

func replay[T any, PT interface {
	*T
	proto.Message
}](b []byte) (connect.AnyResponse, error) {
	msg := PT(new(T))
	if err := proto.Unmarshal(b, msg); err != nil {
		return nil, err
	}
	return connect.NewResponse(msg), nil
}

// NewIdempotencyInterceptor answers a repeated key with the first answer.
// It runs after authentication: keys are per user, so one person's key
// cannot fetch another's answer.
func NewIdempotencyInterceptor(db *tenantdb.DB) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			key := req.Header().Get(IdempotencyKeyHeader)
			if key == "" || req.Spec().IsClient {
				return next(ctx, req)
			}
			procedure := req.Spec().Procedure
			rebuild, ok := replayable[procedure]
			if !ok {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
					"%s cannot be repeated safely; send it without %s", procedure, IdempotencyKeyHeader))
			}
			if len(key) < 16 || len(key) > 100 {
				return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
					"%s must be 16 to 100 characters", IdempotencyKeyHeader))
			}
			u, ok := CurrentUser(ctx)
			if !ok {
				return next(ctx, req)
			}

			var first bool
			var seen sqlcgen.GetIdempotentRequestRow
			if err := db.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
				if err := q.ForgetOldIdempotentRequests(ctx); err != nil {
					return err
				}
				_, err := q.ClaimIdempotencyKey(ctx, sqlcgen.ClaimIdempotencyKeyParams{
					TenantID: u.TenantID, UserID: u.ID, Key: key, Procedure: procedure})
				if err == nil {
					first = true
					return nil
				}
				if !errors.Is(err, pgx.ErrNoRows) {
					return err
				}
				seen, err = q.GetIdempotentRequest(ctx, sqlcgen.GetIdempotentRequestParams{
					TenantID: u.TenantID, UserID: u.ID, Key: key})
				return err
			}); err != nil {
				return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
			}

			if !first {
				switch {
				case seen.Procedure != procedure:
					return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(
						"that key was used for a different request"))
				case seen.Response != nil:
					return rebuild(seen.Response)
				case time.Since(seen.CreatedAt.Time) < unknownAfter:
					return nil, connect.NewError(connect.CodeAborted, errors.New(
						"this entry is still being recorded; try again shortly"))
				default:
					return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New(
						"whether this entry was recorded is not known; check the run before entering it again"))
				}
			}

			params := sqlcgen.ReleaseIdempotencyKeyParams{TenantID: u.TenantID, UserID: u.ID, Key: key}
			res, err := next(ctx, req)
			if err != nil {
				// Nothing was recorded, so the same key may try again.
				_ = db.WithTenantTx(context.WithoutCancel(ctx), u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
					return q.ReleaseIdempotencyKey(ctx, params)
				})
				return nil, err
			}
			msg, ok := res.Any().(proto.Message)
			if !ok {
				return res, nil
			}
			b, err := proto.Marshal(msg)
			if err == nil {
				err = db.WithTenantTx(context.WithoutCancel(ctx), u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
					return q.StoreIdempotentResponse(ctx, sqlcgen.StoreIdempotentResponseParams{
						TenantID: u.TenantID, UserID: u.ID, Key: key, Response: b})
				})
			}
			// The write committed either way; an unstored answer only means a
			// repeat is told the outcome is unknown, never that it runs twice.
			_ = err
			return res, nil
		}
	}
}
