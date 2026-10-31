package simulation_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GustavoQ-Mateus/bankcore/internal/simulation"
)

const dashboardOrigin = "http://localhost:4200"

func simulationRouter(h *simulation.Handler) http.Handler {
	r := chi.NewRouter()
	r.Route("/simulation", h.Routes)
	return r
}

func offlineRouter() http.Handler {
	return simulationRouter(simulation.NewHandler(simulation.NewRunner(nil, nil, nil), nil, dashboardOrigin))
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decodificar erro: %v (%s)", err, rec.Body.String())
	}
	return body.Error.Code
}

func TestStart_OutOfRangeConfigReturnsValidation(t *testing.T) {
	router := offlineRouter()
	for _, body := range []string{
		`{"accounts":1}`, `{"accounts":501}`,
		`{"transfers":0}`, `{"transfers":100001}`,
		`{"rate":0}`, `{"rate":501}`,
		`{"workers":0}`, `{"workers":65}`,
		`{"desconhecido":1}`,
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/simulation/start", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, quero 400", body, rec.Code)
			continue
		}
		if code := errorCode(t, rec); code != "VALIDATION" {
			t.Errorf("%s: code = %s, quero VALIDATION", body, code)
		}
	}
}

func TestStatus_IdleHasNullRunFieldsAndDefaults(t *testing.T) {
	rec := httptest.NewRecorder()
	offlineRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/simulation/status", nil))

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if body["phase"] != "idle" {
		t.Errorf("phase = %v, quero idle", body["phase"])
	}
	for _, key := range []string{"run_id", "started_at", "finished_at"} {
		value, present := body[key]
		if !present || value != nil {
			t.Errorf("%s = %v (presente=%v), quero null", key, value, present)
		}
	}
	config := body["config"].(map[string]any)
	if config["accounts"] != 40.0 || config["transfers"] != 1500.0 || config["rate"] != 60.0 || config["workers"] != 16.0 {
		t.Errorf("config = %v, quero os defaults", config)
	}
}

func TestStop_IdleIsNoop(t *testing.T) {
	rec := httptest.NewRecorder()
	offlineRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/simulation/stop", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"phase":"idle"`) {
		t.Errorf("corpo = %s, quero phase idle", rec.Body.String())
	}
}

func TestCORS_PreflightFromDashboardReturnsNoContent(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/simulation/start", nil)
	req.Header.Set("Origin", dashboardOrigin)
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "content-type")
	rec := httptest.NewRecorder()
	offlineRouter().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, quero 204", rec.Code)
	}
	want := map[string]string{
		"Access-Control-Allow-Origin":  dashboardOrigin,
		"Access-Control-Allow-Methods": "GET, POST, OPTIONS",
		"Access-Control-Allow-Headers": "Content-Type",
		"Access-Control-Max-Age":       "600",
	}
	for header, value := range want {
		if got := rec.Header().Get(header); got != value {
			t.Errorf("%s = %q, quero %q", header, got, value)
		}
	}
}

func TestCORS_OtherOriginGetsNoAllowOrigin(t *testing.T) {
	for _, method := range []string{http.MethodOptions, http.MethodGet} {
		req := httptest.NewRequest(method, "/simulation/status", nil)
		req.Header.Set("Origin", "http://evil.example")
		req.Header.Set("Access-Control-Request-Method", http.MethodGet)
		rec := httptest.NewRecorder()
		offlineRouter().ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("%s: Access-Control-Allow-Origin = %q, quero ausente", method, got)
		}
	}
}

func TestCORS_SimpleRequestFromDashboardAllowsOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/simulation/status", nil)
	req.Header.Set("Origin", dashboardOrigin)
	rec := httptest.NewRecorder()
	offlineRouter().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != dashboardOrigin {
		t.Errorf("Access-Control-Allow-Origin = %q, quero %q", got, dashboardOrigin)
	}
}

func TestCORS_ResetPreflightFromDashboardReturnsNoContent(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/simulation/reset", nil)
	req.Header.Set("Origin", dashboardOrigin)
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rec := httptest.NewRecorder()
	offlineRouter().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, quero 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != dashboardOrigin {
		t.Errorf("Access-Control-Allow-Origin = %q, quero %q", got, dashboardOrigin)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST, OPTIONS" {
		t.Errorf("Access-Control-Allow-Methods = %q, quero GET, POST, OPTIONS", got)
	}
}
