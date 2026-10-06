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
	"reflect"
	"strings"
	"testing"
	"time"

	"koho-move-the-money/internal/httpapi"
	"koho-move-the-money/internal/money"
	"koho-move-the-money/internal/storage"
)

type transferHTTPFixture struct {
	db       *sql.DB
	service  *money.Service
	handler  http.Handler
	path     string
	accounts []money.Account
}

func newTransferHTTPFixture(t *testing.T, balances ...int64) *transferHTTPFixture {
	t.Helper()
	f := &transferHTTPFixture{path: filepath.Join(t.TempDir(), "money.db")}
	var err error
	f.db, err = storage.Open(context.Background(), f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.db.Close() })
	f.service = money.NewService(f.db)
	f.handler = httpapi.NewHandler(f.service)
	for _, balance := range balances {
		account, err := f.service.OpenAccount(context.Background(), balance)
		if err != nil {
			t.Fatal(err)
		}
		f.accounts = append(f.accounts, account)
	}
	return f
}

func postTransfer(handler http.Handler, body, key string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/transfers", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func transferJSON(from, to, amount int64) string {
	return fmt.Sprintf(`{"from_account_id":%d,"to_account_id":%d,"amount_cents":%d}`, from, to, amount)
}

func decodeTransferResponse(t *testing.T, response *httptest.ResponseRecorder, status int) money.Transfer {
	t.Helper()
	if response.Code != status || response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("want %d JSON response, got %d %s: %s", status, response.Code,
			response.Header().Get("Content-Type"), response.Body.String())
	}
	var receipt money.Transfer
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.ID <= 0 || receipt.CreatedAt.IsZero() {
		t.Fatalf("incomplete transfer receipt: %+v", receipt)
	}
	return receipt
}

type httpStoredTransfer struct {
	ID, From, To, Amount int64
	Key, Timestamp       string
}

type transferHTTPState struct {
	Accounts     []money.Account
	Histories    [][]money.HistoryEntry
	Transfers    []httpStoredTransfer
	AccountCount int
}

