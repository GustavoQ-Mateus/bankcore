package transfer_test

import (
	"context"
	"testing"

	"github.com/GustavoQ-Mateus/bankcore/internal/transfer"
)

func TestExternal_PostLeavesPending(t *testing.T) {
	pool := setupPool(t)
	svc := transfer.NewService(pool, transfer.WithExternalSettlement())
	a, b := seedAccounts(t, pool, 10000)
	ctx := context.Background()

	tr, err := svc.Transfer(ctx, a, b, 3000, "ext-1")
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if tr.Status != transfer.StatusPending {
		t.Fatalf("status = %s, quero PENDING", tr.Status)
	}
	if got := balanceOf(t, pool, a); got != 7000 {
		t.Errorf("saldo A = %d, quero 7000 (dinheiro moveu no POST)", got)
	}
	if got := balanceOf(t, pool, b); got != 3000 {
		t.Errorf("saldo B = %d, quero 3000", got)
	}

	pend, err := svc.ListByStatus(ctx, transfer.StatusPending, 50, 0)
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pend) != 1 {
		t.Fatalf("pending = %d, quero 1", len(pend))
	}
}

func TestExternal_RetryNoDoubleDebit(t *testing.T) {
	pool := setupPool(t)
	svc := transfer.NewService(pool, transfer.WithExternalSettlement())
	a, b := seedAccounts(t, pool, 10000)
	ctx := context.Background()

	first, err := svc.Transfer(ctx, a, b, 3000, "ext-retry")
	if err != nil {
		t.Fatalf("primeira: %v", err)
	}
	second, err := svc.Transfer(ctx, a, b, 3000, "ext-retry")
	if err != nil {
		t.Fatalf("segunda: %v", err)
	}

	if first.ID != second.ID {
		t.Errorf("ids diferentes: %s != %s", first.ID, second.ID)
	}
	if got := balanceOf(t, pool, a); got != 7000 {
		t.Errorf("saldo A = %d, quero 7000 (débito único mesmo em PENDING)", got)
	}

	var entries int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM entries`).Scan(&entries); err != nil {
		t.Fatalf("contar entries: %v", err)
	}
	if entries != 2 {
		t.Errorf("entries = %d, quero 2 (idempotente)", entries)
	}
}

func TestExternal_Settle(t *testing.T) {
	pool := setupPool(t)
	svc := transfer.NewService(pool, transfer.WithExternalSettlement())
	a, b := seedAccounts(t, pool, 10000)
	ctx := context.Background()

	tr, err := svc.Transfer(ctx, a, b, 3000, "ext-settle")
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}

	settled, err := svc.Settle(ctx, tr.ID, "liq-ref-1")
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	if settled.Status != transfer.StatusSettled {
		t.Fatalf("status = %s, quero SETTLED", settled.Status)
	}
	if settled.SettledAt == nil {
		t.Error("settled_at não preenchido")
	}
	if settled.SettlementRef == nil || *settled.SettlementRef != "liq-ref-1" {
		t.Errorf("settlement_ref = %v, quero liq-ref-1", settled.SettlementRef)
	}

	again, err := svc.Settle(ctx, tr.ID, "outra-ref")
	if err != nil {
		t.Fatalf("settle repetido: %v", err)
	}
	if again.Status != transfer.StatusSettled {
		t.Errorf("status = %s no settle repetido", again.Status)
	}
	if again.SettlementRef == nil || *again.SettlementRef != "liq-ref-1" {
		t.Errorf("settlement_ref sobrescrito: %v (idempotente deve manter liq-ref-1)", again.SettlementRef)
	}
}

func TestExternal_FailReverses(t *testing.T) {
	pool := setupPool(t)
	svc := transfer.NewService(pool, transfer.WithExternalSettlement())
	a, b := seedAccounts(t, pool, 10000)
	ctx := context.Background()

	tr, err := svc.Transfer(ctx, a, b, 3000, "ext-fail")
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}

	failed, err := svc.Fail(ctx, tr.ID)
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if failed.Status != transfer.StatusFailed {
		t.Fatalf("status = %s, quero FAILED", failed.Status)
	}
	if got := balanceOf(t, pool, a); got != 10000 {
		t.Errorf("saldo A = %d, quero 10000 (estornado)", got)
	}
	if got := balanceOf(t, pool, b); got != 0 {
		t.Errorf("saldo B = %d, quero 0 (estornado)", got)
	}

	var entries int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM entries WHERE transfer_id = $1`, tr.ID).Scan(&entries); err != nil {
		t.Fatalf("contar entries: %v", err)
	}
	if entries != 4 {
		t.Errorf("entries = %d, quero 4 (2 originais + 2 estorno, append-only)", entries)
	}

	again, err := svc.Fail(ctx, tr.ID)
	if err != nil {
		t.Fatalf("fail repetido: %v", err)
	}
	if again.Status != transfer.StatusFailed {
		t.Errorf("status = %s no fail repetido", again.Status)
	}
	if got := balanceOf(t, pool, a); got != 10000 {
		t.Errorf("saldo A = %d após fail repetido, quero 10000 (sem duplo estorno)", got)
	}
}

func TestExternal_SettleThenFailConflict(t *testing.T) {
	pool := setupPool(t)
	svc := transfer.NewService(pool, transfer.WithExternalSettlement())
	a, b := seedAccounts(t, pool, 10000)
	ctx := context.Background()

	tr, err := svc.Transfer(ctx, a, b, 3000, "ext-conflict")
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if _, err := svc.Settle(ctx, tr.ID, ""); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if _, err := svc.Fail(ctx, tr.ID); err == nil {
		t.Fatal("esperava conflito ao falhar transferência já liquidada")
	}
}
