package simulation_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/GustavoQ-Mateus/bankcore/internal/auth"
	"github.com/GustavoQ-Mateus/bankcore/internal/platform/httpx"
	"github.com/GustavoQ-Mateus/bankcore/internal/simulation"
	"github.com/GustavoQ-Mateus/bankcore/internal/transfer"
)

func smallConfig() simulation.Config {
	return simulation.Config{Accounts: 4, Transfers: 50, Rate: 500, Workers: 8}
}

func runToFinished(t *testing.T, f fixture, cfg simulation.Config) simulation.Status {
	t.Helper()
	if _, err := f.runner.Start(cfg); err != nil {
		t.Fatalf("start: %v", err)
	}
	return waitForPhase(t, f.runner, simulation.PhaseFinished)
}

func tableCounts(t *testing.T, f fixture) (entries, transfers, accounts int) {
	t.Helper()
	if err := f.pool.QueryRow(context.Background(),
		`SELECT (SELECT count(*) FROM entries), (SELECT count(*) FROM transfers), (SELECT count(*) FROM accounts)`,
	).Scan(&entries, &transfers, &accounts); err != nil {
		t.Fatalf("contar tabelas: %v", err)
	}
	return entries, transfers, accounts
}

func credentialCounts(t *testing.T, f fixture) (customers, serviceClients int) {
	t.Helper()
	if err := f.pool.QueryRow(context.Background(),
		`SELECT (SELECT count(*) FROM customers), (SELECT count(*) FROM service_clients)`,
	).Scan(&customers, &serviceClients); err != nil {
		t.Fatalf("contar credenciais: %v", err)
	}
	return customers, serviceClients
}

func assertIdleStatus(t *testing.T, status simulation.Status) {
	t.Helper()
	if status.Phase != simulation.PhaseIdle || status.RunID != nil || status.StartedAt != nil || status.FinishedAt != nil {
		t.Errorf("status = %+v, quero idle com campos de execução nulos", status)
	}
	if status.Config != simulation.DefaultConfig() || status.Counters != (simulation.Counters{}) {
		t.Errorf("config/contadores = %+v / %+v, quero defaults e zeros", status.Config, status.Counters)
	}
}

func TestReset_WhileRunningReturnsSimulationRunningAndChangesNothing(t *testing.T) {
	f := setupFixture(t)
	cfg := simulation.Config{Accounts: 4, Transfers: 40, Rate: 10, Workers: 4}

	started, err := f.runner.Start(cfg)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForPhase(t, f.runner, simulation.PhaseRunning)
	time.Sleep(300 * time.Millisecond)
	_, _, accountsBefore := tableCounts(t, f)

	_, err = f.runner.Reset(context.Background())
	var apiErr *httpx.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusConflict || apiErr.Code != "SIMULATION_RUNNING" {
		t.Fatalf("erro = %v, quero 409 SIMULATION_RUNNING", err)
	}
	if _, _, accounts := tableCounts(t, f); accounts == 0 || accounts != accountsBefore {
		t.Errorf("contas = %d depois do reset recusado, quero %d", accounts, accountsBefore)
	}

	finished := waitForPhase(t, f.runner, simulation.PhaseFinished)
	if *finished.RunID != *started.RunID || finished.Counters.Dispatched != int64(cfg.Transfers) {
		t.Errorf("execução afetada pelo reset recusado: %+v", finished)
	}
}

func TestReset_FromIdleFinishedAndStoppedReturnsIdleStatus(t *testing.T) {
	f := setupFixture(t)
	ctx := context.Background()

	status, err := f.runner.Reset(ctx)
	if err != nil {
		t.Fatalf("reset em idle: %v", err)
	}
	assertIdleStatus(t, status)

	runToFinished(t, f, smallConfig())
	status, err = f.runner.Reset(ctx)
	if err != nil {
		t.Fatalf("reset em finished: %v", err)
	}
	assertIdleStatus(t, status)

	if _, err := f.runner.Start(simulation.Config{Accounts: 4, Transfers: 100000, Rate: 50, Workers: 4}); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForPhase(t, f.runner, simulation.PhaseRunning)
	if phase := f.runner.Stop().Phase; phase != simulation.PhaseStopped {
		t.Fatalf("phase = %s depois do stop, quero stopped", phase)
	}
	status, err = f.runner.Reset(ctx)
	if err != nil {
		t.Fatalf("reset em stopped: %v", err)
	}
	assertIdleStatus(t, status)
}

