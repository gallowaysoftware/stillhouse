package rpc

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"

	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1 "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1"
)

type UserService struct {
	q      *sqlcgen.Queries
	logger *slog.Logger
}

func NewUserService(q *sqlcgen.Queries, logger *slog.Logger) *UserService {
	return &UserService{q: q, logger: logger}
}

func (s *UserService) GetMe(
	ctx context.Context,
	req *connect.Request[stillhousev1.GetMeRequest],
) (*connect.Response[stillhousev1.GetMeResponse], error) {
	u, ok := CurrentUser(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	t, err := s.q.GetTenantByID(ctx, u.TenantID)
	if err != nil {
		s.logger.Error("GetMe: tenant lookup", "err", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	return connect.NewResponse(&stillhousev1.GetMeResponse{
		User:   userToProto(u),
		Tenant: tenantToProto(t),
	}), nil
}
