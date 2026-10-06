package money_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"math/big"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	"koho-move-the-money/internal/money"
	"koho-move-the-money/internal/storage"
)

type transferFixture struct {
	db       *sql.DB
	service  *money.Service
	path     string
	accounts []money.Account
}

func newTransferFixture(t *testing.T, balances ...int64) *transferFixture {
	t.Helper()
	f := &transferFixture{path: filepath.Join(t.TempDir(), "money.db")}
	f.db, f.service = openTransferService(t, f.path)
	for _, balance := range balances {
		account, err := f.service.OpenAccount(context.Background(), balance)
		if err != nil {
			t.Fatal(err)
		}
		f.accounts = append(f.accounts, account)
	}
	return f
}

func openTransferService(t *testing.T, path string) (*sql.DB, *money.Service) {
	t.Helper()
	db, err := storage.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, money.NewService(db)
}

func (f *transferFixture) input(key string, from, to int, amount int64) money.TransferInput {
	return money.TransferInput{IdempotencyKey: key, FromAccountID: f.accounts[from].ID,
		ToAccountID: f.accounts[to].ID, AmountCents: amount}
}

func assertTransferReceipt(t *testing.T, f *transferFixture, input money.TransferInput, got money.Transfer) {
	t.Helper()
	if got.ID <= 0 || got.CreatedAt.IsZero() || got.FromAccountID != input.FromAccountID ||
		got.ToAccountID != input.ToAccountID || got.AmountCents != input.AmountCents {
		t.Fatalf("unexpected receipt: %+v for input %+v", got, input)
	}
	var stored money.Transfer
	var key, timestamp string
	if err := f.db.QueryRow(`SELECT id, idempotency_key, from_account_id, to_account_id,
		amount_cents, created_at FROM transfers WHERE id = ?`, got.ID).Scan(
		&stored.ID, &key, &stored.FromAccountID, &stored.ToAccountID, &stored.AmountCents, &timestamp); err != nil {
		t.Fatal(err)
	}
	var err error
	stored.CreatedAt, err = time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		t.Fatal(err)
	}
	if key != input.IdempotencyKey {
		t.Fatalf("stored key %q differs from supplied key %q", key, input.IdempotencyKey)
	}
	assertSameTransfer(t, got, stored)
}

func assertSameTransfer(t *testing.T, got, want money.Transfer) {
	t.Helper()
	if got.ID != want.ID || got.FromAccountID != want.FromAccountID || got.ToAccountID != want.ToAccountID ||
		got.AmountCents != want.AmountCents || !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("want receipt %+v, got %+v", want, got)
	}
}

func requireNewTransfer(t *testing.T, f *transferFixture, input money.TransferInput) money.Transfer {
	t.Helper()
	receipt, replayed, err := f.service.Transfer(context.Background(), input)
	if err != nil || replayed {
		t.Fatalf("want new successful transfer, got receipt=%+v replayed=%t error=%v", receipt, replayed, err)
	}
	assertTransferReceipt(t, f, input, receipt)
	return receipt
}