func snapshotTransferHTTPState(t *testing.T, f *transferHTTPFixture) transferHTTPState {
	t.Helper()
	var state transferHTTPState
	if err := f.db.QueryRow("SELECT COUNT(*) FROM accounts").Scan(&state.AccountCount); err != nil {
		t.Fatal(err)
	}
	for _, opening := range f.accounts {
		account, err := f.service.GetAccount(context.Background(), opening.ID)
		if err != nil {
			t.Fatal(err)
		}
		history, err := f.service.History(context.Background(), opening.ID)
		if err != nil {
			t.Fatal(err)
		}
		state.Accounts = append(state.Accounts, account)
		state.Histories = append(state.Histories, history)
	}
	rows, err := f.db.Query(`SELECT id, from_account_id, to_account_id, amount_cents,
		idempotency_key, created_at FROM transfers ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var record httpStoredTransfer
		if err := rows.Scan(&record.ID, &record.From, &record.To, &record.Amount, &record.Key, &record.Timestamp); err != nil {
			t.Fatal(err)
		}
		state.Transfers = append(state.Transfers, record)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return state
}

func assertTransferHTTPUnchanged(t *testing.T, f *transferHTTPFixture, before transferHTTPState) {
	t.Helper()
	if after := snapshotTransferHTTPState(t, f); !reflect.DeepEqual(before, after) {
		t.Fatalf("request changed ledger:\nbefore: %+v\nafter: %+v", before, after)
	}
}

func assertTransferHTTPBalances(t *testing.T, f *transferHTTPFixture, balances ...int64) {
	t.Helper()
	if len(balances) != len(f.accounts) {
		t.Fatal("test fixture balance count mismatch")
	}
	for i, opening := range f.accounts {
		account, err := f.service.GetAccount(context.Background(), opening.ID)
		if err != nil {
			t.Fatal(err)
		}
		if account.BalanceCents != balances[i] || account.OpeningBalanceCents != opening.OpeningBalanceCents ||
			!account.CreatedAt.Equal(opening.CreatedAt) {
			t.Fatalf("account %d: want current balance %d and unchanged opening data, got %+v", opening.ID, balances[i], account)
		}
	}
}

func TestTransferHTTPExactAmountsAndKeyForwarding(t *testing.T) {
	for _, test := range []struct {
		name                      string
		sender, recipient, amount int64
	}{
		{"normal", 1000, 200, 325},
		{"one cent", 1, 0, 1},
		{"maximum amount", math.MaxInt64, 0, math.MaxInt64},
		{"recipient reaches maximum", 1, math.MaxInt64 - 1, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newTransferHTTPFixture(t, test.sender, test.recipient)
			from, to := f.accounts[0].ID, f.accounts[1].ID
			key := "http-key'quoted"
			response := postTransfer(f.handler, transferJSON(from, to, test.amount), key)
			receipt := decodeTransferResponse(t, response, http.StatusCreated)
			state := snapshotTransferHTTPState(t, f)
			if len(state.Transfers) != 1 {
				t.Fatalf("want one transfer record, got %+v", state.Transfers)
			}
			record := state.Transfers[0]
			storedTime, err := time.Parse(time.RFC3339Nano, record.Timestamp)
			if err != nil {
				t.Fatal(err)
			}
			if receipt.FromAccountID != from || receipt.ToAccountID != to || receipt.AmountCents != test.amount ||
				record.ID != receipt.ID || record.From != from || record.To != to || record.Amount != test.amount ||
				record.Key != key || !storedTime.Equal(receipt.CreatedAt) {
				t.Fatalf("receipt or persisted key differs: receipt=%+v record=%+v", receipt, record)
			}
			assertTransferHTTPBalances(t, f, test.sender-test.amount, test.recipient+test.amount)
		})
	}
}

func TestTransferHTTPReplayReturnsOriginalReceipt(t *testing.T) {
	f := newTransferHTTPFixture(t, 100, 0)
	body := transferJSON(f.accounts[0].ID, f.accounts[1].ID, 80)
	first := postTransfer(f.handler, body, "http-retry")
	decodeTransferResponse(t, first, http.StatusCreated)
	before := snapshotTransferHTTPState(t, f)
	// Ignore the first response as a client experiencing a lost response would.
	retry := postTransfer(f.handler, body, "http-retry")
	decodeTransferResponse(t, retry, http.StatusOK)
	if first.Body.String() != retry.Body.String() {
		t.Fatalf("replay changed receipt: first=%s replay=%s", first.Body.String(), retry.Body.String())
	}
	assertTransferHTTPUnchanged(t, f, before)
	assertTransferHTTPBalances(t, f, 20, 80)
}

func TestTransferHTTPRejectsInvalidRequests(t *testing.T) {
	valid := map[string]any{"from_account_id": int64(1), "to_account_id": int64(2), "amount_cents": int64(10)}
	tests := []struct{ name, body, key string }{
		{"missing key", transferJSON(1, 2, 10), ""},
		{"blank key", transferJSON(1, 2, 10), " \t "},
		{"same account", transferJSON(1, 1, 10), "key"},
		{"malformed", `{"from_account_id":`, "key"},
		{"empty body", "", "key"},
		{"null object", "null", "key"},
		{"array", "[]", "key"},
		{"empty object", "{}", "key"},
		{"unknown field", `{"from_account_id":1,"to_account_id":2,"amount_cents":10,"currency":"CAD"}`, "key"},
		{"key in body", `{"from_account_id":1,"to_account_id":2,"amount_cents":10,"idempotency_key":"key"}`, "key"},
		{"trailing object", transferJSON(1, 2, 10) + " {}", "key"},
		{"trailing garbage", transferJSON(1, 2, 10) + " garbage", "key"},
	}
	for _, field := range []string{"from_account_id", "to_account_id", "amount_cents"} {
		for _, invalid := range []struct {
			name  string
			value any
		}{
			{"missing", nil}, {"null", nil}, {"zero", int64(0)}, {"negative", int64(-1)},
			{"fractional", 1.5}, {"string", "10"}, {"boolean", true},
			{"overflow", json.Number("9223372036854775808")},
			{"underflow", json.Number("-9223372036854775809")},
		} {
			fields := make(map[string]any)
			for name, value := range valid {
				fields[name] = value
			}
			if invalid.name == "missing" {
				delete(fields, field)
			} else {
				fields[field] = invalid.value
			}
			body, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			tests = append(tests, struct{ name, body, key string }{field + " " + invalid.name, string(body), "key"})
		}
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newTransferHTTPFixture(t, 100, 0)
			before := snapshotTransferHTTPState(t, f)
			assertLookupError(t, postTransfer(f.handler, test.body, test.key), http.StatusBadRequest)
			assertTransferHTTPUnchanged(t, f, before)
		})
	}
}

func TestTransferHTTPMissingAccounts(t *testing.T) {
	for _, test := range []struct {
		name     string
		balances []int64
		from, to int64
	}{
		{"empty database", nil, 1, 2},
		{"sender missing", []int64{100, 0}, 999, 2},
		{"recipient missing", []int64{100, 0}, 1, 999},
		{"both missing", []int64{100, 0}, 998, 999},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newTransferHTTPFixture(t, test.balances...)
			before := snapshotTransferHTTPState(t, f)
			assertLookupError(t, postTransfer(f.handler, transferJSON(test.from, test.to, 1), "missing"), http.StatusNotFound)
			assertTransferHTTPUnchanged(t, f, before)
		})
	}
}

func TestTransferHTTPInsufficientFundsAndOverflow(t *testing.T) {
	for _, test := range []struct {
		name                      string
		sender, recipient, amount int64
	}{
		{"insufficient funds", 100, 0, 101},
		{"empty sender", 0, 0, 1},
		{"overflow", 1, math.MaxInt64, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newTransferHTTPFixture(t, test.sender, test.recipient)
			before := snapshotTransferHTTPState(t, f)
			body := transferJSON(f.accounts[0].ID, f.accounts[1].ID, test.amount)
			assertLookupError(t, postTransfer(f.handler, body, "conflict"), http.StatusConflict)
			assertTransferHTTPUnchanged(t, f, before)
		})
	}
}

func TestTransferHTTPIdempotencyConflict(t *testing.T) {
	for _, field := range []string{"sender", "recipient", "amount"} {
		t.Run(field, func(t *testing.T) {
			f := newTransferHTTPFixture(t, 100, 0, 100)
			from, to, amount := f.accounts[0].ID, f.accounts[1].ID, int64(80)
			decodeTransferResponse(t, postTransfer(f.handler, transferJSON(from, to, amount), "used-key"), http.StatusCreated)
			before := snapshotTransferHTTPState(t, f)
			switch field {
			case "sender":
				from = f.accounts[2].ID
			case "recipient":
				to = f.accounts[2].ID
			case "amount":
				amount = 1
			}
			assertLookupError(t, postTransfer(f.handler, transferJSON(from, to, amount), "used-key"), http.StatusConflict)
			assertTransferHTTPUnchanged(t, f, before)
		})
	}
}

func TestTransferHTTPDatabaseFailure(t *testing.T) {
	f := newTransferHTTPFixture(t, 100, 0)
	before := snapshotTransferHTTPState(t, f)
	// A real SQL failure lets us inspect the still-open database afterward.
	if _, err := f.db.Exec(`CREATE TRIGGER reject_http_transfer BEFORE INSERT ON transfers
		BEGIN SELECT RAISE(ABORT, 'private database failure'); END`); err != nil {
		t.Fatal(err)
	}
	body := transferJSON(f.accounts[0].ID, f.accounts[1].ID, 80)
	errorBody := assertLookupError(t, postTransfer(f.handler, body, "failure"), http.StatusInternalServerError)
	if errorBody["error"] != "could not create transfer" {
		t.Fatalf("unexpected database error response: %v", errorBody)
	}
	assertTransferHTTPUnchanged(t, f, before)
	if _, err := f.db.Exec("DROP TRIGGER reject_http_transfer"); err != nil {
		t.Fatal(err)
	}
	decodeTransferResponse(t, postTransfer(f.handler, body, "failure"), http.StatusCreated)
	assertTransferHTTPBalances(t, f, 20, 80)
}

func TestTransferHTTPDatabaseBusy(t *testing.T) {
	f := newTransferHTTPFixture(t, 100, 0)
	before := snapshotTransferHTTPState(t, f)
	blockerDB, err := storage.Open(context.Background(), f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer blockerDB.Close()
	f.db.SetMaxOpenConns(1)
	if _, err := f.db.Exec("PRAGMA busy_timeout = 25"); err != nil {
		t.Fatal(err)
	}
	blocker, err := blockerDB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	body := transferJSON(f.accounts[0].ID, f.accounts[1].ID, 80)
	request := httptest.NewRequest(http.MethodPost, "/transfers", strings.NewReader(body)).WithContext(ctx)
	request.Header.Set("Idempotency-Key", "busy-retry")
	response := httptest.NewRecorder()
	f.handler.ServeHTTP(response, request)
	assertLookupError(t, response, http.StatusServiceUnavailable)
	assertTransferHTTPUnchanged(t, f, before)
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	decodeTransferResponse(t, postTransfer(f.handler, body, "busy-retry"), http.StatusCreated)
	assertTransferHTTPBalances(t, f, 20, 80)
}
