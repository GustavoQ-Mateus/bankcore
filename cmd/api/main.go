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

	"github.com/GustavoQ-Mateus/bankcore/internal/account"
	"github.com/GustavoQ-Mateus/bankcore/internal/auth"
	"github.com/GustavoQ-Mateus/bankcore/internal/platform/config"
	"github.com/GustavoQ-Mateus/bankcore/internal/platform/database"
	"github.com/GustavoQ-Mateus/bankcore/internal/platform/httpx"
	"github.com/GustavoQ-Mateus/bankcore/internal/transfer"
)

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

	authSvc := auth.NewService(pool, cfg.JWTSecret, cfg.JWTTTL, cfg.BcryptCost)
	accountSvc := account.NewService(pool)
	transferSvc := transfer.NewService(pool)

	authHandler := auth.NewHandler(authSvc)
	accountHandler := account.NewHandler(accountSvc)
	transferHandler := transfer.NewHandler(transferSvc, func(r *http.Request, id uuid.UUID) (uuid.UUID, error) {
		acc, err := accountSvc.Get(r.Context(), id)
		if err != nil {
			return uuid.Nil, err
		}
		return acc.OwnerID, nil
	})

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	r.Route("/auth", authHandler.Routes)

	r.Group(func(r chi.Router) {
		r.Use(authSvc.Authenticate)
		r.Route("/accounts", accountHandler.Routes)
		r.Route("/transfers", transferHandler.Routes)
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole(auth.RoleAdmin))
			r.Route("/admin", accountHandler.AdminRoutes)
		})
	})

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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
