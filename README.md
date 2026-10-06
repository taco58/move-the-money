# Move the Money

A small Go HTTP API for accounts and transfers using SQLite and integer cents.

## Current status

Account creation, lookup, and history are implemented and tested.
Transfers still return `501 Not Implemented`.
Transfer tests define the next feature and intentionally fail until it is implemented.

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
```

Run only the currently implemented account and history features:

```sh
go test ./... -run 'Test(OpenAccount|AccountSchema|CreateAccount|GetAccount|History)'
```
