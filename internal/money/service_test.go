package money_test

import (
	"context"
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
