package money_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"koho-move-the-money/internal/money"
	"koho-move-the-money/internal/storage"
)

func TestHistoryOpeningEventWithoutTransfers(t *testing.T) {
	for _, balance := range []int64{0, 1250, math.MaxInt64} {
		t.Run(fmt.Sprint(balance), func(t *testing.T) {
			_, service := newLookupService(t)
			account, err := service.OpenAccount(context.Background(), balance)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := service.History(context.Background(), account.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertHistoryEntries(t, entries, []money.HistoryEntry{{
				Kind: "opening", AmountCents: balance, CreatedAt: account.CreatedAt,
			}})
		})
	}
}

func TestHistoryNotFound(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(fmt.Sprintf("populated=%t", populated), func(t *testing.T) {
			_, service := newLookupService(t)
			if populated {
				if _, err := service.OpenAccount(context.Background(), 1250); err != nil {
					t.Fatal(err)
				}
			}
			for _, id := range []int64{999, math.MaxInt64} {
				entries, err := service.History(context.Background(), id)
				if !errors.Is(err, money.ErrAccountNotFound) || len(entries) != 0 {
					t.Fatalf("ID %d: want missing account and no history, got %v, %v", id, entries, err)
				}
			}
		})
	}
}

func TestHistoryRejectsInvalidID(t *testing.T) {
	_, service := newLookupService(t)
	for _, id := range []int64{0, -1, math.MinInt64} {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			entries, err := service.History(context.Background(), id)
			if !errors.Is(err, money.ErrInvalidInput) || len(entries) != 0 {
				t.Fatalf("want invalid input and no history, got %v, %v", entries, err)
			}
		})
	}
}

