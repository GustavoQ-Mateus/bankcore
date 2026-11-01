package simulation

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/GustavoQ-Mateus/bankcore/internal/platform/httpx"
)

const corsMaxAgeSeconds = "600"

type limit struct {
	name     string
	min, max int
}

var (
	accountsLimit  = limit{name: "accounts", min: 2, max: 500}
	transfersLimit = limit{name: "transfers", min: 1, max: 100000}
	rateLimit      = limit{name: "rate", min: 1, max: 500}
	workersLimit   = limit{name: "workers", min: 1, max: 64}
)

type Handler struct {
	runner     *Runner
	reporter   *Reporter
	corsOrigin string
}

func NewHandler(runner *Runner, reporter *Reporter, corsOrigin string) *Handler {
	return &Handler{runner: runner, reporter: reporter, corsOrigin: corsOrigin}
}

func (h *Handler) Routes(r chi.Router) {
	r.Use(h.cors)
	r.Post("/start", h.start)
	r.Post("/stop", h.stop)
	r.Post("/reset", h.reset)
	r.Get("/status", h.status)
	r.Get("/stats", h.stats)
}

type startRequest struct {
	Accounts  *int `json:"accounts"`
	Transfers *int `json:"transfers"`
	Rate      *int `json:"rate"`
	Workers   *int `json:"workers"`
}

// start godoc
// @Summary      Iniciar simulação de carga
// @Description  Recurso de demonstração (SIMULATION_ENABLED=true), sem autenticação (ADR 0005). Campos omitidos usam o default. Limites: accounts 2..500, transfers 1..100000, rate 1..500, workers 1..64. Execução ativa retorna 409 com error.code=SIMULATION_RUNNING.
// @Tags         simulation
// @Accept       json
// @Produce      json
// @Param        body  body      startRequest  false  "configuração da execução"
// @Success      202   {object}  Status
// @Failure      400   {object}  map[string]any
// @Failure      409   {object}  map[string]any  "error.code=SIMULATION_RUNNING"
// @Router       /simulation/start [post]
func (h *Handler) start(w http.ResponseWriter, r *http.Request) {
	var req startRequest
	if err := httpx.DecodeOptional(r, &req); err != nil {
		httpx.Error(w, err)
		return
	}
	cfg, err := configFrom(req)
	if err != nil {
		httpx.Error(w, err)
		return
	}
	status, err := h.runner.Start(cfg)
	if err != nil {
		httpx.Error(w, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, status)
}

// stop godoc
// @Summary      Parar simulação
// @Description  Cancela a execução ativa sem interromper transferências em andamento e responde já em stopped. Sem execução ativa é no-op.
// @Tags         simulation
// @Produce      json
// @Success      200  {object}  Status
// @Router       /simulation/stop [post]
func (h *Handler) stop(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, h.runner.Stop())
}

// reset godoc
// @Summary      Limpar ambiente de simulação
// @Description  Apaga todos os lançamentos, transferências e contas numa transação (exceção de demonstração ao ledger append-only, ADR 0006). Mantém customers e service_clients. Zera version_conflicts e o estado do simulador. Execução ativa retorna 409 com error.code=SIMULATION_RUNNING.
// @Tags         simulation
// @Produce      json
// @Success      200  {object}  Status
// @Failure      409  {object}  map[string]any  "error.code=SIMULATION_RUNNING"
// @Failure      500  {object}  map[string]any
// @Router       /simulation/reset [post]
func (h *Handler) reset(w http.ResponseWriter, r *http.Request) {
	status, err := h.runner.Reset(r.Context())
	if err != nil {
		httpx.Error(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, status)
}

// status godoc
// @Summary      Estado da simulação
// @Tags         simulation
// @Produce      json
// @Success      200  {object}  Status
// @Router       /simulation/status [get]
func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, h.runner.Status())
}

// stats godoc
// @Summary      Estatísticas para o dashboard
// @Description  Agregados do banco num snapshot coerente. transfers.pending e volume_cents.pending contam só PENDING com lançamentos. Valores monetários em centavos.
// @Tags         simulation
// @Produce      json
// @Success      200  {object}  Stats
// @Failure      500  {object}  map[string]any
// @Router       /simulation/stats [get]
func (h *Handler) stats(w http.ResponseWriter, r *http.Request) {
	s, err := h.reporter.Stats(r.Context())
	if err != nil {
		httpx.Error(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, s)
}

func (h *Handler) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")
		if r.Header.Get("Origin") != h.corsOrigin {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", h.corsOrigin)
		if r.Method != http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", corsMaxAgeSeconds)
		w.WriteHeader(http.StatusNoContent)
	})
}

func configFrom(req startRequest) (Config, error) {
	cfg := DefaultConfig()
	fields := []struct {
		value *int
		dst   *int
		limit limit
	}{
		{req.Accounts, &cfg.Accounts, accountsLimit},
		{req.Transfers, &cfg.Transfers, transfersLimit},
		{req.Rate, &cfg.Rate, rateLimit},
		{req.Workers, &cfg.Workers, workersLimit},
	}
	for _, f := range fields {
		if f.value == nil {
			continue
		}
		if *f.value < f.limit.min || *f.value > f.limit.max {
			return Config{}, httpx.ErrValidation(fmt.Sprintf("%s deve estar entre %d e %d", f.limit.name, f.limit.min, f.limit.max))
		}
		*f.dst = *f.value
	}
	return cfg, nil
}
