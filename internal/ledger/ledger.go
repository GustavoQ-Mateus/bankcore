package ledger

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Gustavo-QMateus/bankcore/internal/platform/database"
)

type EntryType string

const (
	Credit EntryType = "CREDIT"
	Debit  EntryType = "DEBIT"
)

type Entry struct {
	ID                uuid.UUID  `json:"id"`
	AccountID         uuid.UUID  `json:"account_id"`
	Type              EntryType  `json:"type"`
	AmountCents       int64      `json:"amount_cents"`
	BalanceAfterCents int64      `json:"balance_after_cents"`
	TransferID        *uuid.UUID `json:"transfer_id,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

func Append(ctx context.Context, q database.Querier, accountID uuid.UUID, typ EntryType, amount, balanceAfter int64, transferID *uuid.UUID) error {
	_, err := q.Exec(ctx,
		`INSERT INTO entries (account_id, type, amount_cents, balance_after_cents, transfer_id)
		 VALUES ($1, $2, $3, $4, $5)`,
		accountID, typ, amount, balanceAfter, transferID,
	)
	return err
}

func List(ctx context.Context, q database.Querier, accountID uuid.UUID, limit, offset int) ([]Entry, error) {
	rows, err := q.Query(ctx,
		`SELECT id, account_id, type, amount_cents, balance_after_cents, transfer_id, created_at
		 FROM entries WHERE account_id = $1
		 ORDER BY created_at DESC, id DESC
		 LIMIT $2 OFFSET $3`,
		accountID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]Entry, 0, limit)
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.AccountID, &e.Type, &e.AmountCents, &e.BalanceAfterCents, &e.TransferID, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
