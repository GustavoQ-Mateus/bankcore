package simulation

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/GustavoQ-Mateus/bankcore/internal/account"
	"github.com/GustavoQ-Mateus/bankcore/internal/platform/httpx"
	"github.com/GustavoQ-Mateus/bankcore/internal/transfer"
)

const (
	customerName          = "Simulação"
	customerEmail         = "simulacao@bankcore.local"
	disabledPasswordHash  = "!"
	minDepositCents       = 50000
	maxDepositCents       = 500000
	minTransferCents      = 100
	maxTransferCents      = 40000
	batchInterval         = 100 * time.Millisecond
	batchesPerSecond      = 10
	maxConflictAttempts   = 5
	conflictBackoffBase   = 10 * time.Millisecond
	conflictBackoffJitter = 20 * time.Millisecond
)

type Phase string

const (
	PhaseIdle     Phase = "idle"
	PhaseSeeding  Phase = "seeding"
	PhaseRunning  Phase = "running"
	PhaseFinished Phase = "finished"
	PhaseStopped  Phase = "stopped"
)

type Config struct {
	Accounts  int `json:"accounts"`
	Transfers int `json:"transfers"`
	Rate      int `json:"rate"`
	Workers   int `json:"workers"`
}

func DefaultConfig() Config {
	return Config{Accounts: 40, Transfers: 1500, Rate: 60, Workers: 16}
}

type Counters struct {
	Dispatched           int64 `json:"dispatched"`
	Accepted             int64 `json:"accepted"`
	RejectedInsufficient int64 `json:"rejected_insufficient"`
	ConflictExhausted    int64 `json:"conflict_exhausted"`
	Errors               int64 `json:"errors"`
}

