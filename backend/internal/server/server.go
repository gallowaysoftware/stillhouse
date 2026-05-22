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
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
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
	tdb := tenantdb.New(pool)

	authSvc := rpc.NewAuthService(queries, sm, logger)
	tenantSvc := rpc.NewTenantService(pool, queries, logger)
	userSvc := rpc.NewUserService(queries, logger)
	materialSvc := rpc.NewMaterialService(tdb, logger)
	recipeSvc := rpc.NewRecipeService(tdb, logger)
	mashSvc := rpc.NewMashService(tdb, logger)
	fermentationSvc := rpc.NewFermentationService(tdb, logger)
	bulkSvc := rpc.NewBulkService(tdb, logger)
	distillationSvc := rpc.NewDistillationService(tdb, logger)
	barrelSvc := rpc.NewBarrelService(tdb, logger)
	productSvc := rpc.NewProductService(tdb, logger)
	exciseStampSvc := rpc.NewExciseStampService(tdb, logger)
	bottlingSvc := rpc.NewBottlingService(tdb, logger)
	removalSvc := rpc.NewRemovalService(tdb, logger)
	b266Svc := rpc.NewB266Service(tdb, logger)
	auditSvc := rpc.NewAuditService(tdb, logger)

	interceptors := connect.WithInterceptors(rpc.NewAuthInterceptor(sm, queries))

	mux := http.NewServeMux()
	mux.Handle(stillhousev1connect.NewAuthServiceHandler(authSvc, interceptors))
	mux.Handle(stillhousev1connect.NewTenantServiceHandler(tenantSvc, interceptors))
	mux.Handle(stillhousev1connect.NewUserServiceHandler(userSvc, interceptors))
	mux.Handle(stillhousev1connect.NewMaterialServiceHandler(materialSvc, interceptors))
	mux.Handle(stillhousev1connect.NewRecipeServiceHandler(recipeSvc, interceptors))
	mux.Handle(stillhousev1connect.NewMashServiceHandler(mashSvc, interceptors))
	mux.Handle(stillhousev1connect.NewFermentationServiceHandler(fermentationSvc, interceptors))
	mux.Handle(stillhousev1connect.NewBulkServiceHandler(bulkSvc, interceptors))
	mux.Handle(stillhousev1connect.NewDistillationServiceHandler(distillationSvc, interceptors))
	mux.Handle(stillhousev1connect.NewBarrelServiceHandler(barrelSvc, interceptors))
	mux.Handle(stillhousev1connect.NewProductServiceHandler(productSvc, interceptors))
	mux.Handle(stillhousev1connect.NewExciseStampServiceHandler(exciseStampSvc, interceptors))
	mux.Handle(stillhousev1connect.NewBottlingServiceHandler(bottlingSvc, interceptors))
	mux.Handle(stillhousev1connect.NewRemovalServiceHandler(removalSvc, interceptors))
	mux.Handle(stillhousev1connect.NewB266ServiceHandler(b266Svc, interceptors))
	mux.Handle(stillhousev1connect.NewAuditServiceHandler(auditSvc, interceptors))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	if cfg.StaticDir != "" {
		mux.Handle("/", staticSPAHandler(cfg.StaticDir))
	}

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
