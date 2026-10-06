package money

import (
	"context"
	"database/sql"
)

// Service owns the money rules and transaction boundaries.
type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

func (s *Service) OpenAccount(ctx context.Context, startingBalanceCents int64) (Account, error) {
	return Account{}, ErrNotImplemented
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
