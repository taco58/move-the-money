package money

import "time"

type Account struct {
	ID                  int64     `json:"id"`
	OpeningBalanceCents int64     `json:"opening_balance_cents"`
	BalanceCents        int64     `json:"balance_cents"`
	CreatedAt           time.Time `json:"created_at"`
}

type TransferInput struct {
	IdempotencyKey string
	FromAccountID  int64
	ToAccountID    int64
	AmountCents    int64
}

type Transfer struct {
	ID            int64     `json:"id"`
	FromAccountID int64     `json:"from_account_id"`
	ToAccountID   int64     `json:"to_account_id"`
	AmountCents   int64     `json:"amount_cents"`
	CreatedAt     time.Time `json:"created_at"`
}

type HistoryEntry struct {
	Kind           string    `json:"kind"` // opening, incoming, or outgoing
	AmountCents    int64     `json:"amount_cents"`
	TransferID     *int64    `json:"transfer_id,omitempty"`
	CounterpartyID *int64    `json:"counterparty_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}
