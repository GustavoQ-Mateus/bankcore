package transfer_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/GustavoQ-Mateus/bankcore/internal/platform/httpx"
	"github.com/GustavoQ-Mateus/bankcore/internal/transfer"
)

func setupPool(t *testing.T) *pgxpool.Pool {
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

	schema, err := os.ReadFile("../../migrations/0001_init.up.sql")
	if err != nil {
		t.Fatalf("ler migration: %v", err)
	}
	if _, err := pool.Exec(ctx, string(schema)); err != nil {
		t.Fatalf("aplicar migration: %v", err)
	}

	return pool
}

func seedAccounts(t *testing.T, pool *pgxpool.Pool, balanceA int64) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	var ownerID uuid.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO customers (name, email, password_hash) VALUES ('Owner', 'owner@bankcore.dev', 'x') RETURNING id`,
	).Scan(&ownerID)
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}

	var a, b uuid.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO accounts (owner_id, number, balance_cents) VALUES ($1, '0000000001', $2) RETURNING id`,
		ownerID, balanceA,
	).Scan(&a); err != nil {
		t.Fatalf("seed account A: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO accounts (owner_id, number, balance_cents) VALUES ($1, '0000000002', 0) RETURNING id`,
		ownerID,
	).Scan(&b); err != nil {
		t.Fatalf("seed account B: %v", err)
	}
	return a, b
}

func balanceOf(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) int64 {
	t.Helper()
	var bal int64
	if err := pool.QueryRow(context.Background(), `SELECT balance_cents FROM accounts WHERE id = $1`, id).Scan(&bal); err != nil {
		t.Fatalf("ler saldo: %v", err)
	}
	return bal
}

func TestTransfer_Atomic(t *testing.T) {
	pool := setupPool(t)
	svc := transfer.NewService(pool)
	a, b := seedAccounts(t, pool, 10000)

	tr, err := svc.Transfer(context.Background(), a, b, 3000, "key-atomic")
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if tr.Status != transfer.StatusSettled {
		t.Fatalf("status = %s, quero SETTLED", tr.Status)
	}

	if got := balanceOf(t, pool, a); got != 7000 {
		t.Errorf("saldo A = %d, quero 7000", got)
	}
	if got := balanceOf(t, pool, b); got != 3000 {
		t.Errorf("saldo B = %d, quero 3000", got)
	}

	var entries int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM entries WHERE transfer_id = $1`, tr.ID).Scan(&entries); err != nil {
		t.Fatalf("contar entries: %v", err)
	}
	if entries != 2 {
		t.Errorf("entries = %d, quero 2", entries)
	}
}

func TestTransfer_InsufficientFunds(t *testing.T) {
	pool := setupPool(t)
	svc := transfer.NewService(pool)
	a, b := seedAccounts(t, pool, 1000)

	_, err := svc.Transfer(context.Background(), a, b, 5000, "key-insufficient")
	if err == nil {
		t.Fatal("esperava erro de saldo insuficiente")
	}

	if got := balanceOf(t, pool, a); got != 1000 {
		t.Errorf("saldo A = %d, quero 1000 (inalterado)", got)
	}
	if got := balanceOf(t, pool, b); got != 0 {
		t.Errorf("saldo B = %d, quero 0 (inalterado)", got)
	}
}

func TestTransfer_Idempotency(t *testing.T) {
	pool := setupPool(t)
	svc := transfer.NewService(pool)
	a, b := seedAccounts(t, pool, 10000)

	ctx := context.Background()
	first, err := svc.Transfer(ctx, a, b, 2500, "key-idem")
	if err != nil {
		t.Fatalf("primeira transfer: %v", err)
	}
	second, err := svc.Transfer(ctx, a, b, 2500, "key-idem")
	if err != nil {
		t.Fatalf("segunda transfer: %v", err)
	}

	if first.ID != second.ID {
		t.Errorf("ids diferentes: %s != %s", first.ID, second.ID)
	}
	if got := balanceOf(t, pool, a); got != 7500 {
		t.Errorf("saldo A = %d, quero 7500 (débito único)", got)
	}
	if got := balanceOf(t, pool, b); got != 2500 {
		t.Errorf("saldo B = %d, quero 2500 (crédito único)", got)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM entries`).Scan(&count); err != nil {
		t.Fatalf("contar entries: %v", err)
	}
	if count != 2 {
		t.Errorf("entries = %d, quero 2 (idempotente)", count)
	}
}

func TestTransfer_Concurrent(t *testing.T) {
	pool := setupPool(t)
	svc := transfer.NewService(pool)
	a, b := seedAccounts(t, pool, 10000)

	const n = 20
	const amount = 1000

	var wg sync.WaitGroup
	var mu sync.Mutex
	settled := 0

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := uuid.NewString()
			if transferWithClientRetry(svc, a, b, amount, key) {
				mu.Lock()
				settled++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	balA := balanceOf(t, pool, a)
	balB := balanceOf(t, pool, b)

	if balA < 0 {
		t.Errorf("saldo A ficou negativo: %d", balA)
	}
	if balA+balB != 10000 {
		t.Errorf("conservação violada: A=%d B=%d soma=%d, quero 10000", balA, balB, balA+balB)
	}
	if settled != 10 {
		t.Errorf("settled = %d, quero 10 (100.00 / 10.00)", settled)
	}
	if balB != int64(settled)*amount {
		t.Errorf("saldo B = %d, quero %d", balB, int64(settled)*amount)
	}
}

func transferWithClientRetry(svc *transfer.Service, from, to uuid.UUID, amount int64, key string) bool {
	for attempt := 0; attempt < 50; attempt++ {
		_, err := svc.Transfer(context.Background(), from, to, amount, key)
		if err == nil {
			return true
		}
		var apiErr *httpx.APIError
		if errors.As(err, &apiErr) && apiErr.Code == "CONFLICT" {
			continue
		}
		return false
	}
	return false
}
