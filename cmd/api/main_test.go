package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/GustavoQ-Mateus/bankcore/internal/account"
	"github.com/GustavoQ-Mateus/bankcore/internal/auth"
	"github.com/GustavoQ-Mateus/bankcore/internal/simulation"
	"github.com/GustavoQ-Mateus/bankcore/internal/transfer"
)

func testRouter(simulationEnabled bool) http.Handler {
	authSvc := auth.NewService(nil, "test-secret", time.Hour, 15*time.Minute, 4, false)
	runner := simulation.NewRunner(nil, nil, nil)
	return newRouter(simulationEnabled, routerDeps{
		authSvc:        authSvc,
		authHandler:    auth.NewHandler(authSvc),
		accountHandler: account.NewHandler(account.NewService(nil)),
		transferHandler: transfer.NewHandler(transfer.NewService(nil), func(r *http.Request, id uuid.UUID) (uuid.UUID, error) {
			return uuid.Nil, nil
		}),
		simulationHandler: simulation.NewHandler(runner, nil, "http://localhost:4200"),
	})
}

func TestRouter_SimulationDisabledReturnsNotFound(t *testing.T) {
	router := testRouter(false)
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/simulation/start"},
		{http.MethodPost, "/simulation/stop"},
		{http.MethodPost, "/simulation/reset"},
		{http.MethodGet, "/simulation/status"},
		{http.MethodGet, "/simulation/stats"},
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(route.method, route.path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, quero 404", route.method, route.path, rec.Code)
		}
	}
}

func TestRouter_SimulationEnabledServesStatusWithoutAuth(t *testing.T) {
	rec := httptest.NewRecorder()
	testRouter(true).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/simulation/status", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET /simulation/status = %d, quero 200", rec.Code)
	}
}
