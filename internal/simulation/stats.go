package simulation

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/GustavoQ-Mateus/bankcore/internal/platform/httpx"
	"github.com/GustavoQ-Mateus/bankcore/internal/transfer"
)

type TransferCounts struct {
	Pending int64 `json:"pending"`
	Settled int64 `json:"settled"`
	Failed  int64 `json:"failed"`
}

type Volume struct {
	Pending int64 `json:"pending"`
	Settled int64 `json:"settled"`
}

type Throughput struct {
	CreatedPerSec int64 `json:"created_per_sec"`
	SettledPerSec int64 `json:"settled_per_sec"`
}

type Latency struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
}

type Ledger struct {
	Accounts          int64 `json:"accounts"`
	DivergentAccounts int64 `json:"divergent_accounts"`
}

type Stats struct {
	CapturedAt          time.Time      `json:"captured_at"`
	Transfers           TransferCounts `json:"transfers"`
	VolumeCents         Volume         `json:"volume_cents"`
	Throughput          Throughput     `json:"throughput"`
	SettlementLatencyMS Latency        `json:"settlement_latency_ms"`
	Ledger              Ledger         `json:"ledger"`
	VersionConflicts    int64          `json:"version_conflicts"`
	Simulation          Status         `json:"simulation"`
}

const transferStatsQuery = `
SELECT
    count(*) FILTER (WHERE t.status = 'PENDING' AND b.booked),
    count(*) FILTER (WHERE t.status = 'SETTLED'),
    count(*) FILTER (WHERE t.status = 'FAILED'),
    coalesce(sum(t.amount_cents) FILTER (WHERE t.status = 'PENDING' AND b.booked), 0)::bigint,
    coalesce(sum(t.amount_cents) FILTER (WHERE t.status = 'SETTLED'), 0)::bigint,
    count(*) FILTER (WHERE t.created_at > now() - interval '1 second'),
    count(*) FILTER (WHERE t.settled_at > now() - interval '1 second')
FROM transfers t
CROSS JOIN LATERAL (SELECT EXISTS (SELECT 1 FROM entries e WHERE e.transfer_id = t.id) AS booked) b`

const latencyQuery = `
SELECT
    coalesce(percentile_cont(0.5) WITHIN GROUP (ORDER BY extract(epoch FROM settled_at - created_at)::float8 * 1000), 0),
    coalesce(percentile_cont(0.95) WITHIN GROUP (ORDER BY extract(epoch FROM settled_at - created_at)::float8 * 1000), 0)
FROM transfers
WHERE settled_at > now() - interval '60 seconds'`

const ledgerQuery = `
SELECT
    count(*),
    count(*) FILTER (WHERE a.balance_cents <> coalesce(e.net, 0))
FROM accounts a
LEFT JOIN (
    SELECT account_id, sum(CASE WHEN type = 'CREDIT' THEN amount_cents ELSE -amount_cents END) AS net
    FROM entries
    GROUP BY account_id
) e ON e.account_id = a.id`

type Reporter struct {
	pool      *pgxpool.Pool
	transfers *transfer.Service
	runner    *Runner
}

func NewReporter(pool *pgxpool.Pool, transfers *transfer.Service, runner *Runner) *Reporter {
	return &Reporter{pool: pool, transfers: transfers, runner: runner}
}

func (r *Reporter) Stats(ctx context.Context) (Stats, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Stats{}, httpx.ErrInternal
	}
	defer tx.Rollback(ctx)

	s := Stats{CapturedAt: time.Now().UTC()}
	if err := tx.QueryRow(ctx, transferStatsQuery).Scan(
		&s.Transfers.Pending, &s.Transfers.Settled, &s.Transfers.Failed,
		&s.VolumeCents.Pending, &s.VolumeCents.Settled,
		&s.Throughput.CreatedPerSec, &s.Throughput.SettledPerSec,
	); err != nil {
		return Stats{}, httpx.ErrInternal
	}
	if err := tx.QueryRow(ctx, latencyQuery).Scan(&s.SettlementLatencyMS.P50, &s.SettlementLatencyMS.P95); err != nil {
		return Stats{}, httpx.ErrInternal
	}
	if err := tx.QueryRow(ctx, ledgerQuery).Scan(&s.Ledger.Accounts, &s.Ledger.DivergentAccounts); err != nil {
		return Stats{}, httpx.ErrInternal
	}
	if err := tx.Commit(ctx); err != nil {
		return Stats{}, httpx.ErrInternal
	}

	s.VersionConflicts = r.transfers.VersionConflicts()
	s.Simulation = r.runner.Status()
	return s, nil
}
