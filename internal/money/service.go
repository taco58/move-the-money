package money

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
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
	if id <= 0 {
		return Account{}, fmt.Errorf("%w: account ID must be positive", ErrInvalidInput)
	}

	var account Account
	var createdAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, opening_balance_cents, balance_cents, created_at
		FROM accounts
		WHERE id = ?
	`, id).Scan(&account.ID, &account.OpeningBalanceCents, &account.BalanceCents, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, fmt.Errorf("%w: ID %d", ErrAccountNotFound, id)
	}
	if err != nil {
		return Account{}, fmt.Errorf("get account: %w", err)
	}
	account.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return Account{}, fmt.Errorf("parse account creation time: %w", err)
	}
	return account, nil
}

func (s *Service) History(ctx context.Context, accountID int64) ([]HistoryEntry, error) {
	if accountID <= 0 {
		return nil, fmt.Errorf("%w: account ID must be positive", ErrInvalidInput)
	}
	// One SELECT gives the opening event and transfers a single read snapshot,
	// without acquiring the writer lock configured for explicit transactions.
	rows, err := s.db.QueryContext(ctx, `
		SELECT 'opening', opening_balance_cents, NULL, NULL, created_at
		FROM accounts WHERE id = ?
		UNION ALL
		SELECT
			CASE WHEN t.from_account_id = a.id THEN 'outgoing' ELSE 'incoming' END,
			t.amount_cents,
			t.id,
			CASE WHEN t.from_account_id = a.id THEN t.to_account_id ELSE t.from_account_id END,
			t.created_at
		FROM accounts a
		JOIN transfers t ON t.from_account_id = a.id OR t.to_account_id = a.id
		WHERE a.id = ?
	`, accountID, accountID)
	if err != nil {
		return nil, fmt.Errorf("get account history: %w", err)
	}
	defer rows.Close()

	var entries []HistoryEntry
	for rows.Next() {
		var entry HistoryEntry
		var transferID, counterpartyID sql.NullInt64
		var createdAt string
		if err := rows.Scan(&entry.Kind, &entry.AmountCents, &transferID, &counterpartyID, &createdAt); err != nil {
			return nil, fmt.Errorf("scan account history: %w", err)
		}
		entry.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse history creation time: %w", err)
		}
		if transferID.Valid {
			entry.TransferID = &transferID.Int64
		}
		if counterpartyID.Valid {
			entry.CounterpartyID = &counterpartyID.Int64
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read account history: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: ID %d", ErrAccountNotFound, accountID)
	}
	// Opening is always first. Compare parsed times because RFC3339 text can
	// use different fractional-second widths for the same instant.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Kind == "opening" {
			return entries[j].Kind != "opening"
		}
		if entries[j].Kind == "opening" {
			return false
		}
		if entries[i].CreatedAt.Equal(entries[j].CreatedAt) {
			return *entries[i].TransferID < *entries[j].TransferID
		}
		return entries[i].CreatedAt.Before(entries[j].CreatedAt)
	})
	return entries, nil
}
