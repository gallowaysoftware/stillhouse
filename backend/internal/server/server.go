// Package server wires the HTTP server, ConnectRPC handlers, session
// management, and database pool into a single runnable unit.
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/alexedwards/scs/pgxstore"
	"github.com/alexedwards/scs/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gallowaysoftware/stillhouse/backend/internal/alerting"
	"github.com/gallowaysoftware/stillhouse/backend/internal/config"
	"github.com/gallowaysoftware/stillhouse/backend/internal/db/sqlcgen"
	stillhousev1connect "github.com/gallowaysoftware/stillhouse/backend/internal/genpb/stillhouse/v1/stillhousev1connect"
	"github.com/gallowaysoftware/stillhouse/backend/internal/mailer"
	"github.com/gallowaysoftware/stillhouse/backend/internal/mcp"
	"github.com/gallowaysoftware/stillhouse/backend/internal/rpc"
	"github.com/gallowaysoftware/stillhouse/backend/internal/tenantdb"
	"github.com/gallowaysoftware/stillhouse/backend/internal/version"
)

type Server struct {
	cfg     *config.Config
	logger  *slog.Logger
	pool    *pgxpool.Pool
	session *scs.SessionManager
	http    *http.Server
	// alertRunner evaluates the alert rules on a timer. Started by
	// ListenAndServe and stopped by Shutdown, so it lives exactly as
	// long as the server does.
	alertRunner *alerting.Runner
	stopAlerts  context.CancelFunc
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
	if err := assertRLSEnforced(ctx, pool, logger, cfg.Dev); err != nil {
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

	mailerImpl := mailer.FromEnv(logger)
	// resetURLPrefix is what gets emailed to users; the token is appended
	// to it. STILLHOUSE_BASE_URL=https://stillhouse.example.com in
	// prod; falls back to localhost for dev so the console mailer still
	// produces clickable links.
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	authSvc := rpc.NewAuthService(queries, tdb, sm, logger, mailerImpl, baseURL+"/reset-password?token=", cfg.TrustProxyHeaders)
	tenantSvc := rpc.NewTenantService(pool, queries, logger)
	userSvc := rpc.NewUserService(queries, sm, logger)
	materialSvc := rpc.NewMaterialService(tdb, logger)
	recipeSvc := rpc.NewRecipeService(tdb, logger)
	mashSvc := rpc.NewMashService(tdb, logger)
	fermentationSvc := rpc.NewFermentationService(tdb, logger)
	bulkSvc := rpc.NewBulkService(tdb, logger)
	distillationSvc := rpc.NewDistillationService(tdb, logger)
	barrelSvc := rpc.NewBarrelService(tdb, logger)
	productSvc := rpc.NewProductService(tdb, logger)
	exciseStampSvc := rpc.NewExciseStampService(tdb, logger)
	instrumentSvc := rpc.NewInstrumentService(tdb, logger)
	bottlingSvc := rpc.NewBottlingService(tdb, logger)
	removalSvc := rpc.NewRemovalService(tdb, logger)
	b266Svc := rpc.NewB266Service(tdb, logger)
	auditSvc := rpc.NewAuditService(tdb, logger)
	pricingSvc := rpc.NewPricingService(tdb, logger)
	customerSvc := rpc.NewCustomerService(tdb, logger)
	// The alert evaluator. Fifteen minutes is often enough that a filing
	// deadline or a stamp shortage surfaces the same working day, and
	// rare enough that it is not a load-bearing query pattern.
	alertRunner := alerting.NewRunner(tdb, queries, mailerImpl, baseURL, 15*time.Minute, logger)
	alertSvc := rpc.NewAlertService(tdb, alertRunner, logger)
	journalSvc := rpc.NewJournalService(tdb, logger)
	importSvc := rpc.NewImportService(tdb, logger)
	labSvc := rpc.NewLabService(tdb, logger)
	purchasingSvc := rpc.NewPurchasingService(tdb, logger)
	locationSvc := rpc.NewLocationService(tdb, logger)
	workOrderSvc := rpc.NewWorkOrderService(tdb, logger)
	redistillationSvc := rpc.NewRedistillationService(tdb, logger)
	traceabilitySvc := rpc.NewTraceabilityService(tdb, logger)
	inviteSvc := rpc.NewInviteService(queries, tdb, sm, mailerImpl, logger)
	apiTokenSvc := rpc.NewAPITokenService(tdb, logger)
	// Pure computation against the embedded CRA tables — no DB, no tenant.
	alcoholometrySvc := rpc.NewAlcoholometryService(logger)

	interceptors := connect.WithInterceptors(
		rpc.NewAuthInterceptor(sm, queries),
		rpc.NewRoleGateInterceptor(),
	)

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
	mux.Handle(stillhousev1connect.NewInstrumentServiceHandler(instrumentSvc, interceptors))
	mux.Handle(stillhousev1connect.NewBottlingServiceHandler(bottlingSvc, interceptors))
	mux.Handle(stillhousev1connect.NewRemovalServiceHandler(removalSvc, interceptors))
	mux.Handle(stillhousev1connect.NewB266ServiceHandler(b266Svc, interceptors))
	mux.Handle(stillhousev1connect.NewAuditServiceHandler(auditSvc, interceptors))
	mux.Handle(stillhousev1connect.NewPricingServiceHandler(pricingSvc, interceptors))
	mux.Handle(stillhousev1connect.NewCustomerServiceHandler(customerSvc, interceptors))
	mux.Handle(stillhousev1connect.NewAlertServiceHandler(alertSvc, interceptors))
	mux.Handle(stillhousev1connect.NewJournalServiceHandler(journalSvc, interceptors))
	mux.Handle(stillhousev1connect.NewImportServiceHandler(importSvc, interceptors))
	mux.Handle(stillhousev1connect.NewLabServiceHandler(labSvc, interceptors))
	mux.Handle(stillhousev1connect.NewPurchasingServiceHandler(purchasingSvc, interceptors))
	mux.Handle(stillhousev1connect.NewLocationServiceHandler(locationSvc, interceptors))
	mux.Handle(stillhousev1connect.NewWorkOrderServiceHandler(workOrderSvc, interceptors))
	mux.Handle(stillhousev1connect.NewRedistillationServiceHandler(redistillationSvc, interceptors))
	mux.Handle(stillhousev1connect.NewTraceabilityServiceHandler(traceabilitySvc, interceptors))
	mux.Handle(stillhousev1connect.NewInviteServiceHandler(inviteSvc, interceptors))
	mux.Handle(stillhousev1connect.NewAPITokenServiceHandler(apiTokenSvc, interceptors))
	mux.Handle(stillhousev1connect.NewAlcoholometryServiceHandler(alcoholometrySvc, interceptors))
	mux.Handle("/export/audit.csv", auditExportHandler(sm, tdb, logger))
	mux.Handle("/export/tenant.zip", tenantExportHandler(sm, pool, queries, logger))
	// The monthly close: duty payable, material in and out, cost of
	// sales, as one CSV with its own caveats attached.
	mux.Handle("/export/journal.csv", journalExportHandler(sm, tdb, logger))
	// One bundle per reporting period: the figures as filed, the movements
	// behind each line, the determinations and instruments behind each
	// movement, and the trail.
	mux.Handle("/export/b266-binder.zip", b266BinderHandler(sm, pool, queries, logger))
	// MCP endpoint — non-browser clients (e.g. Claude.ai mobile) speak
	// JSON-RPC over Streamable HTTP here. Auth is Authorization: Bearer
	// sh_..., issued by cmd/mcp-token; the cookie-session middleware
	// further down the chain is a no-op for these requests.
	mux.Handle("/mcp", mcp.NewHandler(mcp.Deps{
		Queries:       queries,
		Bulk:          bulkSvc,
		Barrel:        barrelSvc,
		Recipe:        recipeSvc,
		Product:       productSvc,
		Fermentation:  fermentationSvc,
		Mash:          mashSvc,
		B266:          b266Svc,
		Alcoholometry: alcoholometrySvc,
		Logger:        logger,
	}))
	// What is running. Unauthenticated on purpose: it is the first thing
	// an operator or a monitor asks after a restart, and it discloses
	// nothing a container digest doesn't.
	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version":    version.Version,
			"commit":     version.Commit,
			"build_date": version.BuildDate,
			"release":    version.IsRelease(),
		})
	})
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
		Handler:           securityHeaders(loggingMiddleware(sm.LoadAndSave(sessionRevocation(mux, sm, queries, logger)), logger), cfg.Dev),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second, // big enough for CSV exports
		IdleTimeout:       120 * time.Second,
	}

	return &Server{
		cfg:         cfg,
		logger:      logger,
		pool:        pool,
		session:     sm,
		http:        httpSrv,
		alertRunner: alertRunner,
	}, nil
}

func (s *Server) ListenAndServe() error {
	// The alert evaluator runs for as long as the server does. Started
	// here rather than in New so a server that is constructed and never
	// served — a test, a config check — does not start writing alerts.
	alertCtx, cancel := context.WithCancel(context.Background())
	s.stopAlerts = cancel
	go s.alertRunner.Start(alertCtx)
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.stopAlerts != nil {
		s.stopAlerts()
	}
	if err := s.http.Shutdown(ctx); err != nil {
		return err
	}
	s.pool.Close()
	return nil
}
