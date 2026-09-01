package transfer

import (
	"context"
	"errors"
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
	StatusPending Status = "PENDING"
	StatusSettled Status = "SETTLED"
	StatusFailed  Status = "FAILED"
)

type Transfer struct {
	ID            uuid.UUID `json:"id"`
	FromAccountID uuid.UUID `json:"from_account_id"`
	ToAccountID   uuid.UUID `json:"to_account_id"`
	AmountCents   int64     `json:"amount_cents"`
	Status        Status    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

var errVersionConflict = errors.New("version conflict")

func (s *Service) Transfer(ctx context.Context, from, to uuid.UUID, amount int64, key string) (Transfer, error) {
	if key == "" {
		return Transfer{}, httpx.ErrValidation("header Idempotency-Key é obrigatório")
	}
	if from == to {
		return Transfer{}, httpx.ErrValidation("conta de origem e destino devem ser diferentes")
	}
	if amount <= 0 {
		return Transfer{}, httpx.ErrValidation("amount deve ser positivo")
	}

	t, resolved, err := s.acquire(ctx, from, to, amount, key)
	if err != nil {
		return Transfer{}, err
	}
	if resolved != nil {
		return t, resolved
	}
	if t.Status == StatusSettled {
		return t, nil
	}
	if t.Status == StatusFailed {
		return t, httpx.ErrValidation("transferência anterior falhou para esta Idempotency-Key")
	}

	return s.process(ctx, t)
}

func (s *Service) acquire(ctx context.Context, from, to uuid.UUID, amount int64, key string) (Transfer, error, error) {
	var t Transfer
	err := s.pool.QueryRow(ctx,
		`INSERT INTO transfers (from_account_id, to_account_id, amount_cents, idempotency_key, status)
		 VALUES ($1, $2, $3, $4, 'PENDING')
		 RETURNING id, from_account_id, to_account_id, amount_cents, status, created_at`,
		from, to, amount, key,
	).Scan(&t.ID, &t.FromAccountID, &t.ToAccountID, &t.AmountCents, &t.Status, &t.CreatedAt)
	if err == nil {
		return t, nil, nil
	}
	if !database.IsUniqueViolation(err) {
		return Transfer{}, nil, httpx.ErrInternal
	}

	existing, err := s.findByKey(ctx, key)
	if err != nil {
		return Transfer{}, nil, err
	}
	if existing.FromAccountID != from || existing.ToAccountID != to || existing.AmountCents != amount {
		return Transfer{}, nil, httpx.ErrConflict("Idempotency-Key já usada com parâmetros diferentes")
	}
	return existing, nil, nil
}

func (s *Service) process(ctx context.Context, t Transfer) (Transfer, error) {
	for i := 0; i < maxRetries; i++ {
		err := s.processOnce(ctx, t)
		if err == nil {
			t.Status = StatusSettled
			return t, nil
		}
		if errors.Is(err, errVersionConflict) {
			continue
		}
		var apiErr *httpx.APIError
		if errors.As(err, &apiErr) && apiErr.Status == 400 {
			s.markFailed(ctx, t.ID)
			t.Status = StatusFailed
			return t, err
		}
		return Transfer{}, err
	}
	return Transfer{}, httpx.ErrConflict("conflito de concorrência, tente novamente com a mesma Idempotency-Key")
}

func (s *Service) processOnce(ctx context.Context, t Transfer) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return httpx.ErrInternal
	}
	defer tx.Rollback(ctx)

	var current Status
	if err := tx.QueryRow(ctx, `SELECT status FROM transfers WHERE id = $1 FOR UPDATE`, t.ID).Scan(&current); err != nil {
		return httpx.ErrInternal
	}
	if current == StatusSettled {
		return nil
	}
	if current == StatusFailed {
		return httpx.ErrValidation("transferência já falhou")
	}

	first, second := order(t.FromAccountID, t.ToAccountID)
	accs := map[uuid.UUID]lockedAccount{}
	for _, id := range []uuid.UUID{first, second} {
		a, err := readAccount(ctx, tx, id)
		if err != nil {
			return err
		}
		if a.status != "ACTIVE" {
			return httpx.ErrValidation("conta não está ativa")
		}
		accs[id] = a
	}

	fromAcc := accs[t.FromAccountID]
	toAcc := accs[t.ToAccountID]

	if fromAcc.balance < t.AmountCents {
		return httpx.ErrValidation("saldo insuficiente")
	}

	fromNew := fromAcc.balance - t.AmountCents
	toNew := toAcc.balance + t.AmountCents

	for _, id := range []uuid.UUID{first, second} {
		var newBalance int64
		if id == t.FromAccountID {
			newBalance = fromNew
		} else {
			newBalance = toNew
		}
		if err := optimisticUpdate(ctx, tx, id, newBalance, accs[id].version); err != nil {
			return err
		}
	}

	if err := ledger.Append(ctx, tx, t.FromAccountID, ledger.Debit, t.AmountCents, fromNew, &t.ID); err != nil {
		return httpx.ErrInternal
	}
	if err := ledger.Append(ctx, tx, t.ToAccountID, ledger.Credit, t.AmountCents, toNew, &t.ID); err != nil {
		return httpx.ErrInternal
	}

	if _, err := tx.Exec(ctx, `UPDATE transfers SET status = 'SETTLED' WHERE id = $1`, t.ID); err != nil {
		return httpx.ErrInternal
	}

	if err := tx.Commit(ctx); err != nil {
		return httpx.ErrInternal
	}
	return nil
}

type lockedAccount struct {
	balance int64
	version int
	status  string
}

func readAccount(ctx context.Context, q database.Querier, id uuid.UUID) (lockedAccount, error) {
	var a lockedAccount
	err := q.QueryRow(ctx,
		`SELECT balance_cents, version, status FROM accounts WHERE id = $1`, id,
	).Scan(&a.balance, &a.version, &a.status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return lockedAccount{}, httpx.ErrValidation("conta não encontrada")
		}
		return lockedAccount{}, httpx.ErrInternal
	}
	return a, nil
}

func optimisticUpdate(ctx context.Context, q database.Querier, id uuid.UUID, newBalance int64, version int) error {
	tag, err := q.Exec(ctx,
		`UPDATE accounts SET balance_cents = $1, version = version + 1
		 WHERE id = $2 AND version = $3`,
		newBalance, id, version,
	)
	if err != nil {
		return httpx.ErrInternal
	}
	if tag.RowsAffected() == 0 {
		return errVersionConflict
	}
	return nil
}

func (s *Service) markFailed(ctx context.Context, id uuid.UUID) {
	_, _ = s.pool.Exec(ctx, `UPDATE transfers SET status = 'FAILED' WHERE id = $1`, id)
}

func (s *Service) findByKey(ctx context.Context, key string) (Transfer, error) {
	var t Transfer
	err := s.pool.QueryRow(ctx,
		`SELECT id, from_account_id, to_account_id, amount_cents, status, created_at
		 FROM transfers WHERE idempotency_key = $1`, key,
	).Scan(&t.ID, &t.FromAccountID, &t.ToAccountID, &t.AmountCents, &t.Status, &t.CreatedAt)
	if err != nil {
		return Transfer{}, httpx.ErrInternal
	}
	return t, nil
}

func order(a, b uuid.UUID) (uuid.UUID, uuid.UUID) {
	if a.String() < b.String() {
		return a, b
	}
	return b, a
}
