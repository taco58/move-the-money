package money

import "errors"

var (
	ErrNotImplemented      = errors.New("operation not implemented")
	ErrInvalidInput        = errors.New("invalid input")
	ErrAccountNotFound     = errors.New("account not found")
	ErrInsufficientFunds   = errors.New("insufficient funds")
	ErrIdempotencyConflict = errors.New("idempotency key already used for another transfer")
	ErrBalanceOverflow     = errors.New("recipient balance would overflow")
)
