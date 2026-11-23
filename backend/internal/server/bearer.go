package server

import (
	"net/http"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	"github.com/gallowaysoftware/stillhouse/backend/internal/rpc"
)

// bearerUser attaches the user of an Authorization: Bearer token to a plain
// HTTP handler, as the ConnectRPC interceptor does for the API. A token that
// does not resolve is refused, not ignored, so it never falls back to a
// cookie the same request happens to carry.
func bearerUser(q *sqlcgen.Queries, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, err := rpc.ExtractBearer(r.Header)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		user, err := rpc.LookupBearer(r.Context(), q, tok)
		if err != nil {
			http.Error(w, "invalid bearer token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(rpc.WithUser(r.Context(), user)))
	})
}
