package httpapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
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
