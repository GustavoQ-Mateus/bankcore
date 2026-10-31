package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	httpSwagger "github.com/swaggo/http-swagger/v2"

	"github.com/GustavoQ-Mateus/bankcore/internal/account"
	_ "github.com/GustavoQ-Mateus/bankcore/internal/apidocs"
	"github.com/GustavoQ-Mateus/bankcore/internal/auth"
	"github.com/GustavoQ-Mateus/bankcore/internal/platform/config"
	"github.com/GustavoQ-Mateus/bankcore/internal/platform/database"
	"github.com/GustavoQ-Mateus/bankcore/internal/platform/httpx"
	"github.com/GustavoQ-Mateus/bankcore/internal/simulation"
	"github.com/GustavoQ-Mateus/bankcore/internal/transfer"
)

// @title           BankCore API
// @version         1.3.0
// @description     Núcleo bancário em Go: contas, depósitos, saques e transferências atômicas com ledger append-only. Valores entram/saem em decimal e são operados em centavos int64. v1.1.0 adiciona a fronteira de liquidação com o Liquida (ADR 0004); v1.2.0 fecha o cadastro público de administrador (gate ALLOW_PUBLIC_ADMIN_REGISTER + seed do 1º admin); v1.3.0 adiciona o simulador de carga e as estatísticas do dashboard (SIMULATION_ENABLED, ADR 0005).
// @host            localhost:8080
// @BasePath        /
// @securityDefinitions.apikey  BearerAuth
// @in                          header
// @name                        Authorization
// @description                 Informe o token como: Bearer {token}
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx := context.Background()
	pool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	authSvc := auth.NewService(pool, cfg.JWTSecret, cfg.JWTTTL, cfg.ServiceJWTTTL, cfg.BcryptCost, cfg.AllowPublicAdminRegister)
	accountSvc := account.NewService(pool)

	if cfg.AdminEmail != "" && cfg.AdminPassword != "" {
		created, err := authSvc.EnsureAdmin(ctx, "Administrador", cfg.AdminEmail, cfg.AdminPassword)
		if err != nil {
			return err
		}
		if created {
			log.Printf("admin semeado: %s", cfg.AdminEmail)
		}
	}

	if cfg.SettlementClientID != "" && cfg.SettlementClientSecret != "" {
		created, err := authSvc.EnsureServiceClient(ctx, cfg.SettlementClientID, cfg.SettlementClientSecret)
		if err != nil {
			return err
		}
		if created {
			log.Printf("credencial de liquidação semeada: %s", cfg.SettlementClientID)
		}
	}

	var transferOpts []transfer.Option
	if cfg.LiquidaExternal {
		transferOpts = append(transferOpts, transfer.WithExternalSettlement())
	}
	transferSvc := transfer.NewService(pool, transferOpts...)

	runner := simulation.NewRunner(pool, accountSvc, transferSvc)
	deps := routerDeps{
		authSvc:        authSvc,
		authHandler:    auth.NewHandler(authSvc),
		accountHandler: account.NewHandler(accountSvc),
		transferHandler: transfer.NewHandler(transferSvc, func(r *http.Request, id uuid.UUID) (uuid.UUID, error) {
			acc, err := accountSvc.Get(r.Context(), id)
			if err != nil {
				return uuid.Nil, err
			}
			return acc.OwnerID, nil
		}),
		simulationHandler: simulation.NewHandler(
			runner,
			simulation.NewReporter(pool, transferSvc, runner),
			cfg.SimulationCORSOrigin,
		),
	}
	r := newRouter(cfg.SimulationEnabled, deps)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("BankCore ouvindo em %s", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case <-stop:
		runner.Stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

type routerDeps struct {
	authSvc           *auth.Service
	authHandler       *auth.Handler
	accountHandler    *account.Handler
	transferHandler   *transfer.Handler
	simulationHandler *simulation.Handler
}

func newRouter(simulationEnabled bool, d routerDeps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Get("/swagger/*", httpSwagger.WrapHandler)

	r.Route("/auth", d.authHandler.Routes)

	if simulationEnabled {
		r.Route("/simulation", d.simulationHandler.Routes)
	}

	r.Group(func(r chi.Router) {
		r.Use(d.authSvc.Authenticate)
		r.Route("/accounts", d.accountHandler.Routes)
		r.Route("/transfers", d.transferHandler.Routes)
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole(auth.RoleSettlement))
			r.Route("/settlement", d.transferHandler.SettlementRoutes)
		})
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole(auth.RoleAdmin))
			r.Route("/admin", func(r chi.Router) {
				d.accountHandler.AdminRoutes(r)
				d.transferHandler.AdminRoutes(r)
				d.authHandler.AdminRoutes(r)
			})
		})
	})

	return r
}