// Check both the committed ledger and its public history. big.Int keeps the
// conservation assertion exact even when several valid balances sum above int64.
func assertTransferLedger(t *testing.T, f *transferFixture, balances []int64, receipts ...money.Transfer) {
	t.Helper()
	if len(balances) != len(f.accounts) {
		t.Fatal("test fixture balance count mismatch")
	}
	var initialTotal, currentTotal big.Int
	for i, opening := range f.accounts {
		account, err := f.service.GetAccount(context.Background(), opening.ID)
		if err != nil {
			t.Fatal(err)
		}
		want := opening
		want.BalanceCents = balances[i]
		assertLookupAccount(t, account, want)
		if account.BalanceCents < 0 {
			t.Fatalf("account %d went negative", account.ID)
		}
		initialTotal.Add(&initialTotal, big.NewInt(opening.OpeningBalanceCents))
		currentTotal.Add(&currentTotal, big.NewInt(account.BalanceCents))
		wantHistory := []money.HistoryEntry{{Kind: "opening", AmountCents: opening.OpeningBalanceCents, CreatedAt: opening.CreatedAt}}
		for _, receipt := range receipts {
			entry := money.HistoryEntry{AmountCents: receipt.AmountCents, TransferID: historyID(receipt.ID), CreatedAt: receipt.CreatedAt}
			switch account.ID {
			case receipt.FromAccountID:
				entry.Kind, entry.CounterpartyID = "outgoing", historyID(receipt.ToAccountID)
			case receipt.ToAccountID:
				entry.Kind, entry.CounterpartyID = "incoming", historyID(receipt.FromAccountID)
			default:
				continue
			}
			wantHistory = append(wantHistory, entry)
		}
		gotHistory, err := f.service.History(context.Background(), account.ID)
		if err != nil {
			t.Fatal(err)
		}
		assertHistoryEntries(t, gotHistory, wantHistory)
	}
	if initialTotal.Cmp(&currentTotal) != 0 {
		t.Fatalf("money was created or lost: initial=%s current=%s", &initialTotal, &currentTotal)
	}
	var count int
	if err := f.db.QueryRow("SELECT COUNT(*) FROM transfers").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(receipts) {
		t.Fatalf("want %d transfer records, got %d", len(receipts), count)
	}
}

type storedTransferState struct {
	ID, From, To, Amount int64
	Key, Timestamp       string
}

type transferState struct {
	Accounts     []money.Account
	Histories    [][]money.HistoryEntry
	Transfers    []storedTransferState
	AccountCount int
}

