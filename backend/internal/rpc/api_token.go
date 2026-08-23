package rpc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
)

// APITokenService manages a user's personal access tokens. Tokens are
// per-user; tenants don't share them. The plaintext value is shown
// only by IssueAPIToken — every other RPC sees just the hash.
//
// Every method here runs inside WithTenantTx. api_tokens has been under
// row-level security since migration 000033, so the tenant GUC is what
// makes these queries see anything at all — the Go-side ownership check
// in RevokeAPIToken is now the second line of defence rather than the
// only one. The bearer-auth path is the one caller that legitimately has
// no tenant yet; it goes through the SECURITY DEFINER keyhole instead
// (see GetAPITokenByHash).
type APITokenService struct {
	tdb    *tenantdb.DB
	logger *slog.Logger
}

func NewAPITokenService(tdb *tenantdb.DB, logger *slog.Logger) *APITokenService {
	return &APITokenService{tdb: tdb, logger: logger}
}

// IssueAPIToken mints a new token, returning the plaintext exactly
// once. The caller is responsible for showing it to the user
// immediately with a clear "won't be shown again" warning.
func (s *APITokenService) IssueAPIToken(
	ctx context.Context,
	req *connect.Request[stillhousev1.IssueAPITokenRequest],
) (*connect.Response[stillhousev1.IssueAPITokenResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	name := strings.TrimSpace(req.Msg.GetName())
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}
	if len(name) > 100 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is too long (max 100 chars)"))
	}

	plaintext, hash, err := generateAPIToken()
	if err != nil {
		s.logger.Error("IssueAPIToken: rand", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	var row sqlcgen.ApiToken
	err = s.tdb.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var err error
		row, err = q.CreateAPIToken(ctx, sqlcgen.CreateAPITokenParams{
			TokenHash: hash,
			TenantID:  u.TenantID,
			UserID:    u.ID,
			Name:      name,
		})
		return err
	})
	if err != nil {
		s.logger.Error("IssueAPIToken: insert", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.IssueAPITokenResponse{
		Token:     apiTokenToProto(row),
		Plaintext: plaintext,
	}), nil
}

func (s *APITokenService) ListAPITokens(
	ctx context.Context,
	_ *connect.Request[stillhousev1.ListAPITokensRequest],
) (*connect.Response[stillhousev1.ListAPITokensResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	var rows []sqlcgen.ApiToken
	err := s.tdb.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		var err error
		rows, err = q.ListAPITokensForUser(ctx, u.ID)
		return err
	})
	if err != nil {
		s.logger.Error("ListAPITokens", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	out := make([]*stillhousev1.APIToken, 0, len(rows))
	for _, r := range rows {
		out = append(out, apiTokenToProto(r))
	}
	return connect.NewResponse(&stillhousev1.ListAPITokensResponse{Tokens: out}), nil
}

// RevokeAPIToken marks the token revoked. The id is the URL-safe
// base64 of the token's SHA-256 hash (as returned by Issue/List).
// Ownership is enforced before the update: a user can revoke only
// their own tokens, regardless of role.
//
// The lookup and the update share one transaction, so the ownership
// check cannot be raced by a concurrent revoke, and the RLS policy
// scopes both halves to the caller's tenant.
func (s *APITokenService) RevokeAPIToken(
	ctx context.Context,
	req *connect.Request[stillhousev1.RevokeAPITokenRequest],
) (*connect.Response[stillhousev1.RevokeAPITokenResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	hash, err := base64.RawURLEncoding.DecodeString(req.Msg.GetId())
	if err != nil || len(hash) != sha256.Size {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid token id"))
	}

	var row sqlcgen.ApiToken
	err = s.tdb.WithTenantTx(ctx, u.TenantID, func(ctx context.Context, q *sqlcgen.Queries) error {
		// Ownership check before mutation. Defence in depth — a guessed id
		// must not revoke someone else's token. RLS already keeps another
		// tenant's rows invisible here; this catches another *user* inside
		// the same tenant.
		existing, err := q.GetAPITokenRowByHash(ctx, hash)
		if err != nil {
			return err
		}
		if existing.UserID != u.ID {
			// Hide existence — surface the same NotFound as a missing token.
			return pgx.ErrNoRows
		}
		if existing.RevokedAt.Valid {
			// Idempotent: revoking an already-revoked token returns the row.
			row = existing
			return nil
		}
		row, err = q.RevokeAPIToken(ctx, hash)
		return err
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("token not found"))
		}
		s.logger.Error("RevokeAPIToken", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.RevokeAPITokenResponse{Token: apiTokenToProto(row)}), nil
}

// generateAPIToken returns (plaintext, sha256(plaintext)). Same
// recipe used by cmd/mcp-token; both routes produce indistinguishable
// tokens on the wire.
func generateAPIToken() (string, []byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	tok := APITokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(tok))
	return tok, sum[:], nil
}

func apiTokenToProto(r sqlcgen.ApiToken) *stillhousev1.APIToken {
	out := &stillhousev1.APIToken{
		Id:        base64.RawURLEncoding.EncodeToString(r.TokenHash),
		Name:      r.Name,
		CreatedAt: timestamppb.New(r.CreatedAt.Time),
	}
	if r.LastUsedAt.Valid {
		out.LastUsedAt = timestamppb.New(r.LastUsedAt.Time)
	}
	if r.RevokedAt.Valid {
		out.RevokedAt = timestamppb.New(r.RevokedAt.Time)
	}
	return out
}
