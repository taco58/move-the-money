# Move the Money

A small Go HTTP API for accounts and transfers using SQLite and integer cents.

## Current status

Account creation, lookup, history, and transfers are implemented and tested.

## Run

Requires Go 1.26 or newer and a C compiler for `github.com/mattn/go-sqlite3`.

```sh
go mod download
go run ./cmd/server
```

Defaults: port `8080`, database `money.db`. Override with `PORT` and `DB_PATH`;
the database's parent directory must exist.

```sh
PORT=8081 DB_PATH=/tmp/money-demo.db go run ./cmd/server
```

## Create an account

The starting balance is required and must be a nonnegative integer in cents.
Returns `201` with the created account.

```sh
curl -i -X POST http://localhost:8080/accounts \
  -H 'Content-Type: application/json' \
  -d '{"starting_balance_cents":1250}'
```

## Get an account

```sh
curl -i http://localhost:8080/accounts/1
```

Returns `200` with the account and current balance, `404` for a missing account,
or `400` for an invalid ID. IDs must be positive 64-bit integers.

## Get account history

```sh
curl -i http://localhost:8080/accounts/1/transactions
```

Returns `200` with an opening event followed by incoming/outgoing transfers,
ordered by time then transfer ID. Amounts are positive cents; `kind` indicates
direction. The opening amount can be zero. Invalid IDs return `400`; missing
accounts return `404`.

## Tests

```sh
go test ./...
go test -race ./...
```

## Send money

```sh
curl -i -X POST http://localhost:8080/transfers \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: demo-transfer-1' \
  -d '{"from_account_id":1,"to_account_id":2,"amount_cents":100}'
```

Create both accounts first. Amounts must be positive integer cents and accounts
must differ. Returns `201` for a new transfer, or `200` with the original receipt
when the same key and request are retried. Reusing a key with different details
returns `409`. Failed transfers do not consume keys; successful keys persist.

Invalid input returns `400`, missing accounts `404`, insufficient funds or
recipient overflow `409`, writer-lock timeout `503`, and unexpected failures `500`.
Retry uncertain outcomes with the same key and request.

## Scope and next steps

SQLite serializes writes, keeping transfers atomic across service instances at
the cost of write throughput. Amounts use integer cents for one currency.
Authentication, a UI, and deployment are outside this exercise. Next steps:
history pagination and a reconciliation check against the transfer ledger.
