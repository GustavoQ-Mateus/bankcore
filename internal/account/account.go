package account

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Gustavo-QMateus/bankcore/internal/ledger"
	"github.com/Gustavo-QMateus/bankcore/internal/platform/database"
	"github.com/Gustavo-QMateus/bankcore/internal/platform/httpx"
)

const maxRetries = 3

type Status string

const (
	StatusActive  Status = "ACTIVE"
	StatusBlocked Status = "BLOCKED"
)

type Account struct {
	ID           uuid.UUID `json:"id"`
	OwnerID      uuid.UUID `json:"owner_id"`
	Number       string    `json:"number"`
	BalanceCents int64     `json:"balance_cents"`
	Status       Status    `json:"status"`
	Version      int       `json:"version"`
	CreatedAt    time.Time `json:"created_at"`
}

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

func (s *Service) Open(ctx context.Context, ownerID uuid.UUID) (Account, error) {
	for i := 0; i < maxRetries; i++ {
		number := generateNumber()
		var a Account
		err := s.pool.QueryRow(ctx,
			`INSERT INTO accounts (owner_id, number) VALUES ($1, $2)
			 RETURNING id, owner_id, number, balance_cents, status, version, created_at`,
			ownerID, number,
		).Scan(&a.ID, &a.OwnerID, &a.Number, &a.BalanceCents, &a.Status, &a.Version, &a.CreatedAt)
		if err != nil {
			if database.IsUniqueViolation(err) {
				continue
			}
			return Account{}, httpx.ErrInternal
		}
		return a, nil
	}
	return Account{}, httpx.ErrInternal
}

func (s *Service) Get(ctx context.Context, id uuid.UUID) (Account, error) {
	return getByID(ctx, s.pool, id)
}

func (s *Service) Deposit(ctx context.Context, id uuid.UUID, amount int64) (Account, error) {
	return s.mutate(ctx, id, amount, ledger.Credit)
}

func (s *Service) Withdraw(ctx context.Context, id uuid.UUID, amount int64) (Account, error) {
	return s.mutate(ctx, id, -amount, ledger.Debit)
}

func (s *Service) mutate(ctx context.Context, id uuid.UUID, delta int64, typ ledger.EntryType) (Account, error) {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		acc, err := s.tryMutate(ctx, id, delta, typ)
		if err == nil {
			return acc, nil
		}
		if errors.Is(err, errVersionConflict) {
			lastErr = err
			continue
		}
		return Account{}, err
	}
	_ = lastErr
	return Account{}, httpx.ErrConflict("conta em uso concorrente, tente novamente")
}

var errVersionConflict = errors.New("version conflict")

func (s *Service) tryMutate(ctx context.Context, id uuid.UUID, delta int64, typ ledger.EntryType) (Account, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Account{}, httpx.ErrInternal
	}
	defer tx.Rollback(ctx)

	acc, err := getByID(ctx, tx, id)
	if err != nil {
		return Account{}, err
	}
	if acc.Status != StatusActive {
		return Account{}, httpx.ErrValidation("conta não está ativa")
	}

	newBalance := acc.BalanceCents + delta
	if newBalance < 0 {
		return Account{}, httpx.ErrValidation("saldo insuficiente")
	}

	tag, err := tx.Exec(ctx,
		`UPDATE accounts SET balance_cents = $1, version = version + 1
		 WHERE id = $2 AND version = $3`,
		newBalance, id, acc.Version,
	)
	if err != nil {
		return Account{}, httpx.ErrInternal
	}
	if tag.RowsAffected() == 0 {
		return Account{}, errVersionConflict
	}

	amount := delta
	if amount < 0 {
		amount = -amount
	}
	if err := ledger.Append(ctx, tx, id, typ, amount, newBalance, nil); err != nil {
		return Account{}, httpx.ErrInternal
	}

	if err := tx.Commit(ctx); err != nil {
		return Account{}, httpx.ErrInternal
	}

	acc.BalanceCents = newBalance
	acc.Version++
	return acc, nil
}

func (s *Service) List(ctx context.Context) ([]Account, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, owner_id, number, balance_cents, status, version, created_at
		 FROM accounts ORDER BY created_at DESC`)
	if err != nil {
		return nil, httpx.ErrInternal
	}
	defer rows.Close()

	accounts := make([]Account, 0)
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.OwnerID, &a.Number, &a.BalanceCents, &a.Status, &a.Version, &a.CreatedAt); err != nil {
			return nil, httpx.ErrInternal
		}
		accounts = append(accounts, a)
	}
	if rows.Err() != nil {
		return nil, httpx.ErrInternal
	}
	return accounts, nil
}

func (s *Service) SetStatus(ctx context.Context, id uuid.UUID, status Status) (Account, error) {
	var a Account
	err := s.pool.QueryRow(ctx,
		`UPDATE accounts SET status = $1 WHERE id = $2
		 RETURNING id, owner_id, number, balance_cents, status, version, created_at`,
		status, id,
	).Scan(&a.ID, &a.OwnerID, &a.Number, &a.BalanceCents, &a.Status, &a.Version, &a.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Account{}, httpx.ErrNotFound
		}
		return Account{}, httpx.ErrInternal
	}
	return a, nil
}

func (s *Service) Statement(ctx context.Context, id uuid.UUID, limit, offset int) ([]ledger.Entry, error) {
	entries, err := ledger.List(ctx, s.pool, id, limit, offset)
	if err != nil {
		return nil, httpx.ErrInternal
	}
	return entries, nil
}

func getByID(ctx context.Context, q database.Querier, id uuid.UUID) (Account, error) {
	var a Account
	err := q.QueryRow(ctx,
		`SELECT id, owner_id, number, balance_cents, status, version, created_at
		 FROM accounts WHERE id = $1`, id,
	).Scan(&a.ID, &a.OwnerID, &a.Number, &a.BalanceCents, &a.Status, &a.Version, &a.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Account{}, httpx.ErrNotFound
		}
		return Account{}, httpx.ErrInternal
	}
	return a, nil
}

func generateNumber() string {
	const digits = "0123456789"
	b := make([]byte, 10)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(digits))))
		b[i] = digits[n.Int64()]
	}
	return string(b)
}