type Status struct {
	Phase      Phase      `json:"phase"`
	RunID      *uuid.UUID `json:"run_id"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Config     Config     `json:"config"`
	Counters   Counters   `json:"counters"`
}

type counters struct {
	dispatched           atomic.Int64
	accepted             atomic.Int64
	rejectedInsufficient atomic.Int64
	conflictExhausted    atomic.Int64
	errors               atomic.Int64
}

func (c *counters) snapshot() Counters {
	return Counters{
		Dispatched:           c.dispatched.Load(),
		Accepted:             c.accepted.Load(),
		RejectedInsufficient: c.rejectedInsufficient.Load(),
		ConflictExhausted:    c.conflictExhausted.Load(),
		Errors:               c.errors.Load(),
	}
}

type Runner struct {
	pool       *pgxpool.Pool
	accounts   *account.Service
	transfers  *transfer.Service
	mu         sync.Mutex
	phase      Phase
	runID      uuid.UUID
	startedAt  time.Time
	finishedAt time.Time
	config     Config
	counters   *counters
	cancel     context.CancelFunc
	done       chan struct{}
}

func NewRunner(pool *pgxpool.Pool, accounts *account.Service, transfers *transfer.Service) *Runner {
	return &Runner{
		pool:      pool,
		accounts:  accounts,
		transfers: transfers,
		phase:     PhaseIdle,
		config:    DefaultConfig(),
		counters:  &counters{},
	}
}

func (r *Runner) Start(cfg Config) (Status, error) {
	r.mu.Lock()
	if r.active() {
		r.mu.Unlock()
		return Status{}, httpx.ErrSimulationRunning
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.phase = PhaseSeeding
	r.runID = uuid.New()
	r.startedAt = time.Now().UTC()
	r.finishedAt = time.Time{}
	r.config = cfg
	r.counters = &counters{}
	r.cancel = cancel
	r.done = make(chan struct{})
	run := execution{runner: r, ctx: ctx, id: r.runID, config: cfg, counters: r.counters, done: r.done}
	r.mu.Unlock()

	go run.execute()
	return r.Status(), nil
}

func (r *Runner) Stop() Status {
	r.mu.Lock()
	if !r.active() {
		r.mu.Unlock()
		return r.Status()
	}
	r.cancel()
	done := r.done
	r.mu.Unlock()

	<-done
	return r.Status()
}

func (r *Runner) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()

	s := Status{Phase: r.phase, Config: r.config, Counters: r.counters.snapshot()}
	if r.phase == PhaseIdle {
		return s
	}
	runID := r.runID
	startedAt := r.startedAt
	s.RunID = &runID
	s.StartedAt = &startedAt
	if !r.finishedAt.IsZero() {
		finishedAt := r.finishedAt
		s.FinishedAt = &finishedAt
	}
	return s
}

func (r *Runner) active() bool {
	return r.phase == PhaseSeeding || r.phase == PhaseRunning
}

func (r *Runner) setPhase(phase Phase) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phase = phase
	if phase == PhaseFinished || phase == PhaseStopped {
		r.finishedAt = time.Now().UTC()
	}
}

type execution struct {
	runner   *Runner
	ctx      context.Context
	id       uuid.UUID
	config   Config
	counters *counters
	done     chan struct{}
}

func (e execution) execute() {
	defer close(e.done)

	accounts, err := e.seed()
	if err != nil {
		if e.ctx.Err() == nil {
			log.Printf("simulação %s: falha no seeding: %v", e.id, err)
		}
		e.runner.setPhase(PhaseStopped)
		return
	}

	e.runner.setPhase(PhaseRunning)
	e.run(accounts)

	if e.counters.dispatched.Load() == int64(e.config.Transfers) {
		e.runner.setPhase(PhaseFinished)
		return
	}
	e.runner.setPhase(PhaseStopped)
}

func (e execution) seed() ([]uuid.UUID, error) {
	ownerID, err := e.ensureCustomer()
	if err != nil {
		return nil, err
	}

	accounts := make([]uuid.UUID, 0, e.config.Accounts)
	for i := 0; i < e.config.Accounts; i++ {
		if err := e.ctx.Err(); err != nil {
			return nil, err
		}
		acc, err := e.runner.accounts.Open(e.ctx, ownerID)
		if err != nil {
			return nil, err
		}
		if _, err := e.runner.accounts.Deposit(e.ctx, acc.ID, randomBetween(minDepositCents, maxDepositCents)); err != nil {
			return nil, err
		}
		accounts = append(accounts, acc.ID)
	}
	return accounts, nil
}

func (e execution) ensureCustomer() (uuid.UUID, error) {
	if _, err := e.runner.pool.Exec(e.ctx,
		`INSERT INTO customers (name, email, password_hash) VALUES ($1, $2, $3) ON CONFLICT (email) DO NOTHING`,
		customerName, customerEmail, disabledPasswordHash,
	); err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err := e.runner.pool.QueryRow(e.ctx, `SELECT id FROM customers WHERE email = $1`, customerEmail).Scan(&id)
	return id, err
}

func (e execution) run(accounts []uuid.UUID) {
	jobs := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < e.config.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range jobs {
				e.counters.dispatched.Add(1)
				e.record(e.transfer(accounts, n))
			}
		}()
	}

	e.dispatch(jobs)
	close(jobs)
	wg.Wait()
}

func (e execution) dispatch(jobs chan<- int) {
	ticker := time.NewTicker(batchInterval)
	defer ticker.Stop()

	next := 1
	for batch := 0; ; batch++ {
		for i := 0; i < batchSize(e.config.Rate, batch) && next <= e.config.Transfers; i++ {
			select {
			case jobs <- next:
				next++
			case <-e.ctx.Done():
				return
			}
		}
		if next > e.config.Transfers {
			return
		}
		select {
		case <-ticker.C:
		case <-e.ctx.Done():
			return
		}
	}
}

func batchSize(rate, batch int) int {
	size := rate / batchesPerSecond
	if batch%batchesPerSecond < rate%batchesPerSecond {
		size++
	}
	return size
}

func (e execution) transfer(accounts []uuid.UUID, n int) (transfer.Transfer, error) {
	from, to := randomPair(accounts)
	amount := randomBetween(minTransferCents, maxTransferCents)
	key := fmt.Sprintf("sim-%s-%d", e.id, n)
	ctx := context.WithoutCancel(e.ctx)

	for attempt := 1; ; attempt++ {
		t, err := e.runner.transfers.Transfer(ctx, from, to, amount, key)
		if !isConflict(err) || attempt == maxConflictAttempts {
			return t, err
		}
		time.Sleep(conflictBackoffBase*time.Duration(attempt) + rand.N(conflictBackoffJitter))
	}
}

func (e execution) record(t transfer.Transfer, err error) {
	if err == nil {
		e.counters.accepted.Add(1)
		return
	}
	var apiErr *httpx.APIError
	if !errors.As(err, &apiErr) {
		e.counters.errors.Add(1)
		return
	}
	switch {
	case apiErr.Status == http.StatusBadRequest && t.Status == transfer.StatusFailed:
		e.counters.rejectedInsufficient.Add(1)
	case isConflict(err):
		e.counters.conflictExhausted.Add(1)
	default:
		e.counters.errors.Add(1)
	}
}

func isConflict(err error) bool {
	var apiErr *httpx.APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusConflict && apiErr.Code == "CONFLICT"
}

func randomPair(accounts []uuid.UUID) (uuid.UUID, uuid.UUID) {
	from := rand.IntN(len(accounts))
	to := rand.IntN(len(accounts) - 1)
	if to >= from {
		to++
	}
	return accounts[from], accounts[to]
}

func randomBetween(low, high int64) int64 {
	return low + rand.Int64N(high-low+1)
}
