package transfer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/GustavoQ-Mateus/bankcore/internal/platform/httpx"
	"github.com/GustavoQ-Mateus/bankcore/internal/transfer"
)

func TestGet_ReturnsTransfer(t *testing.T) {
	pool := setupPool(t)
	svc := transfer.NewService(pool)
	a, b := seedAccounts(t, pool, 10000)
	ctx := context.Background()

	created, err := svc.Transfer(ctx, a, b, 4000, "key-get")
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}

	got, err := svc.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("id = %s, quero %s", got.ID, created.ID)
	}
	if got.FromAccountID != a || got.ToAccountID != b {
		t.Errorf("participantes errados: from=%s to=%s", got.FromAccountID, got.ToAccountID)
	}
	if got.AmountCents != 4000 {
		t.Errorf("amount = %d, quero 4000", got.AmountCents)
	}
	if got.Status != transfer.StatusSettled {
		t.Errorf("status = %s, quero SETTLED", got.Status)
	}
}

func TestGet_NotFound(t *testing.T) {
	pool := setupPool(t)
	svc := transfer.NewService(pool)

	_, err := svc.Get(context.Background(), uuid.New())
	if err == nil {
		t.Fatal("esperava erro NotFound")
	}
	var apiErr *httpx.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "NOT_FOUND" {
		t.Errorf("erro = %v, quero NOT_FOUND", err)
	}
}

func TestListByStatus_FiltersAndOrders(t *testing.T) {
	pool := setupPool(t)
	svc := transfer.NewService(pool)
	a, b := seedAccounts(t, pool, 100000)
	ctx := context.Background()

	// 3 SETTLED
	for i, key := range []string{"s1", "s2", "s3"} {
		if _, err := svc.Transfer(ctx, a, b, int64(1000*(i+1)), key); err != nil {
			t.Fatalf("settled %s: %v", key, err)
		}
	}
	// 1 FAILED (saldo insuficiente na conta B, que tem crédito mas menos que o débito pedido)
	if _, err := svc.Transfer(ctx, b, a, 999999, "f1"); err == nil {
		t.Fatal("esperava falha de saldo insuficiente para gerar FAILED")
	}

	settled, err := svc.ListByStatus(ctx, transfer.StatusSettled, 50, 0)
	if err != nil {
		t.Fatalf("list settled: %v", err)
	}
	if len(settled) != 3 {
		t.Fatalf("settled = %d, quero 3", len(settled))
	}
	for _, tr := range settled {
		if tr.Status != transfer.StatusSettled {
			t.Errorf("status = %s no resultado SETTLED", tr.Status)
		}
	}
	// ordem ASC por created_at: s1 antes de s3
	if !settled[0].CreatedAt.Before(settled[2].CreatedAt) && !settled[0].CreatedAt.Equal(settled[2].CreatedAt) {
		t.Errorf("resultado não está em ordem ASC por created_at")
	}

	failed, err := svc.ListByStatus(ctx, transfer.StatusFailed, 50, 0)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(failed) != 1 {
		t.Fatalf("failed = %d, quero 1", len(failed))
	}

	pending, err := svc.ListByStatus(ctx, transfer.StatusPending, 50, 0)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("pending = %d, quero 0", len(pending))
	}
}

func TestListByStatus_Pagination(t *testing.T) {
	pool := setupPool(t)
	svc := transfer.NewService(pool)
	a, b := seedAccounts(t, pool, 100000)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := svc.Transfer(ctx, a, b, 1000, uuid.NewString()); err != nil {
			t.Fatalf("transfer %d: %v", i, err)
		}
	}

	page1, err := svc.ListByStatus(ctx, transfer.StatusSettled, 2, 0)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 = %d, quero 2", len(page1))
	}

	page3, err := svc.ListByStatus(ctx, transfer.StatusSettled, 2, 4)
	if err != nil {
		t.Fatalf("page3: %v", err)
	}
	if len(page3) != 1 {
		t.Fatalf("page3 = %d, quero 1 (5 total, offset 4)", len(page3))
	}
}
