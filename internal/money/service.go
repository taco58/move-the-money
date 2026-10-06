package money

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Service owns the money rules and transaction boundaries.
type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

func (s *Service) OpenAccount(ctx context.Context, startingBalanceCents int64) (Account, error) {
	if startingBalanceCents < 0 {
		return Account{}, fmt.Errorf("%w: starting balance must be nonnegative", ErrInvalidInput)
	}

	var account Account
	var createdAt string
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO accounts (opening_balance_cents, balance_cents)
		VALUES (?, ?)
		RETURNING id, opening_balance_cents, balance_cents, created_at
	`, startingBalanceCents, startingBalanceCents).Scan(
		&account.ID, &account.OpeningBalanceCents, &account.BalanceCents, &createdAt,
	)
	if err != nil {
		return Account{}, fmt.Errorf("create account: %w", err)
	}
	account.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return Account{}, fmt.Errorf("parse account creation time: %w", err)
	}
	return account, nil
}

func (s *Service) GetAccount(ctx context.Context, id int64) (Account, error) {
	return Account{}, ErrNotImplemented
}

func (s *Service) Transfer(ctx context.Context, input TransferInput) (Transfer, bool, error) {
	return Transfer{}, false, ErrNotImplemented
}

func (s *Service) History(ctx context.Context, accountID int64) ([]HistoryEntry, error) {
	return nil, ErrNotImplemented
}
