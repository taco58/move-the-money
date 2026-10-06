# Move the Money

A small Go HTTP API for accounts and transfers using SQLite and integer cents.

## Current status

Project skeleton only. The server initializes SQLite; all four endpoints return
`501 Not Implemented`. Money operations and tests are not implemented yet.

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
