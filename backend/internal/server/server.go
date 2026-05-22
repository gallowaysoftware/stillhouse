// Package server wires the HTTP server, ConnectRPC handlers, session
// management, and database pool into a single runnable unit.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/alexedwards/scs/pgxstore"
	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gallowaysoftware/stillhouse/backend/internal/config"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1connect "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1/stillhousev1connect"
	"github.com/gallowaysoftware/stillhouse/backend/internal/rpc"
)

type Server struct {
	cfg     *config.Config
	logger  *slog.Logger
	pool    *pgxpool.Pool
	session *scs.SessionManager
	http    *http.Server
}

func New(cfg *config.Config, logger *slog.Logger) (*Server, error) {
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}

	sm := scs.New()
	sm.Store = pgxstore.New(pool)
	sm.Lifetime = 7 * 24 * time.Hour
	sm.Cookie.Name = "stillhouse_session"
	sm.Cookie.HttpOnly = true
	sm.Cookie.Secure = !cfg.Dev
	sm.Cookie.SameSite = http.SameSiteLaxMode
	sm.Cookie.Path = "/"

	queries := sqlcgen.New(pool)

	authSvc := rpc.NewAuthService(queries, sm, logger)
	tenantSvc := rpc.NewTenantService(pool, queries, logger)
	userSvc := rpc.NewUserService(queries, logger)

	interceptors := connect.WithInterceptors(rpc.NewAuthInterceptor(sm, queries))

	mux := http.NewServeMux()
	mux.Handle(stillhousev1connect.NewAuthServiceHandler(authSvc, interceptors))
	mux.Handle(stillhousev1connect.NewTenantServiceHandler(tenantSvc, interceptors))
	mux.Handle(stillhousev1connect.NewUserServiceHandler(userSvc, interceptors))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           sm.LoadAndSave(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	return &Server{
		cfg:     cfg,
		logger:  logger,
		pool:    pool,
		session: sm,
		http:    httpSrv,
	}, nil
}

func (s *Server) ListenAndServe() error { return s.http.ListenAndServe() }

func (s *Server) Shutdown(ctx context.Context) error {
	if err := s.http.Shutdown(ctx); err != nil {
		return err
	}
	s.pool.Close()
	return nil
}
