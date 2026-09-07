package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/GustavoQ-Mateus/bankcore/internal/auth"
	"github.com/GustavoQ-Mateus/bankcore/internal/platform/httpx"
)

func TestRegister_GateClosed_RejectsAdmin(t *testing.T) {
	svc, pool := setupAuthWithGate(t, false)
	ctx := context.Background()

	_, err := svc.Register(ctx, "Fulano", "a@bankcore.dev", "secret6", auth.RoleAdmin)
	if err == nil {
		t.Fatal("esperava recusa com gate fechado")
	}
	var apiErr *httpx.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "ADMIN_REGISTER_DISABLED" || apiErr.Status != 403 {
		t.Fatalf("erro = %v, quero 403 ADMIN_REGISTER_DISABLED", err)
	}

	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM customers WHERE email = $1`, "a@bankcore.dev").Scan(&n); err != nil {
		t.Fatalf("contar: %v", err)
	}
	if n != 0 {
		t.Errorf("linhas criadas = %d, quero 0", n)
	}
}

func TestRegister_GateClosed_AllowsCustomer(t *testing.T) {
	svc, _ := setupAuthWithGate(t, false)
	ctx := context.Background()

	c, err := svc.Register(ctx, "Cliente", "c@bankcore.dev", "secret6", auth.RoleCustomer)
	if err != nil {
		t.Fatalf("register CUSTOMER: %v", err)
	}
	if c.Role != auth.RoleCustomer {
		t.Errorf("role = %s, quero CUSTOMER", c.Role)
	}

	empty, err := svc.Register(ctx, "SemRole", "d@bankcore.dev", "secret6", "")
	if err != nil {
		t.Fatalf("register sem role: %v", err)
	}
	if empty.Role != auth.RoleCustomer {
		t.Errorf("role default = %s, quero CUSTOMER", empty.Role)
	}

	if _, _, err := svc.Login(ctx, "c@bankcore.dev", "secret6"); err != nil {
		t.Errorf("login do CUSTOMER falhou: %v", err)
	}
}

func TestRegister_GateOpen_AllowsAdmin(t *testing.T) {
	svc, _ := setupAuthWithGate(t, true)
	ctx := context.Background()

	c, err := svc.Register(ctx, "Chefe", "admin@bankcore.dev", "secret6", auth.RoleAdmin)
	if err != nil {
		t.Fatalf("register ADMIN com gate aberto: %v", err)
	}
	if c.Role != auth.RoleAdmin {
		t.Errorf("role = %s, quero ADMIN", c.Role)
	}
}

func TestEnsureAdmin_Idempotent(t *testing.T) {
	svc, pool := setupAuthWithGate(t, false)
	ctx := context.Background()

	created, err := svc.EnsureAdmin(ctx, "Administrador", "seed@bankcore.dev", "secret6")
	if err != nil {
		t.Fatalf("primeiro seed: %v", err)
	}
	if !created {
		t.Fatal("primeiro seed deveria criar (created=true)")
	}

	again, err := svc.EnsureAdmin(ctx, "Administrador", "seed@bankcore.dev", "outrasenha")
	if err != nil {
		t.Fatalf("segundo seed: %v", err)
	}
	if again {
		t.Error("segundo seed não deveria criar (created=false)")
	}

	var n int
	var role auth.Role
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM customers WHERE email = $1`, "seed@bankcore.dev").Scan(&n); err != nil {
		t.Fatalf("contar: %v", err)
	}
	if n != 1 {
		t.Errorf("linhas = %d, quero 1", n)
	}
	if err := pool.QueryRow(ctx,
		`SELECT role FROM customers WHERE email = $1`, "seed@bankcore.dev").Scan(&role); err != nil {
		t.Fatalf("ler role: %v", err)
	}
	if role != auth.RoleAdmin {
		t.Errorf("role = %s, quero ADMIN", role)
	}

	if _, _, err := svc.Login(ctx, "seed@bankcore.dev", "secret6"); err != nil {
		t.Errorf("login do admin semeado falhou: %v", err)
	}
}
