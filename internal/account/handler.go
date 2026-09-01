package account

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Gustavo-QMateus/bankcore/internal/auth"
	"github.com/Gustavo-QMateus/bankcore/internal/platform/httpx"
	"github.com/Gustavo-QMateus/bankcore/internal/platform/money"
)

const pageSize = 20

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Routes(r chi.Router) {
	r.Post("/", h.open)
	r.Get("/{id}", h.get)
	r.Post("/{id}/deposit", h.deposit)
	r.Post("/{id}/withdraw", h.withdraw)
	r.Get("/{id}/statement", h.statement)
}

func (h *Handler) AdminRoutes(r chi.Router) {
	r.Get("/accounts", h.adminList)
	r.Patch("/accounts/{id}/status", h.adminSetStatus)
}

type amountRequest struct {
	Amount string `json:"amount"`
}

type statusRequest struct {
	Status Status `json:"status"`
}

func (h *Handler) open(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.Error(w, httpx.ErrUnauthorized)
		return
	}
	acc, err := h.svc.Open(r.Context(), userID)
	if err != nil {
		httpx.Error(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, acc)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	acc, err := h.authorized(r)
	if err != nil {
		httpx.Error(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, acc)
}

func (h *Handler) deposit(w http.ResponseWriter, r *http.Request) {
	acc, amount, err := h.parseAmountOp(r)
	if err != nil {
		httpx.Error(w, err)
		return
	}
	updated, err := h.svc.Deposit(r.Context(), acc.ID, amount)
	if err != nil {
		httpx.Error(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, updated)
}

func (h *Handler) withdraw(w http.ResponseWriter, r *http.Request) {
	acc, amount, err := h.parseAmountOp(r)
	if err != nil {
		httpx.Error(w, err)
		return
	}
	updated, err := h.svc.Withdraw(r.Context(), acc.ID, amount)
	if err != nil {
		httpx.Error(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, updated)
}

func (h *Handler) statement(w http.ResponseWriter, r *http.Request) {
	acc, err := h.authorized(r)
	if err != nil {
		httpx.Error(w, err)
		return
	}
	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			page = v
		}
	}
	entries, err := h.svc.Statement(r.Context(), acc.ID, pageSize, (page-1)*pageSize)
	if err != nil {
		httpx.Error(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"page":    page,
		"entries": entries,
	})
}

func (h *Handler) adminList(w http.ResponseWriter, r *http.Request) {
	accounts, err := h.svc.List(r.Context())
	if err != nil {
		httpx.Error(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, accounts)
}

func (h *Handler) adminSetStatus(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Error(w, httpx.ErrValidation("id inválido"))
		return
	}
	var req statusRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, err)
		return
	}
	if req.Status != StatusActive && req.Status != StatusBlocked {
		httpx.Error(w, httpx.ErrValidation("status deve ser ACTIVE ou BLOCKED"))
		return
	}
	acc, err := h.svc.SetStatus(r.Context(), id, req.Status)
	if err != nil {
		httpx.Error(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, acc)
}

func (h *Handler) parseAmountOp(r *http.Request) (Account, int64, error) {
	acc, err := h.authorized(r)
	if err != nil {
		return Account{}, 0, err
	}
	var req amountRequest
	if err := httpx.Decode(r, &req); err != nil {
		return Account{}, 0, err
	}
	amount, err := money.ParseDecimal(req.Amount)
	if err != nil || amount <= 0 {
		return Account{}, 0, httpx.ErrValidation("amount deve ser um valor positivo")
	}
	return acc, amount, nil
}

func (h *Handler) authorized(r *http.Request) (Account, error) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return Account{}, httpx.ErrValidation("id inválido")
	}
	acc, err := h.svc.Get(r.Context(), id)
	if err != nil {
		return Account{}, err
	}
	userID, _ := auth.UserIDFrom(r.Context())
	if auth.RoleFrom(r.Context()) != auth.RoleAdmin && acc.OwnerID != userID {
		return Account{}, httpx.ErrForbidden
	}
	return acc, nil
}