func snapshotTransferState(t *testing.T, f *transferFixture) transferState {
	t.Helper()
	var state transferState
	if err := f.db.QueryRow("SELECT COUNT(*) FROM accounts").Scan(&state.AccountCount); err != nil {
		t.Fatal(err)
	}
	for _, account := range f.accounts {
		got, err := f.service.GetAccount(context.Background(), account.ID)
		if err != nil {
			t.Fatal(err)
		}
		history, err := f.service.History(context.Background(), account.ID)
		if err != nil {
			t.Fatal(err)
		}
		state.Accounts = append(state.Accounts, got)
		state.Histories = append(state.Histories, history)
	}
	rows, err := f.db.Query(`SELECT id, from_account_id, to_account_id, amount_cents,
		idempotency_key, created_at FROM transfers ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var record storedTransferState
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

func assertTransferUnchanged(t *testing.T, f *transferFixture, before transferState) {
	t.Helper()
	if after := snapshotTransferState(t, f); !reflect.DeepEqual(before, after) {
		t.Fatalf("rejected/replayed request changed ledger:\nbefore: %+v\nafter: %+v", before, after)
	}
}

func assertTransferFailure(t *testing.T, got money.Transfer, replayed bool, err, want error) {
	t.Helper()
	if !errors.Is(err, want) || got != (money.Transfer{}) || replayed {
		t.Fatalf("want %v and no receipt, got receipt=%+v replayed=%t error=%v", want, got, replayed, err)
	}
}

func TestTransferExactAmountsAndHistory(t *testing.T) {
	for _, test := range []struct {
		name                      string
		sender, recipient, amount int64
	}{
		{"normal", 1000, 200, 325},
		{"one cent", 100, 0, 1},
		{"entire balance", 100, 0, 100},
		{"recipient reaches maximum", 1, math.MaxInt64 - 1, 1},
		{"maximum amount", math.MaxInt64, 0, math.MaxInt64},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newTransferFixture(t, test.sender, test.recipient, 77)
			receipt := requireNewTransfer(t, f, f.input("exact", 0, 1, test.amount))
			assertTransferLedger(t, f, []int64{test.sender - test.amount, test.recipient + test.amount, 77}, receipt)
		})
	}
}

func TestTransferSeveralOneCentTransfers(t *testing.T) {
	f := newTransferFixture(t, 10, 0)
	var receipts []money.Transfer
	for i := range 10 {
		receipts = append(receipts, requireNewTransfer(t, f, f.input(fmt.Sprintf("cent-%d", i), 0, 1, 1)))
	}
	assertTransferLedger(t, f, []int64{0, 10}, receipts...)
}

func TestTransferRejectsInvalidInput(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*money.TransferInput)
	}{
		{"zero amount", func(in *money.TransferInput) { in.AmountCents = 0 }},
		{"negative amount", func(in *money.TransferInput) { in.AmountCents = -1 }},
		{"minimum amount", func(in *money.TransferInput) { in.AmountCents = math.MinInt64 }},
		{"zero sender", func(in *money.TransferInput) { in.FromAccountID = 0 }},
		{"negative sender", func(in *money.TransferInput) { in.FromAccountID = -1 }},
		{"zero recipient", func(in *money.TransferInput) { in.ToAccountID = 0 }},
		{"negative recipient", func(in *money.TransferInput) { in.ToAccountID = -1 }},
		{"same account", func(in *money.TransferInput) { in.ToAccountID = in.FromAccountID }},
		{"empty key", func(in *money.TransferInput) { in.IdempotencyKey = "" }},
		{"whitespace key", func(in *money.TransferInput) { in.IdempotencyKey = " \t\n " }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newTransferFixture(t, 100, 0)
			input := f.input("invalid", 0, 1, 10)
			test.change(&input)
			before := snapshotTransferState(t, f)
			got, replayed, err := f.service.Transfer(context.Background(), input)
			assertTransferFailure(t, got, replayed, err, money.ErrInvalidInput)
			assertTransferUnchanged(t, f, before)
		})
	}
}

func TestTransferMissingAccounts(t *testing.T) {
	for _, test := range []struct {
		name     string
		balances []int64
		from, to int64
	}{
		{"empty database", nil, 1, 2},
		{"sender missing", []int64{100, 0}, 999, 2},
		{"recipient missing", []int64{100, 0}, 1, 999},
		{"both missing", []int64{100, 0}, 998, 999},
		{"maximum valid ID missing", []int64{100}, 1, math.MaxInt64},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newTransferFixture(t, test.balances...)
			before := snapshotTransferState(t, f)
			got, replayed, err := f.service.Transfer(context.Background(), money.TransferInput{
				IdempotencyKey: "missing", FromAccountID: test.from, ToAccountID: test.to, AmountCents: 1})
			assertTransferFailure(t, got, replayed, err, money.ErrAccountNotFound)
			assertTransferUnchanged(t, f, before)
		})
	}
}

func TestTransferRejectsInsufficientFundsAndOverflow(t *testing.T) {
	for _, test := range []struct {
		name                      string
		sender, recipient, amount int64
		want                      error
	}{
		{"insufficient", 100, 0, 101, money.ErrInsufficientFunds},
		{"empty sender", 0, 0, 1, money.ErrInsufficientFunds},
		{"recipient overflow", 1, math.MaxInt64, 1, money.ErrBalanceOverflow},
		{"large addition overflow", math.MaxInt64, 1, math.MaxInt64, money.ErrBalanceOverflow},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newTransferFixture(t, test.sender, test.recipient)
			before := snapshotTransferState(t, f)
			got, replayed, err := f.service.Transfer(context.Background(), f.input("rejected", 0, 1, test.amount))
			assertTransferFailure(t, got, replayed, err, test.want)
			assertTransferUnchanged(t, f, before)
		})
	}
}

func TestTransferCanceledContext(t *testing.T) {
	f := newTransferFixture(t, 100, 0)
	before := snapshotTransferState(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	input := f.input("cancel", 0, 1, 10)
	got, replayed, err := f.service.Transfer(ctx, input)
	assertTransferFailure(t, got, replayed, err, context.Canceled)
	assertTransferUnchanged(t, f, before)
	receipt := requireNewTransfer(t, f, input)
	assertTransferLedger(t, f, []int64{90, 10}, receipt)
}

func TestTransferDatabaseFailure(t *testing.T) {
	f := newTransferFixture(t, 100, 0)
	before := snapshotTransferState(t, f)
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	got, replayed, err := f.service.Transfer(context.Background(), f.input("closed", 0, 1, 10))
	if err == nil || errors.Is(err, money.ErrNotImplemented) || errors.Is(err, money.ErrAccountNotFound) ||
		errors.Is(err, money.ErrInvalidInput) || errors.Is(err, money.ErrDatabaseBusy) || got != (money.Transfer{}) || replayed {
		t.Fatalf("want unexpected database failure and no receipt, got %+v, %t, %v", got, replayed, err)
	}
	f.db, f.service = openTransferService(t, f.path)
	assertTransferUnchanged(t, f, before)
}

func TestTransferRollsBackInjectedFailures(t *testing.T) {
	for _, stage := range []string{"recipient credit after debit", "transfer record insertion"} {
		t.Run(stage, func(t *testing.T) {
			f := newTransferFixture(t, 100, 0)
			before := snapshotTransferState(t, f)
			trigger := `CREATE TRIGGER reject_transfer BEFORE INSERT ON transfers
				BEGIN SELECT RAISE(ABORT, 'injected transfer failure'); END`
			message := "injected transfer failure"
			if stage == "recipient credit after debit" {
				// Assert the stage from inside the write transaction; outside readers
				// cannot see its uncommitted debit. A credit-first implementation fails.
				trigger = fmt.Sprintf(`CREATE TRIGGER reject_transfer BEFORE UPDATE OF balance_cents ON accounts
					WHEN NEW.id = %d AND NEW.balance_cents > OLD.balance_cents
					BEGIN
						SELECT CASE WHEN (SELECT balance_cents FROM accounts WHERE id = %d) != 20
							THEN RAISE(ABORT, 'credit occurred before debit') END;
						SELECT RAISE(ABORT, 'injected credit failure');
					END`, f.accounts[1].ID, f.accounts[0].ID)
				message = "injected credit failure"
			}
			if _, err := f.db.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			input := f.input("rollback", 0, 1, 80)
			got, replayed, err := f.service.Transfer(context.Background(), input)
			var sqliteErr sqlite3.Error
			if !errors.As(err, &sqliteErr) || sqliteErr.ExtendedCode != sqlite3.ErrConstraintTrigger ||
				!strings.Contains(err.Error(), message) || got != (money.Transfer{}) || replayed {
				t.Fatalf("want injected failure %q and no receipt, got %+v, %t, %v", message, got, replayed, err)
			}
			assertTransferUnchanged(t, f, before)
			if _, err := f.db.Exec("DROP TRIGGER reject_transfer"); err != nil {
				t.Fatal(err)
			}
			receipt := requireNewTransfer(t, f, input)
			assertTransferLedger(t, f, []int64{20, 80}, receipt)
		})
	}
}

func TestTransferReplayAfterFundsAreSpent(t *testing.T) {
	f := newTransferFixture(t, 100, 0)
	input := f.input("retry", 0, 1, 80)
	first := requireNewTransfer(t, f, input)
	before := snapshotTransferState(t, f)
	// Returning the original receipt also models a lost first HTTP response.
	for range 2 {
		got, replayed, err := f.service.Transfer(context.Background(), input)
		if err != nil || !replayed {
			t.Fatalf("want successful replay despite only 20 cents remaining, got %+v, %t, %v", got, replayed, err)
		}
		assertSameTransfer(t, got, first)
		assertTransferUnchanged(t, f, before)
	}
	assertTransferLedger(t, f, []int64{20, 80}, first)
}

func TestTransferIdempotencyConflict(t *testing.T) {
	for _, field := range []string{"sender", "recipient", "amount"} {
		t.Run(field, func(t *testing.T) {
			f := newTransferFixture(t, 100, 0, 100)
			input := f.input("global-key", 0, 1, 80)
			first := requireNewTransfer(t, f, input)
			before := snapshotTransferState(t, f)
			switch field {
			case "sender":
				input.FromAccountID = f.accounts[2].ID
			case "recipient":
				input.ToAccountID = f.accounts[2].ID
			case "amount":
				input.AmountCents = 1
			}
			got, replayed, err := f.service.Transfer(context.Background(), input)
			assertTransferFailure(t, got, replayed, err, money.ErrIdempotencyConflict)
			assertTransferUnchanged(t, f, before)
			assertTransferLedger(t, f, []int64{20, 80, 100}, first)
		})
	}
}

func TestTransferKeysArePreservedExactly(t *testing.T) {
	f := newTransferFixture(t, 100, 0)
	var receipts []money.Transfer
	// Whitespace around a nonblank key and case are significant. Treat keys as
	// opaque tokens, not text to normalize. Quotes also exercise SQL parameters.
	for _, key := range []string{"key", " key ", "KEY", "key'quoted"} {
		receipts = append(receipts, requireNewTransfer(t, f, f.input(key, 0, 1, 10)))
	}
	assertTransferLedger(t, f, []int64{60, 40}, receipts...)
}

func TestTransferReplayPersistsAfterReopen(t *testing.T) {
	f := newTransferFixture(t, 100, 0)
	input := f.input("persistent", 0, 1, 80)
	first := requireNewTransfer(t, f, input)
	before := snapshotTransferState(t, f)
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	f.db, f.service = openTransferService(t, f.path)
	got, replayed, err := f.service.Transfer(context.Background(), input)
	if err != nil || !replayed {
		t.Fatalf("want replay after reopening database, got %+v, %t, %v", got, replayed, err)
	}
	assertSameTransfer(t, got, first)
	assertTransferUnchanged(t, f, before)
	assertTransferLedger(t, f, []int64{20, 80}, first)
}

func TestTransferFailedAttemptDoesNotConsumeKey(t *testing.T) {
	f := newTransferFixture(t, 10, 0, 100)
	input := f.input("retry-after-funding", 0, 1, 80)
	before := snapshotTransferState(t, f)
	got, replayed, err := f.service.Transfer(context.Background(), input)
	assertTransferFailure(t, got, replayed, err, money.ErrInsufficientFunds)
	assertTransferUnchanged(t, f, before)
	funding := requireNewTransfer(t, f, f.input("funding", 2, 0, 70))
	retried := requireNewTransfer(t, f, input)
	assertTransferLedger(t, f, []int64{0, 80, 30}, funding, retried)
}

type concurrentTransferResult struct {
	Index    int
	Receipt  money.Transfer
	Replayed bool
	Err      error
}

func concurrentTransfers(t *testing.T, f *transferFixture, inputs [2]money.TransferInput) [2]concurrentTransferResult {
	t.Helper()
	secondDB, secondService := openTransferService(t, f.path)
	// Each handle has its own physical connection; neither goroutine can be
	// serialized by sharing a single-connection sql.DB pool.
	f.db.SetMaxOpenConns(1)
	secondDB.SetMaxOpenConns(1)
	services := [2]*money.Service{f.service, secondService}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	results := make(chan concurrentTransferResult, 2)
	for i := range 2 {
		go func() {
			ready <- struct{}{}
			select {
			case <-start:
			case <-ctx.Done():
				results <- concurrentTransferResult{Index: i, Err: ctx.Err()}
				return
			}
			receipt, replayed, err := services[i].Transfer(ctx, inputs[i])
			results <- concurrentTransferResult{i, receipt, replayed, err}
		}()
	}
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("goroutines did not become ready before deadline")
		}
	}
	close(start)
	var got [2]concurrentTransferResult
	for range 2 {
		select {
		case result := <-results:
			got[result.Index] = result
		case <-ctx.Done():
			t.Fatal("concurrent transfers did not finish before deadline")
		}
	}
	return got
}

func TestTransferConcurrentCompetingFunds(t *testing.T) {
	f := newTransferFixture(t, 100, 0, 0)
	inputs := [2]money.TransferInput{f.input("to-B", 0, 1, 80), f.input("to-C", 0, 2, 80)}
	results := concurrentTransfers(t, f, inputs)
	winner := -1
	for i, result := range results {
		if result.Err == nil {
			if winner != -1 || result.Replayed {
				t.Fatalf("expected exactly one new success, got %+v", results)
			}
			winner = i
			assertTransferReceipt(t, f, inputs[i], result.Receipt)
		} else {
			// SQLITE_BUSY is not an acceptable substitute for checking funds.
			assertTransferFailure(t, result.Receipt, result.Replayed, result.Err, money.ErrInsufficientFunds)
		}
	}
	if winner == -1 {
		t.Fatalf("no transfer succeeded: %+v", results)
	}
	balances := []int64{20, 0, 0}
	balances[winner+1] = 80
	assertTransferLedger(t, f, balances, results[winner].Receipt)
}

func TestTransferConcurrentIdenticalKey(t *testing.T) {
	f := newTransferFixture(t, 100, 0)
	input := f.input("same-key", 0, 1, 80)
	results := concurrentTransfers(t, f, [2]money.TransferInput{input, input})
	for _, result := range results {
		if result.Err != nil {
			t.Fatalf("want two successful responses, got %+v", results)
		}
		assertTransferReceipt(t, f, input, result.Receipt)
	}
	if results[0].Replayed == results[1].Replayed {
		t.Fatalf("want one new transfer and one replay, got %+v", results)
	}
	assertSameTransfer(t, results[0].Receipt, results[1].Receipt)
	assertTransferLedger(t, f, []int64{20, 80}, results[0].Receipt)
}

func TestTransferConcurrentConflictingKey(t *testing.T) {
	f := newTransferFixture(t, 100, 0, 0)
	inputs := [2]money.TransferInput{f.input("same-key", 0, 1, 80), f.input("same-key", 0, 2, 80)}
	results := concurrentTransfers(t, f, inputs)
	winner := -1
	for i, result := range results {
		if result.Err == nil {
			if winner != -1 || result.Replayed {
				t.Fatalf("want exactly one new success, got %+v", results)
			}
			winner = i
			assertTransferReceipt(t, f, inputs[i], result.Receipt)
		} else {
			assertTransferFailure(t, result.Receipt, result.Replayed, result.Err, money.ErrIdempotencyConflict)
		}
	}
	if winner == -1 {
		t.Fatalf("no transfer succeeded: %+v", results)
	}
	balances := []int64{20, 0, 0}
	balances[winner+1] = 80
	assertTransferLedger(t, f, balances, results[winner].Receipt)
}

func TestTransferDatabaseBusy(t *testing.T) {
	f := newTransferFixture(t, 100, 0)
	before := snapshotTransferState(t, f)
	blockerDB, _ := openTransferService(t, f.path)
	// PRAGMA is connection-local. Pin the service pool to the initialized
	// connection and shorten its wait so this contention test stays fast.
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
	input := f.input("busy-retry", 0, 1, 80)
	got, replayed, err := f.service.Transfer(ctx, input)
	assertTransferFailure(t, got, replayed, err, money.ErrDatabaseBusy)
	assertTransferUnchanged(t, f, before)
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	receipt := requireNewTransfer(t, f, input)
	assertTransferLedger(t, f, []int64{20, 80}, receipt)
}
