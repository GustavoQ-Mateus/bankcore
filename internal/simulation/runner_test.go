package simulation_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/GustavoQ-Mateus/bankcore/internal/account"
	"github.com/GustavoQ-Mateus/bankcore/internal/auth"
	"github.com/GustavoQ-Mateus/bankcore/internal/platform/httpx"
	"github.com/GustavoQ-Mateus/bankcore/internal/simulation"
	"github.com/GustavoQ-Mateus/bankcore/internal/transfer"
)

const finishTimeout = 60 * time.Second

type fixture struct {
	pool      *pgxpool.Pool
	transfers *transfer.Service
	runner    *simulation.Runner
	reporter  *simulation.Reporter
}

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

	for _, m := range []string{"0001_init.up.sql", "0002_settlement.up.sql", "0003_entries_transfer_index.up.sql"} {
		schema, err := os.ReadFile("../../migrations/" + m)
		if err != nil {
			t.Fatalf("ler migration %s: %v", m, err)
		}
		if _, err := pool.Exec(ctx, string(schema)); err != nil {
			t.Fatalf("aplicar migration %s: %v", m, err)
		}
	}
	return pool
}

func setupFixture(t *testing.T) fixture {
	t.Helper()
	pool := setupPool(t)
	transfers := transfer.NewService(pool, transfer.WithExternalSettlement())
	runner := simulation.NewRunner(pool, account.NewService(pool), transfers)
	t.Cleanup(func() { runner.Stop() })
	return fixture{
		pool:      pool,
		transfers: transfers,
		runner:    runner,
		reporter:  simulation.NewReporter(pool, transfers, runner),
	}
}

