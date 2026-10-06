# Build Log: Move the Money

## What I set out to build

- An HTTP API for opening accounts, checking balances, viewing history, and
  transferring money.
- Priority: the four correctness rules over features. No UI, authentication,
  or deployment.

## Design decisions

- **Go + SQLite:** small footprint, persistent storage, real transactions and
  constraints. Tradeoff: SQLite allows one writer at a time.
- **Integer cents (`int64`):** no floating point. Recipient overflow is checked
  before addition, since integers alone don't prevent it.
- **One transaction per transfer:** the idempotency check, both balance updates,
  and the transfer record commit together. `BEGIN IMMEDIATE` takes the writer lock
  before balances are read, so separate service instances obey the same rules.
  A process-local mutex wouldn't coordinate separate processes.
- **Idempotency:** a matching key returns the original receipt, checked before
  current funds because the original transfer may have spent them. A key reused
  with different details returns a conflict. Only successful transfers consume keys.

## How I worked with AI

- **Plan and test first:** I planned and justified design choices through discussion
  before implementation, then tests and errors before each operation. Commits
  reflect that sequence.
- **Build on my ideas:** I proposed concurrent goroutines and per-transfer keys,
  then bounced ideas off AI to refine the tests and identify missing cases.
- **Grill the reasoning:** I asked why each approach worked, where it could fail,
  and how to verify it. AI handled most implementation; I questioned the explanations
  and requested a final audit of the generated code.

## Where the AI or I was wrong

- **Duplicate JSON fields (open):** rejecting unknown fields did not reject
  duplicate ones. A request containing `amount_cents` twice (80 and 90) returned
  `201` and transferred 90. Go's decoder silently keeps the last value, and none
  of my original tests covered ambiguous input. An isolated test reproduced it.
  The planned fix is to reject duplicate fields before any transfer runs.
- **Testing which layer protects rule 1:** I suspected the competing-transfer
  test might not depend on the sender's SQL balance guard, so I had AI temporarily
  remove it and rerun. All 50 runs still passed. The writer lock and the
  in-transaction balance check already prevent the overdraft, so the guard is a
  second line of defense. I kept it for that reason.
- **Read-only transaction:** AI proposed one for consistent history reads.
  Reading the driver source showed `ReadOnly` is ignored, and `_txlock=immediate`
  would still take the writer lock. A two-connection audit test confirmed it
  blocked a writer. We used a single query instead.
- **Timestamp ordering**: While reviewing the proposed history query, I noticed it sorted by timestamp text, and I wondered what would happen if two timestamps for the same instant were formatted differently. To find out, I wrote a small test with transfer ID 1 at 2026-01-01T12:00:00Z and ID 2 at 2026-01-01T12:00:00.000Z. They're the same moment, so ID 1 should come first, but the output put ID 2 first because . sorts before Z as text, which bypassed the ID tie-breaker. I fixed the comparison to use parsed times, then IDs, and kept the test as a regression test.

## Evidence of correctness

- Competing 80-cent transfers from a 100-cent account: exactly one succeeds.
  Balances, history, total money, and the transfer count are all checked.
- Injected failures (credit rejected after debit, record insert rejected) roll
  back completely, and the same key succeeds on retry.
- Concurrent retries, conflicting keys, restart persistence, one-cent and
  max-`int64` boundaries.
- Full suite, `-race`, `go vet`, and 50 repetitions of the concurrency tests pass.
  A manual HTTP demo with a server restart also passed.
- The later audit ran in a temporary copy. Its additional tests exposed gaps;
  fixes and permanent regression tests are still pending.

## Limitations

- **Write throughput:** SQLite allows one writer at a time, so concurrent transfers
  queue. Under heavy load, latency grows and requests can hit the 5-second lock
  timeout (`503`).
- **Unpaginated history:** the full history loads per request, so response size,
  memory, and query time grow with account activity.
