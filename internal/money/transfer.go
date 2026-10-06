package money

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/mattn/go-sqlite3"
)

// Transfer returns the committed receipt and whether this is a replay. Only
// successful transfers consume keys; failures return no receipt.
func (s *Service) Transfer(ctx context.Context, input TransferInput) (Transfer, bool, error) {
	if input.FromAccountID <= 0 || input.ToAccountID <= 0 {
		return Transfer{}, false, fmt.Errorf("%w: account IDs must be positive", ErrInvalidInput)
	}
	if input.FromAccountID == input.ToAccountID {
		return Transfer{}, false, fmt.Errorf("%w: sender and recipient must differ", ErrInvalidInput)
	}
	if input.AmountCents <= 0 {
		return Transfer{}, false, fmt.Errorf("%w: amount_cents must be positive", ErrInvalidInput)
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return Transfer{}, false, fmt.Errorf("%w: Idempotency-Key is required and must not be blank", ErrInvalidInput)
	}

	// storage.Open configures BEGIN IMMEDIATE on every connection. Acquire the
	// writer lock before reading keys or balances so another writer cannot act
	// on the same old state, even through a separate database handle.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Transfer{}, false, transferDatabaseError("begin transfer", err)
	}
	defer tx.Rollback()

	stored, err := scanTransfer(tx.QueryRowContext(ctx, `
		SELECT id, from_account_id, to_account_id, amount_cents, created_at
		FROM transfers WHERE idempotency_key = ?
	`, input.IdempotencyKey))
	if err == nil {
		if stored.FromAccountID != input.FromAccountID || stored.ToAccountID != input.ToAccountID ||
			stored.AmountCents != input.AmountCents {
			return Transfer{}, false, ErrIdempotencyConflict
		}
		// Replay the original receipt before consulting today's balances.
		if err := tx.Commit(); err != nil {
			return Transfer{}, false, transferDatabaseError("commit transfer replay", err)
		}
		return stored, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Transfer{}, false, transferDatabaseError("look up transfer key", err)
	}

	senderBalance, err := transferAccountBalance(ctx, tx, input.FromAccountID)
	if err != nil {
		return Transfer{}, false, err
	}
	recipientBalance, err := transferAccountBalance(ctx, tx, input.ToAccountID)
	if err != nil {
		return Transfer{}, false, err
	}
	if senderBalance < input.AmountCents {
		return Transfer{}, false, ErrInsufficientFunds
	}
	// Check before adding. Both Go int64 arithmetic and SQLite arithmetic can
	// leave the intended exact-integer range if overflow is allowed to happen.
	maximumRecipientBalance := int64(math.MaxInt64) - input.AmountCents
	if recipientBalance > maximumRecipientBalance {
		return Transfer{}, false, ErrBalanceOverflow
	}

	// Guard the updates as well as the reads. All statements use tx, never db,
	// so any subsequent error rolls back both balances and the transfer record.
	result, err := tx.ExecContext(ctx, `
		UPDATE accounts SET balance_cents = balance_cents - ?
		WHERE id = ? AND balance_cents >= ?
	`, input.AmountCents, input.FromAccountID, input.AmountCents)
	if err != nil {
		return Transfer{}, false, transferDatabaseError("debit sender", err)
	}
	if err := requireOneTransferUpdate(result); err != nil {
		return Transfer{}, false, fmt.Errorf("debit sender: %w", err)
	}
	result, err = tx.ExecContext(ctx, `
		UPDATE accounts SET balance_cents = balance_cents + ?
		WHERE id = ? AND balance_cents <= ?
	`, input.AmountCents, input.ToAccountID, maximumRecipientBalance)
	if err != nil {
		return Transfer{}, false, transferDatabaseError("credit recipient", err)
	}
	if err := requireOneTransferUpdate(result); err != nil {
		return Transfer{}, false, fmt.Errorf("credit recipient: %w", err)
	}

	receipt, err := scanTransfer(tx.QueryRowContext(ctx, `
		INSERT INTO transfers (idempotency_key, from_account_id, to_account_id, amount_cents)
		VALUES (?, ?, ?, ?)
		RETURNING id, from_account_id, to_account_id, amount_cents, created_at
	`, input.IdempotencyKey, input.FromAccountID, input.ToAccountID, input.AmountCents))
	if err != nil {
		return Transfer{}, false, transferDatabaseError("record transfer", err)
	}
	if err := tx.Commit(); err != nil {
		return Transfer{}, false, transferDatabaseError("commit transfer", err)
	}
	return receipt, false, nil
}

func transferAccountBalance(ctx context.Context, tx *sql.Tx, id int64) (int64, error) {
	var balance int64
	err := tx.QueryRowContext(ctx, "SELECT balance_cents FROM accounts WHERE id = ?", id).Scan(&balance)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: ID %d", ErrAccountNotFound, id)
	}
	if err != nil {
		return 0, transferDatabaseError("get transfer account", err)
	}
	return balance, nil
}

func scanTransfer(row *sql.Row) (Transfer, error) {
	var receipt Transfer
	var timestamp string
	if err := row.Scan(&receipt.ID, &receipt.FromAccountID, &receipt.ToAccountID, &receipt.AmountCents, &timestamp); err != nil {
		return Transfer{}, err
	}
	var err error
	receipt.CreatedAt, err = time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return Transfer{}, fmt.Errorf("parse transfer creation time: %w", err)
	}
	return receipt, nil
}

func requireOneTransferUpdate(result sql.Result) error {
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("expected one updated account, got %d", count)
	}
	return nil
}

func transferDatabaseError(operation string, err error) error {
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code == sqlite3.ErrBusy {
		// Preserve the driver error for inspection as well as the domain error
		// used by HTTP callers. Other SQL failures must remain unexpected errors.
		return fmt.Errorf("%s: %w: %w", operation, ErrDatabaseBusy, err)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
