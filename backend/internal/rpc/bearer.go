package rpc

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
)

// APITokenPrefix is the visible prefix on every personal access token
// the server issues. Recognisable in a log, search-able in a paste, and
// distinct enough that a scanner can flag a leak.
const APITokenPrefix = "sh_"

// ErrNoBearer is returned by LookupBearer when the request carries no
// Authorization: Bearer header. Callers that also accept session auth
// treat this as "fall through to session lookup".
var ErrNoBearer = errors.New("no bearer token")

// ErrBadBearer is returned for a malformed or revoked token. Distinct
// from ErrNoBearer so callers can return 401 rather than continue.
var ErrBadBearer = errors.New("invalid bearer token")

// ExtractBearer pulls the token value out of an Authorization header.
// Returns ErrNoBearer when the header is absent or the scheme is
// something other than Bearer.
func ExtractBearer(h http.Header) (string, error) {
	v := h.Get("Authorization")
	if v == "" {
		return "", ErrNoBearer
	}
	// case-insensitive scheme match per RFC 7235
	if len(v) < 7 || !strings.EqualFold(v[:7], "Bearer ") {
		return "", ErrNoBearer
	}
	tok := strings.TrimSpace(v[7:])
	if tok == "" {
		return "", ErrNoBearer
	}
	return tok, nil
}

// LookupBearer hashes tok, looks it up in api_tokens, and returns the
// owning user. Updates last_used_at on every hit — best-effort, errors
// are swallowed so token use never fails due to telemetry.
func LookupBearer(ctx context.Context, q *sqlcgen.Queries, tok string) (sqlcgen.User, error) {
	if !strings.HasPrefix(tok, APITokenPrefix) {
		return sqlcgen.User{}, ErrBadBearer
	}
	sum := sha256.Sum256([]byte(tok))
	row, err := q.GetAPITokenByHash(ctx, sum[:])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlcgen.User{}, ErrBadBearer
		}
		return sqlcgen.User{}, err
	}
	_ = q.TouchAPIToken(ctx, sum[:]) // best-effort
	return sqlcgen.User{
		ID:          row.UserID,
		TenantID:    row.TenantID,
		Email:       row.UserEmail,
		DisplayName: row.UserDisplayName,
		Role:        row.UserRole,
	}, nil
}

// WithUser attaches u to ctx so downstream RPC handlers see it via
// CurrentUser. Used by the MCP server (which authenticates via bearer
// tokens outside the Connect interceptor chain) to invoke RPC service
// methods with a fully-formed context.
func WithUser(ctx context.Context, u sqlcgen.User) context.Context {
	return context.WithValue(ctx, ctxUser, u)
}
