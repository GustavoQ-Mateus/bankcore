package transfer

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/Gustavo-QMateus/bankcore/internal/auth"
	"github.com/Gustavo-QMateus/bankcore/internal/platform/httpx"
	"github.com/Gustavo-QMateus/bankcore/internal/platform/money"
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
}

type transferRequest struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Amount string `json:"amount"`
}

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