// seedHistoryTransfer builds committed fixture state without invoking Transfer.
// Both balances and the record change together to keep the fixture consistent.
func seedHistoryTransfer(t *testing.T, db *sql.DB, id, from, to, amount int64, at time.Time) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE accounts SET balance_cents = balance_cents - ? WHERE id = ?", amount, from); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("UPDATE accounts SET balance_cents = balance_cents + ? WHERE id = ?", amount, to); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO transfers
		(id, idempotency_key, from_account_id, to_account_id, amount_cents, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, id, fmt.Sprintf("history-fixture-%d", id), from, to, amount, at.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryDirectionsFilteringOrderingAndReadOnly(t *testing.T) {
	db, service := newLookupService(t)
	accounts := make([]money.Account, 5)
	for i := range accounts {
		account, err := service.OpenAccount(context.Background(), 10000)
		if err != nil {
			t.Fatal(err)
		}
		accounts[i] = account
	}
	openingAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tiedAt := openingAt.Add(time.Hour)
	if _, err := db.Exec("UPDATE accounts SET created_at = ?", openingAt.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	target, other, recipient := accounts[0].ID, accounts[1].ID, accounts[2].ID
	// Opening and the first transfer share a timestamp: opening comes first.
	seedHistoryTransfer(t, db, 9, target, other, 1, openingAt)
	// These share a timestamp but are inserted in reverse ID order.
	seedHistoryTransfer(t, db, 8, other, target, 250, tiedAt)
	seedHistoryTransfer(t, db, 2, target, recipient, 500, tiedAt)
	seedHistoryTransfer(t, db, 4, accounts[3].ID, accounts[4].ID, 50, openingAt.Add(time.Minute))
	for i := range accounts {
		var err error
		accounts[i], err = service.GetAccount(context.Background(), accounts[i].ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	want := []money.HistoryEntry{
		{Kind: "opening", AmountCents: 10000, CreatedAt: openingAt},
		{Kind: "outgoing", AmountCents: 1, TransferID: historyID(9), CounterpartyID: historyID(other), CreatedAt: openingAt},
		{Kind: "outgoing", AmountCents: 500, TransferID: historyID(2), CounterpartyID: historyID(recipient), CreatedAt: tiedAt},
		{Kind: "incoming", AmountCents: 250, TransferID: historyID(8), CounterpartyID: historyID(other), CreatedAt: tiedAt},
	}
	for range 2 {
		entries, err := service.History(context.Background(), target)
		if err != nil {
			t.Fatal(err)
		}
		assertHistoryEntries(t, entries, want)
	}
	for _, want := range accounts {
		got, err := service.GetAccount(context.Background(), want.ID)
		if err != nil {
			t.Fatal(err)
		}
		assertLookupAccount(t, got, want)
	}
	for table, want := range map[string]int{"accounts": 5, "transfers": 4} {
		var count int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("history changed %s count: want %d, got %d", table, want, count)
		}
	}
}

func historyID(id int64) *int64 { return &id }

func assertHistoryEntries(t *testing.T, got, want []money.HistoryEntry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("want %d history entries, got %d: %+v", len(want), len(got), got)
	}
	for i := range want {
		if got[i].Kind != want[i].Kind || got[i].AmountCents != want[i].AmountCents || !reflect.DeepEqual(got[i].TransferID, want[i].TransferID) || !reflect.DeepEqual(got[i].CounterpartyID, want[i].CounterpartyID) || !got[i].CreatedAt.Equal(want[i].CreatedAt) {
			t.Fatalf("entry %d: want %+v, got %+v", i, want[i], got[i])
		}
	}
}

func TestHistoryExactLargeTransferAmount(t *testing.T) {
	db, service := newLookupService(t)
	sender, err := service.OpenAccount(context.Background(), math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := service.OpenAccount(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	at := recipient.CreatedAt.Add(time.Second)
	seedHistoryTransfer(t, db, 1, sender.ID, recipient.ID, math.MaxInt64, at)
	for _, account := range []money.Account{sender, recipient} {
		entries, err := service.History(context.Background(), account.ID)
		if err != nil {
			t.Fatal(err)
		}
		kind, counterparty := "incoming", sender.ID
		if account.ID == sender.ID {
			kind, counterparty = "outgoing", recipient.ID
		}
		assertHistoryEntries(t, entries, []money.HistoryEntry{
			{Kind: "opening", AmountCents: account.OpeningBalanceCents, CreatedAt: account.CreatedAt},
			{Kind: kind, AmountCents: math.MaxInt64, TransferID: historyID(1), CounterpartyID: historyID(counterparty), CreatedAt: at},
		})
	}
}

func TestHistoryAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "money.db")
	db, err := storage.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	service := money.NewService(db)
	sender, err := service.OpenAccount(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := service.OpenAccount(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	at := sender.CreatedAt.Add(time.Second)
	seedHistoryTransfer(t, db, 1, sender.ID, recipient.ID, 1, at)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := storage.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	entries, err := money.NewService(reopened).History(context.Background(), sender.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryEntries(t, entries, []money.HistoryEntry{
		{Kind: "opening", AmountCents: 100, CreatedAt: sender.CreatedAt},
		{Kind: "outgoing", AmountCents: 1, TransferID: historyID(1), CounterpartyID: historyID(recipient.ID), CreatedAt: at},
	})
}

func TestHistoryDatabaseFailure(t *testing.T) {
	for _, failure := range []string{"closed database", "unavailable transfers table"} {
		t.Run(failure, func(t *testing.T) {
			db, service := newLookupService(t)
			account, err := service.OpenAccount(context.Background(), 1250)
			if err != nil {
				t.Fatal(err)
			}
			if failure == "closed database" {
				err = db.Close()
			} else {
				_, err = db.Exec("DROP TABLE transfers")
			}
			if err != nil {
				t.Fatal(err)
			}
			entries, err := service.History(context.Background(), account.ID)
			if err == nil || errors.Is(err, money.ErrNotImplemented) || errors.Is(err, money.ErrAccountNotFound) || errors.Is(err, money.ErrInvalidInput) || len(entries) != 0 {
				t.Fatalf("want database failure and no partial history, got %v, %v", entries, err)
			}
		})
	}
}

func TestHistoryCanceledContext(t *testing.T) {
	_, service := newLookupService(t)
	account, err := service.OpenAccount(context.Background(), 1250)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	entries, err := service.History(ctx, account.ID)
	if !errors.Is(err, context.Canceled) || len(entries) != 0 {
		t.Fatalf("want cancellation and no history, got %v, %v", entries, err)
	}
}

func TestHistoryOrdersEquivalentTimestampFormatsByID(t *testing.T) {
	db, service := newLookupService(t)
	account, err := service.OpenAccount(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.OpenAccount(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	at := other.CreatedAt.Add(time.Second).Truncate(time.Second)
	seedHistoryTransfer(t, db, 2, account.ID, other.ID, 1, at)
	seedHistoryTransfer(t, db, 1, other.ID, account.ID, 1, at)
	// These are the same instant, but a text sort places .000Z before Z.
	if _, err := db.Exec("UPDATE transfers SET created_at = ? WHERE id = 2", at.Format("2006-01-02T15:04:05.000Z07:00")); err != nil {
		t.Fatal(err)
	}
	entries, err := service.History(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryEntries(t, entries, []money.HistoryEntry{
		{Kind: "opening", AmountCents: 100, CreatedAt: account.CreatedAt},
		{Kind: "incoming", AmountCents: 1, TransferID: historyID(1), CounterpartyID: historyID(other.ID), CreatedAt: at},
		{Kind: "outgoing", AmountCents: 1, TransferID: historyID(2), CounterpartyID: historyID(other.ID), CreatedAt: at},
	})
}