func waitForPhase(t *testing.T, runner *simulation.Runner, phase simulation.Phase) simulation.Status {
	t.Helper()
	deadline := time.Now().Add(finishTimeout)
	for time.Now().Before(deadline) {
		status := runner.Status()
		if status.Phase == phase {
			return status
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("simulação não chegou a %s; status = %+v", phase, runner.Status())
	return simulation.Status{}
}

func settleAll(t *testing.T, svc *transfer.Service) {
	t.Helper()
	ctx := context.Background()
	for {
		pending, _, err := svc.ListForSettlement(ctx, transfer.StatusPending, 50, 0)
		if err != nil {
			t.Fatalf("listar backlog: %v", err)
		}
		if len(pending) == 0 {
			return
		}
		for _, tr := range pending {
			if _, err := svc.Settle(ctx, tr.ID, "test"); err != nil {
				t.Fatalf("settle %s: %v", tr.ID, err)
			}
		}
	}
}

func stats(t *testing.T, reporter *simulation.Reporter) simulation.Stats {
	t.Helper()
	s, err := reporter.Stats(context.Background())
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	return s
}

func contendedConfig() simulation.Config {
	return simulation.Config{Accounts: 4, Transfers: 300, Rate: 500, Workers: 16}
}

func TestRunner_FinishedCountersAddUpToTransfers(t *testing.T) {
	f := setupFixture(t)
	cfg := contendedConfig()

	if _, err := f.runner.Start(cfg); err != nil {
		t.Fatalf("start: %v", err)
	}
	status := waitForPhase(t, f.runner, simulation.PhaseFinished)

	c := status.Counters
	if c.Dispatched != int64(cfg.Transfers) {
		t.Errorf("dispatched = %d, quero %d", c.Dispatched, cfg.Transfers)
	}
	if sum := c.Accepted + c.RejectedInsufficient + c.ConflictExhausted + c.Errors; sum != int64(cfg.Transfers) {
		t.Errorf("soma dos resultados = %d, quero %d (%+v)", sum, cfg.Transfers, c)
	}
	if c.Errors != 0 {
		t.Errorf("errors = %d, quero 0", c.Errors)
	}
	if status.FinishedAt == nil || status.RunID == nil {
		t.Errorf("finished_at/run_id ausentes: %+v", status)
	}
}

func TestRunner_ContendedRunKeepsLedgerConsistentAndCountsConflicts(t *testing.T) {
	f := setupFixture(t)

	if _, err := f.runner.Start(contendedConfig()); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForPhase(t, f.runner, simulation.PhaseFinished)

	s := stats(t, f.reporter)
	if s.Ledger.DivergentAccounts != 0 {
		t.Errorf("divergent_accounts = %d, quero 0", s.Ledger.DivergentAccounts)
	}
	if s.VersionConflicts == 0 {
		t.Error("version_conflicts = 0, quero > 0 com poucas contas e muitos workers")
	}
}

func TestRunner_SettledVolumeMatchesRunDebits(t *testing.T) {
	f := setupFixture(t)

	status, err := f.runner.Start(contendedConfig())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForPhase(t, f.runner, simulation.PhaseFinished)
	settleAll(t, f.transfers)

	var debits int64
	if err := f.pool.QueryRow(context.Background(),
		`SELECT coalesce(sum(e.amount_cents), 0)::bigint FROM entries e
		 JOIN transfers t ON t.id = e.transfer_id
		 WHERE e.type = 'DEBIT' AND t.idempotency_key LIKE $1`,
		"sim-"+status.RunID.String()+"-%",
	).Scan(&debits); err != nil {
		t.Fatalf("somar débitos: %v", err)
	}

	s := stats(t, f.reporter)
	if s.VolumeCents.Settled != debits {
		t.Errorf("volume_cents.settled = %d, quero %d (soma dos DEBIT da execução)", s.VolumeCents.Settled, debits)
	}
	if s.Transfers.Pending != 0 || s.VolumeCents.Pending != 0 {
		t.Errorf("pending = %d / %d, quero 0 depois de liquidar tudo", s.Transfers.Pending, s.VolumeCents.Pending)
	}
	if s.Ledger.DivergentAccounts != 0 {
		t.Errorf("divergent_accounts = %d, quero 0", s.Ledger.DivergentAccounts)
	}
}

func TestRunner_StartWhileRunningReturnsSimulationRunning(t *testing.T) {
	f := setupFixture(t)
	long := simulation.Config{Accounts: 2, Transfers: 100000, Rate: 1, Workers: 1}

	first, err := f.runner.Start(long)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_, err = f.runner.Start(contendedConfig())
	var apiErr *httpx.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict || apiErr.Code != "SIMULATION_RUNNING" {
		t.Fatalf("erro = %v, quero 409 SIMULATION_RUNNING", err)
	}

	current := f.runner.Status()
	if *current.RunID != *first.RunID || current.Config != long {
		t.Errorf("execução original alterada: %+v", current)
	}
}

func TestRunner_StopLeavesNoUnbookedPending(t *testing.T) {
	f := setupFixture(t)
	cfg := simulation.Config{Accounts: 4, Transfers: 100000, Rate: 200, Workers: 16}

	started, err := f.runner.Start(cfg)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForPhase(t, f.runner, simulation.PhaseRunning)
	time.Sleep(500 * time.Millisecond)

	stopped := f.runner.Stop()
	if stopped.Phase != simulation.PhaseStopped {
		t.Fatalf("phase = %s, quero stopped", stopped.Phase)
	}
	if stopped.FinishedAt == nil {
		t.Error("finished_at ausente em stopped")
	}
	if stopped.Counters.Dispatched == 0 || stopped.Counters.Dispatched >= int64(cfg.Transfers) {
		t.Errorf("dispatched = %d, quero parcial", stopped.Counters.Dispatched)
	}

	var unbooked, created int64
	if err := f.pool.QueryRow(context.Background(),
		`SELECT
		     count(*) FILTER (WHERE t.status = 'PENDING' AND NOT EXISTS (SELECT 1 FROM entries e WHERE e.transfer_id = t.id)),
		     count(*)
		 FROM transfers t WHERE t.idempotency_key LIKE $1`,
		"sim-"+started.RunID.String()+"-%",
	).Scan(&unbooked, &created); err != nil {
		t.Fatalf("contar: %v", err)
	}
	if unbooked != stopped.Counters.ConflictExhausted {
		t.Errorf("pendentes sem lançamento = %d, quero %d (só órfãs de conflito esgotado)", unbooked, stopped.Counters.ConflictExhausted)
	}
	if created != stopped.Counters.Dispatched {
		t.Errorf("transferências criadas = %d, quero %d (nenhuma depois do stop)", created, stopped.Counters.Dispatched)
	}
}

func TestStartHandler_EmptyBodyUsesDefaults(t *testing.T) {
	f := setupFixture(t)
	router := simulationRouter(simulation.NewHandler(f.runner, f.reporter, dashboardOrigin))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/simulation/start", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, quero 202 (%s)", rec.Code, rec.Body.String())
	}

	status := f.runner.Status()
	if status.Config != simulation.DefaultConfig() {
		t.Errorf("config = %+v, quero defaults", status.Config)
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/simulation/start", nil))
	if rec.Code != http.StatusConflict || errorCode(t, rec) != "SIMULATION_RUNNING" {
		t.Errorf("segundo start = %d %s, quero 409 SIMULATION_RUNNING", rec.Code, rec.Body.String())
	}
}

func TestSimulationCustomer_CannotLogin(t *testing.T) {
	f := setupFixture(t)

	if _, err := f.runner.Start(simulation.Config{Accounts: 2, Transfers: 1, Rate: 1, Workers: 1}); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForPhase(t, f.runner, simulation.PhaseFinished)

	authSvc := auth.NewService(f.pool, "test-secret", time.Hour, 15*time.Minute, 4, false)
	for _, password := range []string{"", "!", "qualquer-senha"} {
		_, _, err := authSvc.Login(context.Background(), "simulacao@bankcore.local", password)
		if !errors.Is(err, httpx.ErrUnauthorized) {
			t.Errorf("login com %q: erro = %v, quero 401", password, err)
		}
	}
}

func TestRunner_ReusesCustomerAcrossRuns(t *testing.T) {
	f := setupFixture(t)
	cfg := simulation.Config{Accounts: 2, Transfers: 1, Rate: 1, Workers: 1}

	for i := 0; i < 2; i++ {
		if _, err := f.runner.Start(cfg); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
		waitForPhase(t, f.runner, simulation.PhaseFinished)
	}

	var customers, accounts int
	if err := f.pool.QueryRow(context.Background(),
		`SELECT (SELECT count(*) FROM customers), (SELECT count(*) FROM accounts)`,
	).Scan(&customers, &accounts); err != nil {
		t.Fatalf("contar: %v", err)
	}
	if customers != 1 || accounts != 4 {
		t.Errorf("customers=%d accounts=%d, quero 1 e 4", customers, accounts)
	}
}

func TestStats_KnownDataAndDivergenceDetection(t *testing.T) {
	f := setupFixture(t)
	ctx := context.Background()

	var owner uuid.UUID
	if err := f.pool.QueryRow(ctx,
		`INSERT INTO customers (name, email, password_hash) VALUES ('Owner', 'owner@bankcore.dev', 'x') RETURNING id`,
	).Scan(&owner); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	accounts := account.NewService(f.pool)
	var ids []uuid.UUID
	for i := 0; i < 3; i++ {
		acc, err := accounts.Open(ctx, owner)
		if err != nil {
			t.Fatalf("abrir conta: %v", err)
		}
		if _, err := accounts.Deposit(ctx, acc.ID, 100000); err != nil {
			t.Fatalf("depósito: %v", err)
		}
		ids = append(ids, acc.ID)
	}

	settled, err := f.transfers.Transfer(ctx, ids[0], ids[1], 1000, "k-settled")
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if _, err := f.transfers.Settle(ctx, settled.ID, ""); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if _, err := f.transfers.Transfer(ctx, ids[1], ids[2], 2500, "k-pending"); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	if _, err := f.transfers.Transfer(ctx, ids[2], ids[0], 999999, "k-failed"); err == nil {
		t.Fatal("esperava saldo insuficiente")
	}
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO transfers (from_account_id, to_account_id, amount_cents, idempotency_key, status)
		 VALUES ($1, $2, 7000, 'k-orphan', 'PENDING')`, ids[0], ids[2],
	); err != nil {
		t.Fatalf("inserir órfã: %v", err)
	}
	if _, err := f.pool.Exec(ctx,
		`UPDATE transfers SET
		     created_at = CASE WHEN idempotency_key = 'k-failed' THEN now() - interval '10 seconds' ELSE now() - interval '300 milliseconds' END,
		     settled_at = CASE WHEN settled_at IS NULL THEN NULL ELSE now() - interval '100 milliseconds' END`,
	); err != nil {
		t.Fatalf("fixar horários: %v", err)
	}

	s := stats(t, f.reporter)
	if s.Transfers != (simulation.TransferCounts{Pending: 1, Settled: 1, Failed: 1}) {
		t.Errorf("transfers = %+v, quero 1/1/1 (órfã fora)", s.Transfers)
	}
	if s.VolumeCents != (simulation.Volume{Pending: 2500, Settled: 1000}) {
		t.Errorf("volume_cents = %+v, quero pending 2500 settled 1000", s.VolumeCents)
	}
	if s.Throughput != (simulation.Throughput{CreatedPerSec: 3, SettledPerSec: 1}) {
		t.Errorf("throughput = %+v, quero 3 criadas e 1 liquidada na janela de 1s", s.Throughput)
	}
	if s.SettlementLatencyMS != (simulation.Latency{P50: 200, P95: 200}) {
		t.Errorf("latência = %+v, quero p50 e p95 de 200ms", s.SettlementLatencyMS)
	}
	if s.Ledger != (simulation.Ledger{Accounts: 3, DivergentAccounts: 0}) {
		t.Errorf("ledger = %+v, quero 3 contas e 0 divergentes", s.Ledger)
	}
	if s.Simulation.Phase != simulation.PhaseIdle {
		t.Errorf("simulation.phase = %s, quero idle", s.Simulation.Phase)
	}

	if _, err := f.pool.Exec(ctx, `UPDATE accounts SET balance_cents = balance_cents + 1 WHERE id = $1`, ids[0]); err != nil {
		t.Fatalf("forçar divergência: %v", err)
	}
	if got := stats(t, f.reporter).Ledger.DivergentAccounts; got != 1 {
		t.Errorf("divergent_accounts = %d após divergência forçada, quero 1", got)
	}
}

func TestStats_EmptyDatabaseReturnsZeros(t *testing.T) {
	f := setupFixture(t)

	s := stats(t, f.reporter)
	if s.Transfers != (simulation.TransferCounts{}) || s.VolumeCents != (simulation.Volume{}) ||
		s.Throughput != (simulation.Throughput{}) || s.SettlementLatencyMS != (simulation.Latency{}) ||
		s.Ledger != (simulation.Ledger{}) {
		t.Errorf("stats = %+v, quero zeros", s)
	}
}
