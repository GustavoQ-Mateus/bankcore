package transfer

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/GustavoQ-Mateus/bankcore/internal/auth"
	"github.com/GustavoQ-Mateus/bankcore/internal/platform/httpx"
	"github.com/GustavoQ-Mateus/bankcore/internal/platform/money"
)

type Handler struct {
	svc     *Service
	ownerOf func(r *http.Request, id uuid.UUID) (uuid.UUID, error)
}

func NewHandler(svc *Service, ownerOf func(r *http.Request, id uuid.UUID) (uuid.UUID, error)) *Handler {
	return &Handler{svc: svc, ownerOf: ownerOf}
}

func (h *Handler) Routes(r chi.Router) {
	r.Post("/", h.create)
	r.Get("/", h.listMine)
	r.Get("/{id}", h.get)
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireRole(auth.RoleSettlement))
		r.Patch("/{id}/settle", h.settle)
		r.Patch("/{id}/fail", h.fail)
	})
}

func (h *Handler) AdminRoutes(r chi.Router) {
	r.Get("/transfers", h.adminList)
}

type transferRequest struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Amount string `json:"amount"`
}

// create godoc
// @Summary  Transferência atômica entre contas
// @Tags     transfers
// @Accept   json
// @Produce  json
// @Security BearerAuth
// @Param    Idempotency-Key  header    string           true  "chave de idempotência"
// @Param    body             body      transferRequest  true  "origem, destino e valor"
// @Success  201              {object}  Transfer
// @Failure  400              {object}  map[string]any
// @Failure  403              {object}  map[string]any
// @Failure  409              {object}  map[string]any
// @Router   /transfers [post]
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req transferRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, err)
		return
	}

	from, err := uuid.Parse(req.From)
	if err != nil {
		httpx.Error(w, httpx.ErrValidation("from inválido"))
		return
	}
	to, err := uuid.Parse(req.To)
	if err != nil {
		httpx.Error(w, httpx.ErrValidation("to inválido"))
		return
	}
	amount, err := money.ParseDecimal(req.Amount)
	if err != nil || amount <= 0 {
		httpx.Error(w, httpx.ErrValidation("amount deve ser um valor positivo"))
		return
	}

	if err := h.authorizeSource(r, from); err != nil {
		httpx.Error(w, err)
		return
	}

	key := r.Header.Get("Idempotency-Key")
	t, err := h.svc.Transfer(r.Context(), from, to, amount, key)
	if err != nil {
		httpx.Error(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, t)
}

// get godoc
// @Summary  Consultar transferência
// @Tags     transfers
// @Produce  json
// @Security BearerAuth
// @Param    id   path      string  true  "transfer id"
// @Success  200  {object}  Transfer
// @Failure  403  {object}  map[string]any
// @Failure  404  {object}  map[string]any
// @Router   /transfers/{id} [get]
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, httpx.ErrValidation("id inválido"))
		return
	}
	t, err := h.svc.Get(r.Context(), id)
	if err != nil {
		httpx.Error(w, err)
		return
	}
	if err := h.authorizeParticipant(r, t); err != nil {
		httpx.Error(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, t)
}

// adminList godoc
// @Summary  Listar transferências por status (ADMIN)
// @Tags     admin
// @Produce  json
// @Security BearerAuth
// @Param    status  query     string  true   "PENDING, SETTLED ou FAILED"
// @Param    page    query     int     false  "página"
// @Success  200     {object}  map[string]any
// @Failure  403     {object}  map[string]any
// @Router   /admin/transfers [get]
func (h *Handler) adminList(w http.ResponseWriter, r *http.Request) {
	status := Status(r.URL.Query().Get("status"))
	if status != StatusPending && status != StatusSettled && status != StatusFailed {
		httpx.Error(w, httpx.ErrValidation("status deve ser PENDING, SETTLED ou FAILED"))
		return
	}
	page := pageParam(r)
	transfers, err := h.svc.ListByStatus(r.Context(), status, pageSize, (page-1)*pageSize)
	if err != nil {
		httpx.Error(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"page":      page,
		"status":    status,
		"transfers": transfers,
	})
}

