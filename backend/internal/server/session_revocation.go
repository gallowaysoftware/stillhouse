package server

import (
	"log/slog"
	"net/http"

	"github.com/alexedwards/scs/v2"
	"github.com/google/uuid"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/rpc"
)

// sessionRevocation destroys any session whose authentication predates
// the owning user's revocation watermark, before the request reaches
// anything that trusts it.
//
// It sits here rather than in the Connect auth interceptor because the
// interceptor is not the only thing that reads a session: the audit
// export, the tenant export and the B266 binder are plain HTTP handlers
// that check scs directly. Putting the check in the one place every
// request passes through means a session-gated handler added later is
// covered without anyone remembering to cover it.
//
// A surviving session's user is attached to the request context, so the
// interceptor downstream reuses it instead of reading the same row a
// second time.
func sessionRevocation(next http.Handler, sm *scs.SessionManager, q *sqlcgen.Queries, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		idStr := sm.GetString(ctx, "user_id")
		if idStr == "" {
			next.ServeHTTP(w, r) // no session: bearer token, or a public route
			return
		}
		userID, err := uuid.Parse(idStr)
		if err != nil {
			next.ServeHTTP(w, r) // malformed; downstream turns it into a 401
			return
		}
		user, err := q.GetUserByID(ctx, userID)
		if err != nil {
			next.ServeHTTP(w, r) // missing user; downstream turns it into a 401
			return
		}
		if !rpc.SessionSurvivesRevocation(sm, ctx, user) {
			if err := sm.Destroy(ctx); err != nil {
				logger.Error("session revocation: destroy", "err", err, "user_id", userID)
				// Destroy failed, so the cookie may survive the response.
				// Refuse the request rather than serve it on a session
				// that should no longer exist.
				http.Error(w, "session expired; sign in again", http.StatusUnauthorized)
				return
			}
			logger.Info("session revoked by credential change", "user_id", userID)
			next.ServeHTTP(w, r) // session is gone; downstream sees no user
			return
		}
		next.ServeHTTP(w, r.WithContext(rpc.WithUser(ctx, user)))
	})
}
