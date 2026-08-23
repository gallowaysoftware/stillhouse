package rpc

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

type ctxKey int

const (
	ctxUser ctxKey = iota
)

// publicProcedures bypass the authentication check. CreateTenant is included
// because it is the bootstrap RPC for a fresh install; the handler itself
// rejects calls once any tenant exists.
var publicProcedures = map[string]bool{
	"/stillhouse.v1.AuthService/Login":              true,
	"/stillhouse.v1.AuthService/Logout":             true,
	"/stillhouse.v1.TenantService/CreateTenant":     true,
	"/stillhouse.v1.InviteService/SignupWithInvite": true,
	// Password recovery is by definition reachable without a session —
	// these were absent, so "forgot password" answered "authentication
	// required". Both are rate-limited (see auth.go) and neither
	// distinguishes a known email from an unknown one.
	"/stillhouse.v1.AuthService/RequestPasswordReset": true,
	"/stillhouse.v1.AuthService/ResetPassword":        true,
}

// NewAuthInterceptor returns a Connect unary interceptor that:
//  1. Lets public procedures through without a session.
//  2. If an Authorization: Bearer header is present, treats it as a
//     personal access token (api_tokens table).
//  3. Otherwise reads the session, loads the user, and attaches them to
//     the request context (accessible via CurrentUser).
func NewAuthInterceptor(sm *scs.SessionManager, q *sqlcgen.Queries) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if publicProcedures[req.Spec().Procedure] {
				return next(ctx, req)
			}
			if tok, err := ExtractBearer(req.Header()); err == nil {
				user, err := LookupBearer(ctx, q, tok)
				if err != nil {
					return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid bearer token"))
				}
				ctx = WithUser(ctx, user)
				return next(ctx, req)
			}
			// The session-revocation middleware has already loaded and
			// vetted the user for any session that survives; reuse it
			// rather than reading the same row twice per request.
			if _, ok := CurrentUser(ctx); ok {
				return next(ctx, req)
			}
			userIDStr := sm.GetString(ctx, "user_id")
			if userIDStr == "" {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
			}
			userID, err := uuid.Parse(userIDStr)
			if err != nil {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid session"))
			}
			user, err := q.GetUserByID(ctx, userID)
			if err != nil {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("session refers to missing user"))
			}
			// Reached only when the middleware is not in the chain (tests
			// drive services directly). Check the watermark here too, so
			// the guarantee doesn't depend on the wiring.
			if !SessionSurvivesRevocation(sm, ctx, user) {
				return nil, connect.NewError(connect.CodeUnauthenticated,
					errors.New("session ended by a credential change; sign in again"))
			}
			ctx = WithUser(ctx, user)
			return next(ctx, req)
		}
	}
}

// CurrentUser returns the authenticated user attached by NewAuthInterceptor.
func CurrentUser(ctx context.Context) (sqlcgen.User, bool) {
	u, ok := ctx.Value(ctxUser).(sqlcgen.User)
	return u, ok
}

// timestampOrNow returns ts converted to a Timestamptz when the protobuf
// Timestamp is actually populated, otherwise the current time. The plain
// `proto.GetX().AsTime()` pattern is unsafe: when the field is unset
// (nil pointer), AsTime returns the Unix epoch (1970-01-01), not the Go
// zero value, so an .IsZero() check passes through bogus 1970 timestamps.
func timestampOrNow(ts *timestamppb.Timestamp) pgtype.Timestamptz {
	if ts == nil {
		return pgtype.Timestamptz{Valid: true, Time: time.Now()}
	}
	return pgtype.Timestamptz{Valid: true, Time: ts.AsTime()}
}
