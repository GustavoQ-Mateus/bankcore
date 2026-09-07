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
	r.Post("/token", h.token)
}

func (h *Handler) AdminRoutes(r chi.Router) {
	r.Post("/service-clients", h.createServiceClient)
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

type tokenRequest struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

// register godoc
// @Summary  Registrar cliente
// @Tags     auth
// @Accept   json
// @Produce  json
// @Param    body  body      registerRequest  true  "dados do cliente"
// @Success  201   {object}  Customer
// @Failure  400   {object}  map[string]any
// @Failure  403   {object}  map[string]any
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

// token godoc
// @Summary  Token de serviço (client-credentials)
// @Description  Fluxo M2M do Liquida: troca client_id/client_secret por um JWT role=SETTLEMENT com TTL curto. Ver ADR 0004.
// @Tags     auth
// @Accept   json
// @Produce  json
// @Param    body  body      tokenRequest  true  "credenciais de serviço"
// @Success  200   {object}  map[string]any
// @Failure  401   {object}  map[string]any
// @Router   /auth/token [post]
func (h *Handler) token(w http.ResponseWriter, r *http.Request) {
	var req tokenRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, err)
		return
	}
	if req.ClientID == "" || req.ClientSecret == "" {
		httpx.Error(w, httpx.ErrValidation("client_id e client_secret são obrigatórios"))
		return
	}

	token, expiresIn, err := h.svc.IssueServiceToken(r.Context(), req.ClientID, req.ClientSecret)
	if err != nil {
		httpx.Error(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"token":      token,
		"token_type": "Bearer",
		"expires_in": expiresIn,
	})
}

// createServiceClient godoc
// @Summary  Provisionar credencial de serviço (ADMIN)
// @Description  Gera client_id/client_secret para o Liquida. O client_secret é exibido UMA ÚNICA VEZ.
// @Tags     admin
// @Produce  json
// @Security BearerAuth
// @Success  201  {object}  map[string]any
// @Failure  403  {object}  map[string]any
// @Router   /admin/service-clients [post]
func (h *Handler) createServiceClient(w http.ResponseWriter, r *http.Request) {
	sc, secret, err := h.svc.CreateServiceClient(r.Context())
	if err != nil {
		httpx.Error(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"client":        sc,
		"client_secret": secret,
		"warning":       "guarde o client_secret agora: ele não será exibido novamente",
	})
}
