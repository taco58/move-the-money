package money

import "errors"

var (
	ErrNotImplemented = errors.New("operation not implemented")
	// ErrInvalidInput covers invalid amounts and nonpositive account IDs.
	ErrInvalidInput = errors.New("invalid input")
	// ErrAccountNotFound means a valid account ID has no stored account,
	// whether requested through account lookup or transaction history.
	// Database and cancellation errors must not be classified as missing accounts.
	ErrAccountNotFound     = errors.New("account not found")
	ErrInsufficientFunds   = errors.New("insufficient funds")
	ErrIdempotencyConflict = errors.New("idempotency key already used for another transfer")
	ErrBalanceOverflow     = errors.New("recipient balance would overflow")
)
