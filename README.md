# Move the Money

A small Go HTTP API for accounts and transfers using SQLite and integer cents.

## Current status

Account creation is implemented and tested. Balance lookup, transfers, and
history still return `501 Not Implemented`.
Account-lookup tests are written first and currently fail until implementation.

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

## Tests

```sh
go test ./...
```
