package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/Gustavo-QMateus/bankcore/internal/platform/httpx"
)

type contextKey string

const (
	ctxUserID contextKey = "userID"
	ctxRole   contextKey = "role"
)

func (s *Service) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			httpx.Error(w, httpx.ErrUnauthorized)
			return
		}
		raw := strings.TrimPrefix(header, "Bearer ")

		id, role, err := s.parseToken(raw)
		if err != nil {
			httpx.Error(w, httpx.ErrUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), ctxUserID, id)
		ctx = context.WithValue(ctx, ctxRole, role)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func RequireRole(role Role) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if RoleFrom(r.Context()) != role {
				httpx.Error(w, httpx.ErrForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func UserIDFrom(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(ctxUserID).(uuid.UUID)
	return id, ok
}

func RoleFrom(ctx context.Context) Role {
	role, _ := ctx.Value(ctxRole).(Role)
	return role
}
