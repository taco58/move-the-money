# Move the Money

A small Go HTTP API for opening accounts, checking balances, viewing transaction
history, and sending money. Uses SQLite for storage and integer cents for amounts.

## Run

Requires Go 1.26 or newer and a C compiler. The SQLite driver uses cgo.

```sh
go mod download
go run ./cmd/server
```

The server listens on port `8080` and stores data in `money.db`. Data survives
server restarts. Set `PORT` and `DB_PATH` to use different values; the database's
parent directory must exist.

```sh
PORT=8081 DB_PATH=/tmp/money-demo.db go run ./cmd/server
```

## Try the API

With the server running, use another terminal for these commands. The examples
assume a new database where the first two accounts have IDs `1` and `2`. If you
already have accounts, use the IDs returned by the creation requests.

Create an account with **1,250 cents ($12.50)** and another with a zero balance.
Both requests return `201 Created` with the account details.

```sh
curl -i -X POST http://localhost:8080/accounts \
  -H 'Content-Type: application/json' \
  -d '{"starting_balance_cents":1250}'

curl -i -X POST http://localhost:8080/accounts \
  -H 'Content-Type: application/json' \
  -d '{"starting_balance_cents":0}'
```

Send **100 cents ($1.00)** from account `1` to account `2`.

```sh
curl -i -X POST http://localhost:8080/transfers \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: demo-transfer-1' \
  -d '{"from_account_id":1,"to_account_id":2,"amount_cents":100}'
```

The first request returns `201 Created`. Run the same command again to retry:
it returns `200 OK` with the original receipt and does not move money again.
Use a new key for a new transfer. Reusing a successful key with different details
returns `409 Conflict`.

Check the balances and account `1`'s history. After this example, balances should
be **1,150 cents** and **100 cents**, even after retrying.

```sh
curl -i http://localhost:8080/accounts/1
curl -i http://localhost:8080/accounts/2
curl -i http://localhost:8080/accounts/1/transactions
```

History starts with the opening balance, followed by transfers ordered by time
and then transfer ID. The `kind` field identifies `opening`, `incoming`, or
`outgoing`; transfer amounts are positive cents.

Account IDs must be positive 64-bit integers. Starting balances must be
nonnegative integer cents. Transfers require a nonblank `Idempotency-Key`,
different sender and recipient accounts, and a positive integer amount.

| Error | HTTP status |
| --- | --- |
| Invalid input | `400` |
| Account not found | `404` |
| Insufficient funds, recipient overflow, or conflicting key | `409` |
| Database writer-lock timeout | `503` |
| Unexpected failure | `500` |

Retry a transfer with an uncertain outcome using the **same key and request**.
Failed transfers do not consume keys; successful keys persist across restarts.

## Tests

```sh
go test ./...
go test -race ./...
go vet ./...
```

Tests cover exact amounts, retries, persistence, and rollback after injected
database failures. The competing-funds test uses two goroutines and independent
database connections: each tries to spend 80 cents from a 100-cent account, and
exactly one must succeed.

Repeat all three transfer concurrency tests:

```sh
go test -race ./internal/money -run '^TestTransferConcurrent' -count=50
```

## Design and scope

- Amounts use `int64` cents for one currency, with explicit overflow checks.
- Each transfer checks its key and updates both balances and the transfer record
  in one transaction. SQLite serializes writers across service instances using
  the same database file, trading write throughput for a simple concurrency model.
- The exercise covers accounts funded at creation and transfers between those
  accounts. Deposits, withdrawals, and external payment integrations are not included.
- Authentication, a UI, and deployment are outside this project.

## Next steps

- **Deposits and withdrawals:** add or remove funds after account creation, with
  the same exact amounts, retry protection, and transaction history as transfers.
- **Transfer reversals:** return funds through a new transfer linked to the original,
  preserving the history of both movements and enforcing the same balance rules.
- **More useful history:** filter transactions by date or direction and return
  results in pages.
- **Beyond the exercise:** add account ownership and authentication so users can
  only access their own accounts, followed by a small UI for balances and transfers.
