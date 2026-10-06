package httpapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"koho-move-the-money/internal/httpapi"
	"koho-move-the-money/internal/money"
	"koho-move-the-money/internal/storage"
)

func newTestHandler(t *testing.T) (*sql.DB, http.Handler) {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "money.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, httpapi.NewHandler(money.NewService(db))
}

func TestCreateAccount(t *testing.T) {
	for _, test := range []struct {
		name    string
		body    string
		balance int64
	}{
		{"positive", `{"starting_balance_cents":1250}`, 1250},
		{"zero", `{"starting_balance_cents":0}`, 0},
		{"maximum", `{"starting_balance_cents":9223372036854775807}`, math.MaxInt64},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, handler := newTestHandler(t)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/accounts", strings.NewReader(test.body))
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusCreated {
				t.Fatalf("want 201, got %d: %s", response.Code, response.Body.String())
			}
			if response.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("unexpected content type: %s", response.Header().Get("Content-Type"))
			}
			var account money.Account
			if err := json.Unmarshal(response.Body.Bytes(), &account); err != nil {
				t.Fatal(err)
			}
			if account.ID <= 0 || account.OpeningBalanceCents != test.balance || account.BalanceCents != test.balance || account.CreatedAt.IsZero() {
				t.Fatalf("unexpected account: %+v", account)
			}
			var stored int64
			if err := db.QueryRow("SELECT balance_cents FROM accounts WHERE id = ?", account.ID).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if stored != test.balance {
				t.Fatalf("stored balance %d differs from requested %d", stored, test.balance)
			}
		})
	}
}

func TestCreateAccountRejectsInvalidRequests(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{"negative", `{"starting_balance_cents":-1}`},
		{"missing", `{}`},
		{"null balance", `{"starting_balance_cents":null}`},
		{"fractional", `{"starting_balance_cents":12.50}`},
		{"string", `{"starting_balance_cents":"1250"}`},
		{"overflow", `{"starting_balance_cents":9223372036854775808}`},
		{"unknown field", `{"starting_balance_cents":1250,"currency":"CAD"}`},
		{"malformed", `{"starting_balance_cents":`},
		{"empty", ``},
		{"null object", `null`},
		{"array", `[]`},
		{"trailing object", `{"starting_balance_cents":1250} {}`},
		{"trailing garbage", `{"starting_balance_cents":1250} garbage`},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, handler := newTestHandler(t)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/accounts", strings.NewReader(test.body)))
			if response.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d: %s", response.Code, response.Body.String())
			}
			var body map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["error"] == "" {
				t.Fatalf("expected JSON error, got %s", response.Body.String())
			}
			var count int
			if err := db.QueryRow("SELECT COUNT(*) FROM accounts").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("invalid request created %d accounts", count)
			}
		})
	}
}

func TestCreateAccountDatabaseFailure(t *testing.T) {
	db, handler := newTestHandler(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/accounts", strings.NewReader(`{"starting_balance_cents":1250}`)))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", response.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "could not create account" {
		t.Fatalf("unexpected database error response: %v", body)
	}
}

func TestGetAccountReturnsStoredAccount(t *testing.T) {
	for _, balance := range []int64{0, 1250, math.MaxInt64} {
		t.Run(fmt.Sprint(balance), func(t *testing.T) {
			db, handler := newTestHandler(t)
			service := money.NewService(db)
			if _, err := service.OpenAccount(context.Background(), 50); err != nil {
				t.Fatal(err)
			}
			want, err := service.OpenAccount(context.Background(), balance)
			if err != nil {
				t.Fatal(err)
			}
			if balance == 1250 {
				if _, err := db.Exec("UPDATE accounts SET balance_cents = 750 WHERE id = ?", want.ID); err != nil {
					t.Fatal(err)
				}
				want.BalanceCents = 750
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/accounts/%d", want.ID), nil))
			if response.Code != http.StatusOK {
				t.Fatalf("want 200, got %d: %s", response.Code, response.Body.String())
			}
			if response.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("unexpected content type: %s", response.Header().Get("Content-Type"))
			}
			var got money.Account
			if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.ID != want.ID || got.OpeningBalanceCents != want.OpeningBalanceCents || got.BalanceCents != want.BalanceCents || !got.CreatedAt.Equal(want.CreatedAt) {
				t.Fatalf("want account %+v, got %+v", want, got)
			}
		})
	}
}

func TestGetAccountNotFound(t *testing.T) {
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
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/accounts/%d", test.id), nil))
			assertLookupError(t, response, http.StatusNotFound)
		})
	}
}

func TestGetAccountRejectsInvalidID(t *testing.T) {
	for _, id := range []string{"0", "-1", "abc", "1.5", "1e2", "0x10", "9223372036854775808", "-9223372036854775809", "%20", "1%20OR%201=1"} {
		t.Run(id, func(t *testing.T) {
			db, handler := newTestHandler(t)
			account, err := money.NewService(db).OpenAccount(context.Background(), 1250)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/accounts/"+id, nil))
			assertLookupError(t, response, http.StatusBadRequest)
			var count int
			var balance int64
			if err := db.QueryRow("SELECT COUNT(*) FROM accounts").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow("SELECT balance_cents FROM accounts WHERE id = ?", account.ID).Scan(&balance); err != nil {
				t.Fatal(err)
			}
			if count != 1 || balance != 1250 {
				t.Fatalf("invalid lookup changed stored data: count=%d balance=%d", count, balance)
			}
		})
	}
}

func TestGetAccountDatabaseFailure(t *testing.T) {
	db, handler := newTestHandler(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/accounts/1", nil))
	body := assertLookupError(t, response, http.StatusInternalServerError)
	if body["error"] != "could not get account" {
		t.Fatalf("unexpected database error response: %v", body)
	}
}

func assertLookupError(t *testing.T, response *httptest.ResponseRecorder, status int) map[string]string {
	t.Helper()
	if response.Code != status {
		t.Fatalf("want %d, got %d: %s", status, response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected content type: %s", response.Header().Get("Content-Type"))
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["error"] == "" {
		t.Fatalf("expected JSON error, got %s", response.Body.String())
	}
	return body
}
