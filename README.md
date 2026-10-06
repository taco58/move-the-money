# Move the Money

A small Go HTTP API for accounts and transfers using SQLite and integer cents.

## Current status

Project skeleton only. All four endpoints return `501 Not Implemented`.
Account-creation tests are written first and currently fail until implementation.

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

## Tests

```sh
go test ./...
```