func TestReset_ClearsTablesAndStats(t *testing.T) {
	f := setupFixture(t)
	runToFinished(t, f, contendedConfig())

	if _, err := f.runner.Reset(context.Background()); err != nil {
		t.Fatalf("reset: %v", err)
	}

	if entries, transfers, accounts := tableCounts(t, f); entries != 0 || transfers != 0 || accounts != 0 {
		t.Errorf("entries=%d transfers=%d accounts=%d, quero tudo vazio", entries, transfers, accounts)
	}
	s := stats(t, f.reporter)
	if s.Transfers != (simulation.TransferCounts{}) || s.VolumeCents != (simulation.Volume{}) ||
		s.Throughput != (simulation.Throughput{}) || s.SettlementLatencyMS != (simulation.Latency{}) ||
		s.Ledger != (simulation.Ledger{}) || s.VersionConflicts != 0 {
		t.Errorf("stats = %+v, quero tudo zero", s)
	}
	assertIdleStatus(t, s.Simulation)
}

func TestReset_KeepsCustomersAndCredentials(t *testing.T) {
	f := setupFixture(t)
	ctx := context.Background()
	authSvc := auth.NewService(f.pool, "test-secret", time.Hour, 15*time.Minute, 4, false)
	if _, err := authSvc.EnsureAdmin(ctx, "Administrador", "admin@bankcore.dev", "admin-secret"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if _, err := authSvc.EnsureServiceClient(ctx, "svc_liquida_dev", "dev-liquida-secret"); err != nil {
		t.Fatalf("seed credencial: %v", err)
	}
	runToFinished(t, f, smallConfig())
	customersBefore, clientsBefore := credentialCounts(t, f)

	if _, err := f.runner.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}

	customers, clients := credentialCounts(t, f)
	if customers != customersBefore || clients != clientsBefore || customers != 2 || clients != 1 {
		t.Errorf("customers=%d service_clients=%d, quero %d e %d", customers, clients, customersBefore, clientsBefore)
	}
	if _, _, err := authSvc.Login(ctx, "admin@bankcore.dev", "admin-secret"); err != nil {
		t.Errorf("login do admin depois do reset: %v", err)
	}
	if _, _, err := authSvc.IssueServiceToken(ctx, "svc_liquida_dev", "dev-liquida-secret"); err != nil {
		t.Errorf("token da credencial depois do reset: %v", err)
	}
}

func TestReset_ThenStartRunsNormally(t *testing.T) {
	f := setupFixture(t)
	runToFinished(t, f, smallConfig())
	if _, err := f.runner.Reset(context.Background()); err != nil {
		t.Fatalf("reset: %v", err)
	}

	cfg := contendedConfig()
	c := runToFinished(t, f, cfg).Counters
	if sum := c.Accepted + c.RejectedInsufficient + c.ConflictExhausted + c.Errors; sum != int64(cfg.Transfers) || c.Dispatched != int64(cfg.Transfers) {
		t.Errorf("contadores = %+v, quero soma e dispatched = %d", c, cfg.Transfers)
	}
	s := stats(t, f.reporter)
	if s.Ledger.DivergentAccounts != 0 || s.Ledger.Accounts != int64(cfg.Accounts) {
		t.Errorf("ledger = %+v, quero %d contas e 0 divergentes", s.Ledger, cfg.Accounts)
	}
}

func TestReset_SettlementOnDeletedTransferReturnsNotFound(t *testing.T) {
	f := setupFixture(t)
	ctx := context.Background()
	runToFinished(t, f, smallConfig())

	pending, _, err := f.transfers.ListForSettlement(ctx, transfer.StatusPending, 50, 0)
	if err != nil || len(pending) < 2 {
		t.Fatalf("backlog antes do reset: %d itens, erro %v", len(pending), err)
	}

	if _, err := f.runner.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}

	if _, err := f.transfers.Settle(ctx, pending[0].ID, "ref"); !errors.Is(err, httpx.ErrNotFound) {
		t.Errorf("settle de transferência apagada: erro = %v, quero 404 NOT_FOUND", err)
	}
	if _, err := f.transfers.Fail(ctx, pending[1].ID); !errors.Is(err, httpx.ErrNotFound) {
		t.Errorf("fail de transferência apagada: erro = %v, quero 404 NOT_FOUND", err)
	}
	backlog, _, err := f.transfers.ListForSettlement(ctx, transfer.StatusPending, 50, 0)
	if err != nil || len(backlog) != 0 {
		t.Errorf("backlog depois do reset: %d itens, erro %v, quero vazio", len(backlog), err)
	}
}

