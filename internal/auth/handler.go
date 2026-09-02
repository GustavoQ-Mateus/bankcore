package auth

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/GustavoQ-Mateus/bankcore/internal/platform/httpx"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Routes(r chi.Router) {
	r.Post("/register", h.register)
	r.Post("/login", h.login)
}

type registerRequest struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     Role   `json:"role"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// register godoc
// @Summary  Registrar cliente
// @Tags     auth
// @Accept   json
// @Produce  json
// @Param    body  body      registerRequest  true  "dados do cliente"
// @Success  201   {object}  Customer
// @Failure  400   {object}  map[string]any
// @Failure  409   {object}  map[string]any
// @Router   /auth/register [post]
func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, err)
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Name == "" || req.Email == "" || len(req.Password) < 6 {
		httpx.Error(w, httpx.ErrValidation("name, email e password (mínimo 6) são obrigatórios"))
		return
	}

	c, err := h.svc.Register(r.Context(), req.Name, req.Email, req.Password, req.Role)
	if err != nil {
		httpx.Error(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, c)
}

// login godoc
// @Summary  Login e emissão de JWT
// @Tags     auth
// @Accept   json
// @Produce  json
// @Param    body  body      loginRequest  true  "credenciais"
// @Success  200   {object}  map[string]any
// @Failure  401   {object}  map[string]any
// @Router   /auth/login [post]
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, err)
		return
	}

	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Email == "" || req.Password == "" {
		httpx.Error(w, httpx.ErrValidation("email e password são obrigatórios"))
		return
	}

	token, c, err := h.svc.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		httpx.Error(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"token":    token,
		"customer": c,
	})
}
