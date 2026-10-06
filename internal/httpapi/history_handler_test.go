package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"koho-move-the-money/internal/money"
)

func TestHistoryOpeningEventWithoutTransfers(t *testing.T) {
	for _, balance := range []int64{0, 1250, math.MaxInt64} {
		t.Run(fmt.Sprint(balance), func(t *testing.T) {
			db, handler := newTestHandler(t)
			account, err := money.NewService(db).OpenAccount(context.Background(), balance)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/accounts/%d/transactions", account.ID), nil))
			entries := decodeHistoryResponse(t, response)
			if len(entries) != 1 {
				t.Fatalf("want one opening event, got %+v", entries)
			}
			entry := entries[0]
			if entry.Kind != "opening" || entry.AmountCents != balance || entry.TransferID != nil || entry.CounterpartyID != nil || !entry.CreatedAt.Equal(account.CreatedAt) {
				t.Fatalf("unexpected opening event: %+v", entry)
			}
		})
	}
}

func TestHistoryReturnsTransferDetails(t *testing.T) {
	db, handler := newTestHandler(t)
	service := money.NewService(db)
	accounts := make([]money.Account, 4)
	for i := range accounts {
		account, err := service.OpenAccount(context.Background(), 1000)
		if err != nil {
			t.Fatal(err)
		}
		accounts[i] = account
	}
	at := accounts[0].CreatedAt.Add(time.Second)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i, fixture := range []struct{ from, to, amount int64 }{
		{accounts[0].ID, accounts[1].ID, 1},
		{accounts[1].ID, accounts[0].ID, 25},
		{accounts[2].ID, accounts[3].ID, 50},
	} {
		if _, err := tx.Exec("UPDATE accounts SET balance_cents = balance_cents - ? WHERE id = ?", fixture.amount, fixture.from); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec("UPDATE accounts SET balance_cents = balance_cents + ? WHERE id = ?", fixture.amount, fixture.to); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO transfers
			(id, idempotency_key, from_account_id, to_account_id, amount_cents, created_at)
			VALUES (?, ?, ?, ?, ?, ?)`, i+1, fmt.Sprintf("history-fixture-%d", i+1), fixture.from, fixture.to, fixture.amount, at.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/accounts/%d/transactions", accounts[0].ID), nil))
	entries := decodeHistoryResponse(t, response)
	if len(entries) != 3 {
		t.Fatalf("want opening and two relevant transfers, got %+v", entries)
	}
	if entries[0].Kind != "opening" || entries[0].AmountCents != 1000 || entries[0].TransferID != nil || entries[0].CounterpartyID != nil || !entries[0].CreatedAt.Equal(accounts[0].CreatedAt) {
		t.Fatalf("unexpected opening event: %+v", entries[0])
	}
	for i, want := range []struct {
		kind   string
		amount int64
	}{
		{"outgoing", 1}, {"incoming", 25},
	} {
		entry := entries[i+1]
		if entry.Kind != want.kind || entry.AmountCents != want.amount || entry.TransferID == nil || *entry.TransferID != int64(i+1) || entry.CounterpartyID == nil || *entry.CounterpartyID != accounts[1].ID || !entry.CreatedAt.Equal(at) {
			t.Fatalf("unexpected transfer entry: %+v", entry)
		}
	}
}

func decodeHistoryResponse(t *testing.T, response *httptest.ResponseRecorder) []money.HistoryEntry {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected content type: %s", response.Header().Get("Content-Type"))
	}
	var entries []money.HistoryEntry
	if err := json.Unmarshal(response.Body.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	if entries == nil {
		t.Fatal("history must be a JSON array, not null")
	}
	return entries
}

func TestHistoryNotFound(t *testing.T) {
	for _, test := range []struct {
		name      string
		id        int64
		populated bool
	}{
		{"empty database", 1, false},
		{"nonempty database", 999, true},
		{"maximum valid ID", math.MaxInt64, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, handler := newTestHandler(t)
			if test.populated {
				if _, err := money.NewService(db).OpenAccount(context.Background(), 1250); err != nil {
					t.Fatal(err)
				}
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/accounts/%d/transactions", test.id), nil))
			assertLookupError(t, response, http.StatusNotFound)
		})
	}
}

func TestHistoryRejectsInvalidID(t *testing.T) {
	for _, id := range []string{"0", "-1", "abc", "1.5", "1e2", "0x10", "9223372036854775808", "-9223372036854775809", "%20", "1%20OR%201=1"} {
		t.Run(id, func(t *testing.T) {
			_, handler := newTestHandler(t)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/accounts/"+id+"/transactions", nil))
			assertLookupError(t, response, http.StatusBadRequest)
		})
	}
}

func TestHistoryDatabaseFailure(t *testing.T) {
	for _, failure := range []string{"closed database", "unavailable transfers table"} {
		t.Run(failure, func(t *testing.T) {
			db, handler := newTestHandler(t)
			account, err := money.NewService(db).OpenAccount(context.Background(), 1250)
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
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/accounts/%d/transactions", account.ID), nil))
			body := assertLookupError(t, response, http.StatusInternalServerError)
			if body["error"] != "could not get account history" {
				t.Fatalf("unexpected database error response: %v", body)
			}
		})
	}
}