func TestResetHandler_ReturnsIdleStatusBody(t *testing.T) {
	f := setupFixture(t)
	runToFinished(t, f, smallConfig())
	router := simulationRouter(simulation.NewHandler(f.runner, f.reporter, dashboardOrigin))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/simulation/reset", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200 (%s)", rec.Code, rec.Body.String())
	}
	want := `{"phase":"idle","run_id":null,"started_at":null,"finished_at":null,` +
		`"config":{"accounts":40,"transfers":1500,"rate":60,"workers":16},` +
		`"counters":{"dispatched":0,"accepted":0,"rejected_insufficient":0,"conflict_exhausted":0,"errors":0}}` + "\n"
	if rec.Body.String() != want {
		t.Errorf("corpo = %s, quero %s", rec.Body.String(), want)
	}
}

func waitForBlockedLock(t *testing.T, f fixture) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_locks WHERE NOT granted`).Scan(&waiting); err != nil {
			t.Fatalf("consultar pg_locks: %v", err)
		}
		if waiting > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("o reset não chegou a esperar lock")
}

func TestReset_WaitsForSettleLockOrderWithoutDeadlock(t *testing.T) {
	f := setupFixture(t)
	ctx := context.Background()
	runToFinished(t, f, smallConfig())
	pending, _, err := f.transfers.ListForSettlement(ctx, transfer.StatusPending, 1, 0)
	if err != nil || len(pending) != 1 {
		t.Fatalf("backlog: %d itens, erro %v", len(pending), err)
	}

	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT status FROM transfers WHERE id = $1 FOR UPDATE`, pending[0].ID); err != nil {
		t.Fatalf("for update: %v", err)
	}

	resetErr := make(chan error, 1)
	go func() {
		_, err := f.runner.Reset(ctx)
		resetErr <- err
	}()
	waitForBlockedLock(t, f)

	var booked bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM entries WHERE transfer_id = $1)`, pending[0].ID).Scan(&booked); err != nil {
		t.Fatalf("leitura de entries na transação do settle: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if err := <-resetErr; err != nil {
		t.Fatalf("reset: %v", err)
	}
	if entries, transfers, accounts := tableCounts(t, f); entries != 0 || transfers != 0 || accounts != 0 {
		t.Errorf("entries=%d transfers=%d accounts=%d, quero tudo vazio", entries, transfers, accounts)
	}
}

func TestReset_ConcurrentSettlesEndSettledOrNotFound(t *testing.T) {
	f := setupFixture(t)
	ctx := context.Background()
	runToFinished(t, f, simulation.Config{Accounts: 4, Transfers: 60, Rate: 500, Workers: 8})
	pending, _, err := f.transfers.ListForSettlement(ctx, transfer.StatusPending, 50, 0)
	if err != nil || len(pending) == 0 {
		t.Fatalf("backlog: %d itens, erro %v", len(pending), err)
	}

	start := make(chan struct{})
	settleErrs := make(chan error, len(pending))
	for _, tr := range pending {
		go func(id uuid.UUID) {
			<-start
			_, err := f.transfers.Settle(ctx, id, "ref")
			settleErrs <- err
		}(tr.ID)
	}
	resetErr := make(chan error, 1)
	go func() {
		<-start
		_, err := f.runner.Reset(ctx)
		resetErr <- err
	}()
	close(start)

	if err := <-resetErr; err != nil {
		t.Fatalf("reset com settles em voo: %v", err)
	}
	for range pending {
		if err := <-settleErrs; err != nil && !errors.Is(err, httpx.ErrNotFound) {
			t.Errorf("settle concorrente: erro = %v, quero 200 antes do reset ou 404 depois", err)
		}
	}
	if entries, transfers, accounts := tableCounts(t, f); entries != 0 || transfers != 0 || accounts != 0 {
		t.Errorf("entries=%d transfers=%d accounts=%d, quero tudo vazio", entries, transfers, accounts)
	}
}
