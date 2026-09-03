package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/GustavoQ-Mateus/bankcore/internal/auth"
)

func setupAuth(t *testing.T) (*auth.Service, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("bankcore"),
		tcpostgres.WithUsername("bankcore"),
		tcpostgres.WithPassword("bankcore"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("iniciar postgres: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	for _, m := range []string{"0001_init.up.sql", "0002_settlement.up.sql"} {
		schema, err := os.ReadFile("../../migrations/" + m)
		if err != nil {
			t.Fatalf("ler migration %s: %v", m, err)
		}
		if _, err := pool.Exec(ctx, string(schema)); err != nil {
			t.Fatalf("aplicar migration %s: %v", m, err)
		}
	}

	return auth.NewService(pool, "test-secret", time.Hour, 15*time.Minute, 4), pool
}

func protectedServer(svc *auth.Service) *httptest.Server {
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(svc.Authenticate)
		r.With(auth.RequireRole(auth.RoleSettlement)).Get("/protected", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	})
	return httptest.NewServer(r)
}

func hit(t *testing.T, url, token string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestServiceToken_IssueAndAccess(t *testing.T) {
	svc, _ := setupAuth(t)
	ctx := context.Background()

	sc, secret, err := svc.CreateServiceClient(ctx)
	if err != nil {
		t.Fatalf("provisionar: %v", err)
	}
	if sc.Role != auth.RoleSettlement {
		t.Errorf("role = %s, quero SETTLEMENT", sc.Role)
	}

	token, expiresIn, err := svc.IssueServiceToken(ctx, sc.ClientID, secret)
	if err != nil {
		t.Fatalf("emitir token: %v", err)
	}
	if expiresIn <= 0 {
		t.Errorf("expires_in = %d, quero > 0", expiresIn)
	}

	srv := protectedServer(svc)
	defer srv.Close()

	if code := hit(t, srv.URL+"/protected", token); code != http.StatusOK {
		t.Errorf("SETTLEMENT recebeu %d, quero 200", code)
	}
}

func TestServiceToken_WrongSecret(t *testing.T) {
	svc, _ := setupAuth(t)
	ctx := context.Background()

	sc, _, err := svc.CreateServiceClient(ctx)
	if err != nil {
		t.Fatalf("provisionar: %v", err)
	}
	if _, _, err := svc.IssueServiceToken(ctx, sc.ClientID, "secret-errado"); err == nil {
		t.Fatal("esperava erro com secret inválido")
	}
	if _, _, err := svc.IssueServiceToken(ctx, "svc_inexistente", "x"); err == nil {
		t.Fatal("esperava erro com client_id inexistente")
	}
}

func TestSettlementRoute_RejectsCustomer(t *testing.T) {
	svc, _ := setupAuth(t)
	ctx := context.Background()

	if _, err := svc.Register(ctx, "Cliente", "c@bankcore.dev", "secret6", auth.RoleCustomer); err != nil {
		t.Fatalf("register: %v", err)
	}
	token, _, err := svc.Login(ctx, "c@bankcore.dev", "secret6")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	srv := protectedServer(svc)
	defer srv.Close()

	if code := hit(t, srv.URL+"/protected", token); code != http.StatusForbidden {
		t.Errorf("CUSTOMER recebeu %d, quero 403", code)
	}
	if code := hit(t, srv.URL+"/protected", ""); code != http.StatusUnauthorized {
		t.Errorf("sem token recebeu %d, quero 401", code)
	}
}