// listMine godoc
// @Summary  Listar minhas transferências por status
// @Tags     transfers
// @Produce  json
// @Security BearerAuth
// @Param    status  query     string  true   "PENDING, SETTLED ou FAILED"
// @Param    page    query     int     false  "página"
// @Success  200     {object}  map[string]any
// @Router   /transfers [get]
func (h *Handler) listMine(w http.ResponseWriter, r *http.Request) {
	status := Status(r.URL.Query().Get("status"))
	if status != StatusPending && status != StatusSettled && status != StatusFailed {
		httpx.Error(w, httpx.ErrValidation("status deve ser PENDING, SETTLED ou FAILED"))
		return
	}
	userID, _ := auth.UserIDFrom(r.Context())
	page := pageParam(r)
	transfers, err := h.svc.ListByStatusForOwner(r.Context(), status, userID, pageSize, (page-1)*pageSize)
	if err != nil {
		httpx.Error(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"page":      page,
		"status":    status,
		"transfers": transfers,
	})
}

type settleRequest struct {
	SettlementRef string `json:"settlement_ref"`
}

// settle godoc
// @Summary  Confirmar liquidação (SETTLEMENT)
// @Tags     settlement
// @Accept   json
// @Produce  json
// @Security BearerAuth
// @Param    id    path      string         true   "transfer id"
// @Param    body  body      settleRequest  false  "referência da liquidação"
// @Success  200   {object}  Transfer
// @Failure  403   {object}  map[string]any
// @Failure  404   {object}  map[string]any
// @Failure  409   {object}  map[string]any
// @Router   /transfers/{id}/settle [patch]
func (h *Handler) settle(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, httpx.ErrValidation("id inválido"))
		return
	}
	var req settleRequest
	if r.ContentLength > 0 {
		if err := httpx.Decode(r, &req); err != nil {
			httpx.Error(w, err)
			return
		}
	}
	t, err := h.svc.Settle(r.Context(), id, req.SettlementRef)
	if err != nil {
		httpx.Error(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, t)
}

// fail godoc
// @Summary  Falhar liquidação com estorno (SETTLEMENT)
// @Tags     settlement
// @Produce  json
// @Security BearerAuth
// @Param    id   path      string  true  "transfer id"
// @Success  200  {object}  Transfer
// @Failure  403  {object}  map[string]any
// @Failure  404  {object}  map[string]any
// @Failure  409  {object}  map[string]any
// @Router   /transfers/{id}/fail [patch]
func (h *Handler) fail(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, httpx.ErrValidation("id inválido"))
		return
	}
	t, err := h.svc.Fail(r.Context(), id)
	if err != nil {
		httpx.Error(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, t)
}

const pageSize = 50

func pageParam(r *http.Request) int {
	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			page = v
		}
	}
	return page
}

func (h *Handler) authorizeParticipant(r *http.Request, t Transfer) error {
	if auth.RoleFrom(r.Context()) == auth.RoleAdmin {
		return nil
	}
	userID, _ := auth.UserIDFrom(r.Context())
	for _, acc := range []uuid.UUID{t.FromAccountID, t.ToAccountID} {
		ownerID, err := h.ownerOf(r, acc)
		if err != nil {
			return err
		}
		if ownerID == userID {
			return nil
		}
	}
	return httpx.ErrForbidden
}

func (h *Handler) authorizeSource(r *http.Request, from uuid.UUID) error {
	if auth.RoleFrom(r.Context()) == auth.RoleAdmin {
		return nil
	}
	ownerID, err := h.ownerOf(r, from)
	if err != nil {
		return err
	}
	userID, _ := auth.UserIDFrom(r.Context())
	if ownerID != userID {
		return httpx.ErrForbidden
	}
	return nil
}
