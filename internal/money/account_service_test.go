package money_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"koho-move-the-money/internal/money"
	"koho-move-the-money/internal/storage"
)

func TestOpenAccount(t *testing.T) {
	for _, balance := range []int64{0, 1250, math.MaxInt64} {
		t.Run(fmt.Sprint(balance), func(t *testing.T) {
			db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "money.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			account, err := money.NewService(db).OpenAccount(context.Background(), balance)
			if err != nil {
				t.Fatal(err)
			}
			if account.ID <= 0 || account.OpeningBalanceCents != balance || account.BalanceCents != balance || account.CreatedAt.IsZero() {
				t.Fatalf("unexpected account: %+v", account)
			}
			var opening, current int64
			var timestamp string
			if err := db.QueryRow("SELECT opening_balance_cents, balance_cents, created_at FROM accounts WHERE id = ?", account.ID).Scan(&opening, &current, &timestamp); err != nil {
				t.Fatal(err)
			}
			storedTime, err := time.Parse(time.RFC3339Nano, timestamp)
			if err != nil {
				t.Fatal(err)
			}
			if opening != balance || current != balance || !storedTime.Equal(account.CreatedAt) {
				t.Fatalf("returned account does not match stored values: %d, %d, %s", opening, current, timestamp)
			}
		})
	}
}

func TestOpenAccountRejectsNegativeBalance(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "money.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := money.NewService(db)
	for _, balance := range []int64{-1, math.MinInt64} {
		if _, err := service.OpenAccount(context.Background(), balance); !errors.Is(err, money.ErrInvalidInput) {
			t.Fatalf("balance %d: want invalid input, got %v", balance, err)
		}
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM accounts").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("invalid requests created %d accounts", count)
	}

}

func TestAccountSchemaRejectsNegativeBalances(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "money.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Bypass the service to test each existing database constraint independently.
	for _, balances := range [][2]int64{{-1, 0}, {0, -1}} {
		if _, err := db.Exec("INSERT INTO accounts (opening_balance_cents, balance_cents) VALUES (?, ?)", balances[0], balances[1]); err == nil {
			t.Fatalf("database accepted opening/current balances %v", balances)
		}
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM accounts").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rejected writes created %d accounts", count)
	}
}

func TestOpenAccountPersistsAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "money.db")
	db, err := storage.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	account, err := money.NewService(db).OpenAccount(context.Background(), 1250)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var opening, balance int64
	if err := db.QueryRow("SELECT opening_balance_cents, balance_cents FROM accounts WHERE id = ?", account.ID).Scan(&opening, &balance); err != nil {
		t.Fatal(err)
	}
	if opening != 1250 || balance != 1250 {
		t.Fatalf("reopened account has opening %d, balance %d", opening, balance)
	}
}

func TestOpenAccountCreatesDistinctAccounts(t *testing.T) {
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "money.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := money.NewService(db)
	first, err := service.OpenAccount(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.OpenAccount(context.Background(), 200)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatalf("accounts share ID %d", first.ID)
	}
}

func newLookupService(t *testing.T) (*sql.DB, *money.Service) {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "money.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, money.NewService(db)
}

func TestGetAccountReturnsStoredAccount(t *testing.T) {
	for _, test := range []struct {
		name             string
		opening, current int64
	}{
		{"zero balance", 0, 0},
		{"changed balance", 1250, 750},
		{"maximum balance", math.MaxInt64, math.MaxInt64},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, service := newLookupService(t)
			// An unrelated first row catches queries that forget to filter by ID.
			if _, err := service.OpenAccount(context.Background(), 50); err != nil {
				t.Fatal(err)
			}
			want, err := service.OpenAccount(context.Background(), test.opening)
			if err != nil {
				t.Fatal(err)
			}
			// Set fixture state without depending on the unfinished transfer feature.
			if _, err := db.Exec("UPDATE accounts SET balance_cents = ? WHERE id = ?", test.current, want.ID); err != nil {
				t.Fatal(err)
			}
			want.BalanceCents = test.current
			for range 2 {
				got, err := service.GetAccount(context.Background(), want.ID)
				if err != nil {
					t.Fatal(err)
				}
				assertLookupAccount(t, got, want)
			}
			var count int
			var opening, current int64
			if err := db.QueryRow("SELECT COUNT(*) FROM accounts").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow("SELECT opening_balance_cents, balance_cents FROM accounts WHERE id = ?", want.ID).Scan(&opening, &current); err != nil {
				t.Fatal(err)
			}
			if count != 2 || opening != test.opening || current != test.current {
				t.Fatalf("lookup changed stored accounts: count=%d opening=%d current=%d", count, opening, current)
			}
		})
	}
}

func assertLookupAccount(t *testing.T, got, want money.Account) {
	t.Helper()
	if got.ID != want.ID || got.OpeningBalanceCents != want.OpeningBalanceCents || got.BalanceCents != want.BalanceCents || !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("want account %+v, got %+v", want, got)
	}
}

func TestGetAccountNotFound(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(fmt.Sprintf("populated=%t", populated), func(t *testing.T) {
			db, service := newLookupService(t)
			wantCount := 0
			if populated {
				if _, err := service.OpenAccount(context.Background(), 1250); err != nil {
					t.Fatal(err)
				}
				wantCount = 1
			}
			for _, id := range []int64{999, math.MaxInt64} {
				account, err := service.GetAccount(context.Background(), id)
				if !errors.Is(err, money.ErrAccountNotFound) {
					t.Fatalf("ID %d: want account not found, got %v", id, err)
				}
				if account.ID != 0 {
					t.Fatalf("missing lookup returned account %+v", account)
				}
			}
			var count int
			if err := db.QueryRow("SELECT COUNT(*) FROM accounts").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != wantCount {
				t.Fatalf("missing lookups changed account count: want %d, got %d", wantCount, count)
			}
		})
	}
}

func TestGetAccountRejectsInvalidID(t *testing.T) {
	_, service := newLookupService(t)
	for _, id := range []int64{0, -1, math.MinInt64} {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			account, err := service.GetAccount(context.Background(), id)
			if !errors.Is(err, money.ErrInvalidInput) {
				t.Fatalf("want invalid input, got %v", err)
			}
			if account.ID != 0 {
				t.Fatalf("invalid lookup returned account %+v", account)
			}
		})
	}
}

func TestGetAccountAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "money.db")
	db, err := storage.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := money.NewService(db).OpenAccount(context.Background(), 1250)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := money.NewService(db).GetAccount(context.Background(), want.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertLookupAccount(t, got, want)
}

func TestGetAccountDatabaseFailure(t *testing.T) {
	db, service := newLookupService(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	account, err := service.GetAccount(context.Background(), 1)
	if err == nil || errors.Is(err, money.ErrNotImplemented) || errors.Is(err, money.ErrAccountNotFound) || errors.Is(err, money.ErrInvalidInput) {
		t.Fatalf("want database failure, got %v", err)
	}
	if account.ID != 0 {
		t.Fatalf("failed lookup returned account %+v", account)
	}
}

func TestGetAccountCanceledContext(t *testing.T) {
	_, service := newLookupService(t)
	account, err := service.OpenAccount(context.Background(), 1250)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.GetAccount(ctx, account.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context cancellation, got %v", err)
	}
}
