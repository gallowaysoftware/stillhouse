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
)

// APITokenService manages a user's personal access tokens. Tokens are
// per-user; tenants don't share them. The plaintext value is shown
// only by IssueAPIToken — every other RPC sees just the hash.
type APITokenService struct {
	q      *sqlcgen.Queries
	logger *slog.Logger
}

func NewAPITokenService(q *sqlcgen.Queries, logger *slog.Logger) *APITokenService {
	return &APITokenService{q: q, logger: logger}
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
	row, err := s.q.CreateAPIToken(ctx, sqlcgen.CreateAPITokenParams{
		TokenHash: hash,
		TenantID:  u.TenantID,
		UserID:    u.ID,
		Name:      name,
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
	rows, err := s.q.ListAPITokensForUser(ctx, u.ID)
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
	// Ownership check before mutation. Defense in depth — we don't want a
	// guessed id to revoke someone else's token, even from another tenant.
	existing, err := s.q.GetAPITokenRowByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("token not found"))
		}
		s.logger.Error("RevokeAPIToken: lookup", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	if existing.UserID != u.ID {
		// Hide existence — surface the same NotFound as a missing token.
		return nil, connect.NewError(connect.CodeNotFound, errors.New("token not found"))
	}
	if existing.RevokedAt.Valid {
		// Idempotent: revoking an already-revoked token returns the row.
		return connect.NewResponse(&stillhousev1.RevokeAPITokenResponse{Token: apiTokenToProto(existing)}), nil
	}
	row, err := s.q.RevokeAPIToken(ctx, hash)
	if err != nil {
		s.logger.Error("RevokeAPIToken: update", "err", err)
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
