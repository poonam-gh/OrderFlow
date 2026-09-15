# OrderFlow — project log

This is a build journal, not API reference docs. It's written in the order
things actually happened, and for each step explains not just *what* the
code does but *why* we made that choice at the time. The point: if you (or
anyone) opens this project in 20 years having forgotten everything, reading
this top to bottom should rebuild the reasoning trail, not just the end
state.

This is the **only** doc file for this project — new sections get appended
here as we build, rather than spreading across separate files.

## What OrderFlow is

A small but real e-commerce order-fulfillment backend, built specifically
to be a substantial Go learning project (not a CRUD tutorial). Core flow:

```
POST /orders → reserve stock → write order + outbox event (one DB tx)
    → worker pool polls outbox → calls mock payment gateway through a
      circuit breaker → updates order status → fires a notification
```

Every write to `orders`/`products`/`payments` is also captured into an
`audit_log` table by a Postgres trigger — independent of application code.

The full architecture + milestone plan lives at
`~/.claude/plans/mossy-booping-puddle.md` if you want the forward-looking
plan rather than this backward-looking log.

---

## 1. Project init

The project began as an *empty* layered-architecture skeleton — just
directories, no code:

```
contracts/   handlers/   routers/   services/   views/   domains/
main.go      (13 bytes: just `package main`)
```

That layout — `contracts` (interfaces), `domains` (business models),
`services` (use cases), `handlers` (HTTP), `routers` (routes), `views`
(response shaping) — is a clean/hexagonal-architecture split: business
logic depends only on interfaces, never directly on infrastructure, so it
can be unit-tested with fakes and infrastructure can be swapped freely.

First command:

```
go mod init orderflow
```

This created `go.mod` with `module orderflow` — every internal import in
the project is rooted at `orderflow/...` because of this module name.

Machine context at the time: Go 1.24.3 installed, no Docker, `podman`
installed instead, `postgresql@15` available via Homebrew but no Redis.
This shaped the later decision to run Postgres/Redis via `podman-compose`
rather than assuming Docker (not yet done — see Status at the bottom).

---

## 2. First `main.go` and `config.yaml`

Every other piece of this project — the DB connection, the Redis
connection, the server port, the worker pool size — needs some externally
supplied value. Rather than scatter values through the codebase, there's
one `Config` struct (`config/config.go`) everything else depends on.

We chose a `config.yaml` file (not environment variables). Its shape
mirrors the Go struct via `yaml:"..."` tags, the same way `encoding/json`
uses `json:"..."` tags:

```yaml
server:
  port: 8080
postgres:
  host: localhost
  port: 5432
  user: orderflow
  password: orderflow
  dbname: orderflow
  sslmode: disable
redis:
  addr: localhost:6379
worker:
  count: 4
```

`config.Load(path)` returns `(*Config, error)` instead of panicking — the
core Go idiom: no exceptions, every fallible operation returns an error
value the caller checks immediately. `Config` also has `PostgresDSN()`,
which builds the `postgres://user:pass@host:port/dbname?sslmode=X` string —
used both by the app's Postgres pool and later by the migration tool, so
the DSN format is defined in exactly one place.

The very first `main.go` did everything directly, to prove the wiring
worked end-to-end: load config → connect to Postgres → expose one
`/healthz` HTTP route that pings the DB. That was intentionally the
smallest possible thing that proves the stack is wired correctly. It was
later replaced by a two-line entrypoint delegating to a cobra CLI (section
7) — the logic didn't disappear, it moved into
`dependencies.NewServerDependencies`.

We initially sketched routing with the stdlib `net/http` mux, then switched
to `gin` once it was clear we'd need a real middleware chain (auth, rate
limiting, metrics, panic recovery). `gin.Default()` already wires in
request logging + panic recovery.

---

## 3. What Postgres is, and how we connect to it

Postgres is ACID-compliant and uses **MVCC** (multi-version concurrency
control) instead of relying purely on locks — readers never block writers,
because each transaction sees a consistent snapshot of the data.

Worth remembering:

- **Durability** comes from the **WAL** (write-ahead log) — every change is
  logged before data pages are touched; a crash recovers by replaying it.
  Also how replication works (streaming the WAL to replicas).
- **MVCC** leaves dead row versions behind, which is why **VACUUM**
  (usually autovacuum) is core maintenance, not optional.
- **Isolation levels** — defaults to `READ COMMITTED`; also `REPEATABLE
  READ` and `SERIALIZABLE`.
- **Indexing** — B-tree by default; GIN for JSONB containment, GiST for
  geometric/full-text, BRIN for huge append-only tables.
- **Extensibility** — PL/pgSQL functions, triggers, custom types,
  extensions (`pgcrypto`, `pgvector`, PostGIS). This project leans on this
  directly — see the audit-log triggers in section 8.
- **Where data physically lives** — on disk, as the source of truth. RAM
  (`shared_buffers`) is a cache *in front of* that. Opposite of Redis
  (section 4), where RAM *is* the source of truth.

Driver: `github.com/jackc/pgx/v5` with `pgxpool` — a connection *pool*, not
a single connection, safe to share across goroutines. No ORM — raw SQL, on
purpose, so nothing about what queries run is hidden.

`infra/postgres/db.go` is the only place that knows how to open a pool:

```go
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
    pool, err := pgxpool.New(ctx, dsn)
    if err != nil {
        return nil, fmt.Errorf("create pgx pool: %w", err)
    }
    if err := pool.Ping(ctx); err != nil {
        pool.Close()
        return nil, fmt.Errorf("ping postgres: %w", err)
    }
    return pool, nil
}
```

Pinging immediately means the app **fails fast** at startup if Postgres
isn't reachable, instead of failing confusingly on the first real request.

---

## 4. What Redis is, and how we connect to it

Redis is an in-memory key-value store — commands execute on a single
thread (per core), so every command (`INCR`, `SETNX`, a Lua script) is
atomic by default, no locking needed. That's why it's the natural fit for
rate limiters and distributed locks: the "check-then-set" race can't happen
mid-command.

Worth remembering:

- **Data structures, not just strings** — hashes, lists, sets, sorted sets
  (great for sliding-window rate limiting — score = timestamp), streams,
  bitmaps, HyperLogLog.
- **Where the data actually lives — RAM.** Opposite of Postgres: a `SET`
  goes into the `redis-server` process's in-memory hash table, not a file
  on disk. That's why it's fast.
- **Persistence is optional and bolted on** — RDB (periodic snapshots) or
  AOF (append-only log, replayed on restart). You can run with neither.
- **TTL / expiry** — every key can expire; this is the mechanism behind
  cache invalidation and rate-limit windows.
- **Eviction policies** (`noeviction`, `allkeys-lru`, ...) matter once
  memory fills — pick deliberately based on whether the keyspace is a cache
  or something else.

**Deliberate choice for this project: no persistence.** Redis here is used
for cache-aside reads, rate-limit counters, and short-lived distributed
locks — none of that needs to survive a restart. The real source of truth
(orders, payments, the outbox) lives entirely in Postgres. So
`docker-compose.yml` (not yet written) will intentionally *not* mount a
volume for Redis.

`infra/redis/client.go` is the only place that knows how to open a Redis
connection, using the same fail-fast-ping-on-startup pattern as Postgres.
Driver: `github.com/redis/go-redis/v9`.

Naming note: this file lives in package `redis` (`orderflow/infra/redis`)
and also imports `github.com/redis/go-redis/v9`, whose package is also
named `redis`. That's legal — unqualified names refer to the current
package, so `redis.Client` unambiguously means the imported driver.
Anywhere *else* that imports both, one gets an alias (`app/app_context.go`
imports the driver as `redis` and this wrapper as `redisinfra`).

---

## 5. `app.AppContext` vs `dependencies/`

Once Postgres and Redis connections existed, the question became: which
parts of the app need which dependencies, and where should that wiring
live? Split into two layers, two folders.

**`app/`** — the shared foundation. `AppContext` holds the handful of
things *every* entrypoint needs: `Config`, `*slog.Logger`, `*pgxpool.Pool`,
`*redis.Client`. `NewAppContext` loads config, connects Postgres, connects
Redis, sets up structured logging — this is the single place that defines
"what it means to boot this application." Both `cmd/server.go` and
`cmd/worker.go` start with the same line: `appCtx, err :=
app.NewAppContext(ctx, configPath)`.

Note `cmd/migrate.go` deliberately does **not** use `AppContext` — it only
needs the Postgres DSN, so it loads `Config` directly. No reason to require
Redis to be up just to run a migration.

**`dependencies/`** — what's specific to *how* the process runs. A
`server` process needs a `gin.Engine`; a `worker` process needs a worker
pool and delegator. Neither needs the other's stuff, so each lives in its
own file built *on top of* an `AppContext`:

- `dependencies/server_dependencies.go` → `ServerDependencies{ App
  *app.AppContext; Engine *gin.Engine }`
- `dependencies/worker_dependencies.go` → `WorkerDependencies{ App
  *app.AppContext; Delegator *services.Delegator; Pool *worker.Pool }`

Why split at all: `AppContext` never needs to know gin, `worker`, or
`services` exist — the migrate command doesn't compile against gin for no
reason. `dependencies/` depends on `app/`, never the reverse, so there's no
import cycle. Adding a fourth entrypoint later means one more file in
`dependencies/`, zero changes to `app/`.

`ServerDependencies.Run` owns graceful shutdown: starts
`http.Server.ListenAndServe` in a goroutine, then selects between context
cancellation (SIGINT/SIGTERM) and the server erroring out; on cancellation
it calls `srv.Shutdown` with a 10s timeout so in-flight requests finish.
`/healthz` pings **both** Postgres and Redis — a health check that always
returns 200 regardless of dependency state isn't checking anything.

---

## 6. The worker pool and the service delegator

The concurrency core, split across two packages on purpose: `worker/`
knows nothing about business logic, `services/` knows nothing about
goroutines.

**`worker/`** defines the contract and runs it:

```go
type Job struct {
    ID      string
    Type    string
    Payload []byte
}

type Handler interface {
    Handle(ctx context.Context, job Job) error
}
```

`Pool` runs N goroutines, each doing `select { case <-ctx.Done(): return;
case job := <-p.jobs: p.handler.Handle(ctx, job) }`. **Why `select` on
`ctx.Done()` instead of closing the `jobs` channel to signal shutdown:** if
something submits jobs concurrently during shutdown (later, an outbox
dispatcher), closing the channel risks a "send on closed channel" panic
unless the submitter stops first in exactly the right order. `select`
against `ctx` sidesteps that — every worker independently notices
cancellation and exits, no coordination needed.

**`services/service_delegator.go`** routes a `Job` to a registered function
by `Type`:

```go
type Delegator struct {
    handlers map[string]EventHandlerFunc
}

func (d *Delegator) Handle(ctx context.Context, job worker.Job) error {
    handler, ok := d.handlers[job.Type]
    if !ok {
        return fmt.Errorf("no handler registered for event type %q", job.Type)
    }
    return handler(ctx, job)
}
```

`Delegator.Handle` has the exact signature `worker.Handler` requires, so a
`*Delegator` *is* a `worker.Handler` — Go interfaces are satisfied
structurally, no explicit "implements" needed. This is the seam: `worker`
only ever talks to the `Handler` interface; `Delegator` is the concrete
thing plugged in at wiring time (`dependencies/worker_dependencies.go`).

Why bother with the split: once the Order domain exists, business logic
(`OrderService.HandleCreated`, etc.) plugs in as a plain function
registered against an event-type string — it doesn't need to know it's
running inside a worker pool, and the pool doesn't need to know anything
about orders. `Pool` can be unit-tested with a fake `Handler`; `Delegator`
registrations can be tested by calling `Handle` directly, no goroutines
involved.

As of this writing, `Delegator` has nothing registered — pure plumbing,
waiting for the Order domain.

---

## 7. One binary, three subcommands (cobra)

This project runs in three modes: HTTP API server, background worker pool,
DB migrations. Rather than three separate binaries, `github.com/spf13/cobra`
gives one binary with subcommands: `orderflow server`, `orderflow worker`,
`orderflow migrate up|down`.

- `cmd/root.go` — root command + shared `--config` flag (defaults to
  `config.yaml`).
- `cmd/server.go` / `cmd/worker.go` — build `AppContext`, then
  `ServerDependencies` / `WorkerDependencies`, then run until
  SIGINT/SIGTERM.
- `cmd/migrate.go` — loads `Config` directly (no `AppContext`, no Redis
  needed) and drives `github.com/golang-migrate/migrate/v4` against
  `migrations/` using `cfg.PostgresDSN()`.
- `main.go` — two lines: `cmd.Execute()`. All real logic lives in `cmd/`.

Graceful shutdown pattern, identical in `server` and `worker`:

```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer stop()
appCtx, err := app.NewAppContext(ctx, configPath)
defer appCtx.Close()
deps := dependencies.NewServerDependencies(appCtx)
return deps.Run(ctx, ...)
```

`signal.NotifyContext` cancels `ctx` the moment the process receives
SIGINT/SIGTERM (what `podman stop` sends). Both `Run` methods shut down
cleanly on cancellation instead of the process being killed mid-work.

Why this over splitting into separate binaries (`cmd/server/main.go` +
`cmd/worker/main.go`, considered in the original plan): cobra subcommands
give the same operational separation — deploy/scale `orderflow server` and
`orderflow worker` as independent processes — without maintaining multiple
`main` packages.

---

## 8. Migrations, schema, and the audit-log triggers

The schema is defined in versioned `.sql` files under `migrations/`,
applied via `golang-migrate` (`orderflow migrate up|down`), rather than the
app creating tables on startup. This gives the schema a linear, replayable
history, every environment applies the same steps in the same order, and
rollback is a defined operation (`down.sql`), not "restore from backup."

Naming follows `golang-migrate` convention:
`NNNNNN_description.up.sql` / `.down.sql`, applied in numeric order.

**`000001_init_schema`** — `users`, `products`, `orders`, `order_items`,
`payments`. Every primary key is a `UUID` via `gen_random_uuid()`
(`pgcrypto` extension) rather than a serial integer, to avoid leaking
sequential IDs through the API. Money is `*_cents BIGINT`, never a float —
floats can't represent currency exactly.

**`000002_outbox`** — `outbox_events (id, event_type, payload jsonb,
status, created_at, processed_at)`, with a **partial** index on
`(created_at) WHERE status = 'pending'` (the dispatcher only ever queries
pending rows).

The problem this solves: `OrderService.CreateOrder` needs to both persist
the order and reliably trigger downstream work (charge payment, notify) —
but a crash between "save order" and "call payment API" would lose the
event, and you can't make a DB write and an external API call atomic
together. Fix: write the order **and** an `outbox_events` row in the same
DB transaction — since both are just Postgres writes, that transaction is
atomic. A separate worker then polls `outbox_events` and dispatches (the
worker pool + delegator from section 6). Not yet built: the actual
dispatcher, which will need `SELECT ... FOR UPDATE SKIP LOCKED` so multiple
worker processes can pull different rows without blocking each other.

**`000003_audit_log`** — `audit_log (id, table_name, operation, row_id,
old_data jsonb, new_data jsonb, changed_at)`, plus one generic trigger
function `fn_audit_log()` attached to `orders`/`products`/`payments`:

```sql
CREATE OR REPLACE FUNCTION fn_audit_log() RETURNS TRIGGER AS $$
DECLARE v_row_id UUID;
BEGIN
    IF (TG_OP = 'DELETE') THEN v_row_id := OLD.id; ELSE v_row_id := NEW.id; END IF;
    INSERT INTO audit_log (table_name, operation, row_id, old_data, new_data)
    VALUES (TG_TABLE_NAME, TG_OP, v_row_id,
        CASE WHEN TG_OP IN ('UPDATE','DELETE') THEN to_jsonb(OLD) ELSE NULL END,
        CASE WHEN TG_OP IN ('UPDATE','INSERT') THEN to_jsonb(NEW) ELSE NULL END);
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
```

**Why a trigger instead of application-level audit logging:** a trigger
runs no matter *how* the row changed — through the API, a manual `psql`
fix, a future admin tool, a bug that bypasses the service layer entirely.
App-level logging can be silently skipped by any code path that forgets to
call it; a trigger can't be bypassed short of disabling triggers outright
(itself a loud, auditable action). Same reasoning that makes the WAL
reliable (section 3) — guarantees living below the application layer are
much harder to accidentally break.

One function is reused across all three tables via `TG_TABLE_NAME` and
`to_jsonb(OLD/NEW)`, generic across any table with a UUID `id` column.

Verification (once Postgres is running): `select * from audit_log where
table_name = 'orders';` should show a row the moment anything inserts into
`orders` — proof the trigger fires independent of the Go application.

---

## 9. Walking the pieces: main.go, AppContext, cobra, gin

### `main.go`, line by line

```go
package main

import "orderflow/cmd"

func main() {
    cmd.Execute()
}
```

`package main` marks this as a compiled binary (not a library) — `go
build`/`go run` looks for it as the entrypoint. `import "orderflow/cmd"`
pulls in the `cmd` package. `func main()` is the one function Go actually
calls when the binary runs; here it does exactly one thing —
`cmd.Execute()`.

`main.go` is this thin on purpose: none of the actual dispatch logic (which
subcommand ran, what flags were passed) lives here — that's cobra's job,
in `cmd/`. `main.go` doesn't even import `orderflow/app` — it stays
ignorant of `AppContext` entirely. This is the standard Go convention once
a project has a real CLI: `main.go` becomes a fixed two-line entrypoint
that basically never changes again.

### `AppContext` — what it holds and how it's built

```go
type AppContext struct {
    Config *config.Config
    Logger *slog.Logger
    DB     *pgxpool.Pool
    Redis  *redis.Client
}
```

`NewAppContext(ctx, configPath)` builds all four in order, failing fast at
whichever step breaks first: load config → set up `slog` JSON logging →
open the Postgres pool (pings it immediately) → open the Redis client. If
the Redis step fails, it explicitly closes the Postgres pool it just opened
before returning the error — otherwise that pool would leak, since nobody
would get an `AppContext` back to call `.Close()` on. Every error gets
wrapped with context (`fmt.Errorf("connect redis: %w", err)`), so a failure
says exactly which dependency broke.

`Close()` is the mirror image — closes DB pool + Redis client, called via
`defer appCtx.Close()` right after a successful `NewAppContext`.

### The actual call chain from `main()` to `AppContext`

`main.go` never calls `AppContext` directly — it's several layers of
cobra indirection:

1. **Before `main()` runs at all** — Go executes every imported package's
   `init()` functions first. `main.go` imports `orderflow/cmd`, so every
   file in `cmd/` gets its `init()` run, which is where subcommands
   register themselves: `rootCmd.AddCommand(serverCmd)` (in
   `cmd/server.go`), `rootCmd.AddCommand(workerCmd)`, etc. Nobody calls
   these explicitly — Go does it automatically for any imported package.
   Worth remembering, it's non-obvious the first time you hit it.
2. `main()` calls `cmd.Execute()`.
3. `Execute()` (`cmd/root.go`) calls `rootCmd.Execute()` — cobra reads
   `os.Args`, matches e.g. `"server"` against the commands registered in
   step 1, and finds `serverCmd`.
4. Cobra calls `serverCmd.RunE`, and **that's** where
   `app.NewAppContext(ctx, configPath)` actually gets called.

Full chain: `main() → cmd.Execute() → rootCmd.Execute() [cobra picks
"server"] → serverCmd.RunE → app.NewAppContext(...)`.

### What is cobra

`github.com/spf13/cobra` — the standard Go library for CLIs with
subcommands (what `kubectl`, `docker`, `gh` are built on). The stdlib
`flag` package only handles one flat command; cobra handles a tree of
subcommands, each with their own flags/help, plus flags shared across all
of them.

Core piece is `cobra.Command`:
```go
var serverCmd = &cobra.Command{
    Use:   "server",
    Short: "Run the HTTP API server",
    RunE:  func(cmd *cobra.Command, args []string) error { ... },
}
```
`RunE` (not `Run`) returns an `error`, which `Execute()` propagates up to
one place (`cmd/root.go`) that handles printing/exit — no subcommand
handles its own error output. Commands nest via `AddCommand` (`orderflow
migrate up`/`down` are children of `migrateCmd`, which is itself a child of
`rootCmd`). `PersistentFlags()` on the root command (`--config`) is
inherited by every subcommand automatically; a plain `Flags()` on one
command would only apply there.

### What is gin, and what the `server`/`worker` commands actually are

**gin** (`github.com/gin-gonic/gin`) is the HTTP framework/router for the
API. It maps method+path to a handler (`r.GET("/healthz", func(c
*gin.Context) {...})`), wraps request/response in `*gin.Context`
(`c.JSON`, `c.String`, `c.Status`), and supports middleware —
`gin.Default()` already wires in request logging + panic recovery. It's
the layer that turns "an HTTP request arrived" into "call this Go
function."

**`orderflow server`** (`cmd/server.go`) — builds `AppContext`, builds
`ServerDependencies` (gin engine + routes), then `deps.Run` starts
`http.Server.ListenAndServe` and blocks, serving real HTTP traffic until
SIGINT/SIGTERM triggers graceful shutdown.

**`orderflow worker`** (`cmd/worker.go`) — same `AppContext` bootstrap, but
builds `WorkerDependencies` (delegator + pool) instead and runs the pool:
N goroutines waiting for jobs, no HTTP port opened at all. Running
`server` and `worker` as separate processes (same binary, different
subcommand) is what lets them scale independently later — e.g. 1 server
instance, 4 worker instances.

---

## 10. Switching the Postgres driver to sqlx

Originally `infra/postgres/db.go` used `pgxpool.Pool` directly — pgx's own
native connection pool, with pgx-specific methods (`Ping(ctx)`, `Query`,
etc.). We switched to `sqlx` (`github.com/jmoiron/sqlx`) instead, because
raw `pgxpool`/`pgx` requires hand-writing `rows.Scan(&a, &b, &c)` for every
query result; `sqlx` adds struct-scanning (`db.Get(&order, query, id)`,
`db.Select(&orders, query)`, matching columns to struct fields via `db:"…"`
tags) and named-parameter queries on top — real convenience once we start
writing repository code for the Order/Product/User domains, without giving
up raw SQL.

`sqlx` itself is built on the stdlib `database/sql` interface, not pgx's
native API — so we keep pgx underneath by importing its `database/sql`
*driver adapter* instead of `pgxpool`:

```go
import (
    _ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver name
    "github.com/jmoiron/sqlx"
)

func NewPool(ctx context.Context, dsn string) (*sqlx.DB, error) {
    db, err := sqlx.ConnectContext(ctx, "pgx", dsn)
    if err != nil {
        return nil, fmt.Errorf("connect postgres: %w", err)
    }
    return db, nil
}
```

The blank import (`_ "github.com/jackc/pgx/v5/stdlib"`) exists only for its
`init()` side effect — it registers `"pgx"` as a driver name with
`database/sql`, the same mechanism `_ "github.com/lib/pq"` uses for plain
Postgres, or `_ "github.com/go-sql-driver/mysql"` for MySQL. Nothing in
this file calls anything from that import directly, which is why it needs
the `_` — otherwise `go build` would reject an import with no visible use.

`sqlx.ConnectContext` = `sqlx.Open` + a ping in one call, so the manual
`pool.Ping(ctx)` step from the original version is gone — `ConnectContext`
already fails fast if Postgres isn't reachable.

**Ripple effect:** `AppContext.DB` changed type from `*pgxpool.Pool` to
`*sqlx.DB`, and the `/healthz` check in
`dependencies/server_dependencies.go` changed from `appCtx.DB.Ping(ctx)`
(pgx's method name) to `appCtx.DB.PingContext(ctx)` (the `database/sql`
method name `sqlx.DB` inherits by embedding `*sql.DB`). Those were the only
two places in the codebase that touched the DB type — no domain
repositories exist yet to migrate.

---

## 11. What is a worker, why use one, what is a worker pool

**A "worker"** is a generic term for code (in Go, a goroutine) that
processes units of work pulled from a queue, separate from whatever
produced that work — one half of the classic producer/consumer pattern.
Something *produces* jobs (eventually: the outbox table, once
`OrderService.CreateOrder` writes to it); workers *consume* and execute
them.

**Why use one at all** — to get slow/unreliable operations out of the
request/response path:
- Don't make the HTTP caller wait on a slow/flaky payment gateway — the
  request returns fast (order saved + outbox event written), a worker
  handles payment in the background.
- Retries without blocking anyone — a worker can retry a failed payment
  call later; an HTTP handler can't "retry later," it has to respond now.
- Failure isolation — if the payment gateway is down, background
  processing backs up, but the API keeps responding to everything else.
- Absorbs bursty load — many orders arriving at once doesn't mean that
  many simultaneous external calls; they queue and process at a
  sustainable rate.

**A worker pool specifically** — instead of `go handleJob(job)` per job
(unbounded — a burst could spawn thousands of goroutines at once, each
holding a DB connection, exhausting the pool or memory), a worker pool
starts a **fixed number** of goroutines up front, all pulling from one
shared queue. That fixed number is a deliberate concurrency cap.

Our implementation (`worker/pool.go`, `worker/worker_handler.go`):
`NewPool(count, handler)` spins up `count` goroutines (from
`config.yaml`'s `worker.count`, default 4), each looping
`select { case <-ctx.Done(): return; case job := <-p.jobs:
handler.Handle(ctx, job) }`. `Submit(job)` pushes onto the shared channel;
whichever goroutine is free picks it up next — N workers sharing one
queue, not one goroutine per job. The `handler` plugged in is
`services.Delegator` (section 6) — the pool itself knows nothing about
orders or payments, only "run `Handle` on whatever comes through the
channel." The producer side (something calling `Submit`) isn't built yet
— that'll be the outbox dispatcher, polling `outbox_events` for pending
rows.

---

## 12. What is a goroutine

A goroutine is Go's unit of concurrent execution — a function running
independently, started with `go doSomething()`. Often called a
"lightweight thread," and the "lightweight" part is the key detail:

- An OS thread reserves 1–8MB of stack and costs a real syscall to create.
  A goroutine starts at ~2KB and grows/shrinks dynamically — which is why
  Go code routinely runs thousands or millions of them without issue.
- Goroutines are scheduled by the **Go runtime**, not the OS — the runtime
  multiplexes many goroutines onto a small number of OS threads (**M:N
  scheduling**, M goroutines onto N OS threads, N controlled by
  `GOMAXPROCS`, default = CPU core count). The OS never sees individual
  goroutines.

**Concurrency vs. parallelism:** goroutines give concurrency (many things
in progress, interleaved); true parallelism additionally depends on
`GOMAXPROCS` and available cores.

**Coordination philosophy:** "don't communicate by sharing memory; share
memory by communicating" — prefer passing data *between* goroutines over
**channels** rather than N goroutines mutating one shared variable behind a
mutex. `worker/pool.go`'s `jobs chan Job` is exactly this: the channel is
the shared communication point, not a shared slice + lock.

**A subtlety that shaped the worker pool code:** a panic inside a goroutine
crashes the whole process unless `recover()` catches it *in that same
goroutine* — recover can't reach across goroutines. That's why gin's
`Recovery` middleware only protects the goroutine handling one HTTP
request, and why `worker/pool.go`'s job loop logs a handler error
(`slog.Error`) instead of letting a bad job ever risk panicking and taking
the whole worker process down.

**Where goroutines already show up here:** gin runs every incoming HTTP
request in its own goroutine automatically; `ServerDependencies.Run` starts
one explicitly (`go func() { srv.ListenAndServe() }()`) so it can serve
*and* simultaneously `select` on `ctx.Done()` for shutdown; and
`worker.Pool.Run` starts exactly `count` of them
(`for i := 0; i < p.workers; i++ { go p.worker(ctx, i, done) }`) — the
worker pool from section 11, concretely.

---

## 13. Why OS concepts keep showing up in these explanations

Go is a runtime sitting on top of the OS, not a self-contained virtual
machine — a few things stay genuinely OS-level no matter what:

1. **CPU execution.** Only the OS puts something on a core. Go schedules
   goroutines onto OS threads; the OS schedules those threads onto cores.
   Go added its own scheduling layer specifically because OS threads are
   too expensive at the scale Go wants — that's *why* explaining
   goroutines requires contrasting them with OS threads (section 12).
2. **I/O is a syscall, always.** Every network read (HTTP, Postgres,
   Redis) or file read ultimately asks the kernel to do it. Go's runtime
   avoids blocking an OS thread while a goroutine waits on I/O, but the
   underlying operation is still a syscall handed to the OS.
3. **Process lifecycle is an OS concept.**
   `signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)`
   (`cmd/server.go`, `cmd/worker.go`) hooks directly into how the kernel
   tells a process to stop. `podman stop` (or Kubernetes terminating a
   pod) delivers SIGTERM straight to the process — graceful shutdown *is*
   an OS-level event reaching this code, not a Go abstraction layered over
   it.

Why it matters practically: once this ships as a container/pod, it's not
"a Go program" in isolation — it's a process the OS/orchestrator manages
(signals, cgroup memory/CPU limits, a PID, OOM-kill). Backend engineering
at an SDE3 level lives right at that boundary between application code and
what the OS actually does with it — also exactly the kind of thing that
gets probed in interviews ("what happens on SIGKILL vs SIGTERM," "why does
your app hang on shutdown sometimes").

---

## 14. Getting it running for the first time

Everything up to here had been built but never actually run against a real
Postgres/Redis. This section covers getting that infra up and proving the
whole chain works end to end.

**Tooling gaps hit along the way:**
- No `podman-compose` installed, and `brew install` was blocked by an
  unaccepted Xcode license (needs `sudo`, so left for you to accept
  manually if you want Homebrew usable later). Installed `podman-compose`
  via `pipx install podman-compose` instead — sidesteps Homebrew entirely.
- No Podman VM existed yet: `podman machine init` then `podman machine
  start` (Podman on macOS always runs containers inside a small Linux VM —
  there's no native Linux container support on macOS, unlike on Linux
  hosts).

**`docker-compose.yml`** — two services, Postgres 16 and Redis 7:
```yaml
services:
  postgres:
    image: postgres:16
    environment: { POSTGRES_USER: orderflow, POSTGRES_PASSWORD: orderflow, POSTGRES_DB: orderflow }
    ports: ["5433:5432"]
    volumes: ["orderflow_postgres_data:/var/lib/postgresql/data"]
  redis:
    image: redis:7
    ports: ["6379:6379"]
volumes:
  orderflow_postgres_data:
```
Postgres gets a named volume (it's the source of truth — see section 3).
Redis deliberately gets **no** volume, per the decision in section 4: it's
only used for cache/rate-limit/locks here, nothing that needs to survive a
restart.

**A real port conflict, and how we resolved it:** Postgres is mapped to
host port **5433**, not the standard 5432. `lsof -nP -iTCP:5432 -sTCP:LISTEN`
showed a Postgres process already running directly on this Mac (PID-owned
by the same user, not managed by `brew services` — likely leftover from
something unrelated to this project) already bound to `127.0.0.1:5432`.
Our Go binary connecting to `localhost:5432` was silently talking to *that*
process instead of the container, and failed with `role "orderflow" does
not exist` — a red herring that looked like a migration/auth bug but was
actually a port collision. Rather than kill an unknown pre-existing
process, we remapped our container's host port to `5433` and updated
`config.yaml`'s `postgres.port` to match. **Lesson:** "connection refused"
or "role does not exist" against `localhost` doesn't guarantee you're
talking to the container you think you are — check `lsof -iTCP:<port>`
first when a DB connection error looks inexplicable.

**Bringing it up and applying migrations:**
```
podman-compose up -d
go run . migrate up
```
Verified independently of the Go code, directly in `psql`:
```
podman exec apis_postgres_1 psql -U orderflow -d orderflow -c "\dt"
```
confirmed all 8 tables exist (`users`, `products`, `orders`, `order_items`,
`payments`, `outbox_events`, `audit_log`, plus `schema_migrations` which
`golang-migrate` itself uses to track applied versions), and querying
`pg_trigger` confirmed all three audit triggers
(`trg_orders_audit`/`trg_products_audit`/`trg_payments_audit`) attached
correctly.

**First successful end-to-end run:**
```
go run . server   # -> GET /healthz returns 200, pinging both Postgres and Redis
go run . worker   # -> logs "worker pool starting" workers=4, then idles waiting for jobs
```
This is the first point in the project where the whole chain — config,
Postgres, Redis, `AppContext`, `ServerDependencies`/`WorkerDependencies`,
gin, the worker pool — was proven to actually work together, not just
compile.

---

## 15. First real endpoint: `POST /orders` (no validation yet)

Deliberately the simplest possible version — no stock checks, no outbox
event, no worker involvement yet. Just: take a request, write a row,
return it. Validation and the outbox/worker wiring are the next two steps,
built on top of this once it's proven to work.

The layers, following the same pattern established in earlier sections:

- **`domains/order/order.go`** — the `Order` struct, with `db:"..."` tags
  (for `sqlx` scanning) and `json:"..."` tags (for the HTTP response)
  living side by side on the same struct — fine at this simple stage,
  though it does mean the DB row shape and the API response shape are
  currently the same thing. That's exactly what `views/` (still empty) is
  for later: a separate response DTO once they need to diverge.
- **`contracts/order_repository.go`** — `OrderRepository` interface,
  `Create(ctx, *order.Order) error`. Same pattern as `worker.Handler`
  (section 6): the service depends on this interface, not a concrete type.
- **`infra/postgres/order_repo.go`** — the concrete implementation:
  ```go
  func (r *OrderRepository) Create(ctx context.Context, o *order.Order) error {
      query := `INSERT INTO orders (user_id, total_cents) VALUES ($1, $2)
                RETURNING id, status, created_at, updated_at`
      return r.db.QueryRowxContext(ctx, query, o.UserID, o.TotalCents).
          Scan(&o.ID, &o.Status, &o.CreatedAt, &o.UpdatedAt)
  }
  ```
  Only two columns are inserted explicitly; `id` (via `gen_random_uuid()`),
  `status` (defaults to `'pending'`), and the timestamps all come from the
  schema's own `DEFAULT`s (section 8) — `RETURNING` reads them straight
  back into the same `*Order` the caller passed in, so the caller ends up
  with the fully-populated row without a second query.
- **`services/order_service.go`** — `OrderService.CreateOrder(ctx, userID,
  totalCents)` builds the domain struct and calls the repo. Thin on
  purpose right now; this is exactly where stock-reservation and the
  outbox-event write will get added next, inside the same DB transaction.
- **`handlers/order_handler.go`** — parses the JSON body
  (`c.ShouldBindJSON`), calls the service, returns `201` with the created
  order or a `400`/`500` with `{"error": "..."}` on failure.

Wired into `dependencies/server_dependencies.go`, right after `/healthz`:
```go
orderRepo := postgres.NewOrderRepository(appCtx.DB)
orderService := services.NewOrderService(orderRepo)
orderHandler := handlers.NewOrderHandler(orderService)
orderHandler.Register(engine)
```

**A real constraint hit while testing:** `orders.user_id` has a foreign key
to `users`, and the Users domain doesn't exist yet — so a random UUID as
`user_id` gets rejected by Postgres. Inserted one throwaway user row
directly via `psql` to unblock testing (`INSERT INTO users (email,
password_hash) VALUES (...)`), not application code — this is a real gap
that only goes away once the Users domain is built.

**Verified end-to-end:**
```
curl -X POST localhost:8080/orders -d '{"user_id":"<uuid>","total_cents":1999}'
→ 201 { "id": "...", "status": "pending", "total_cents": 1999, ... }
```
Confirmed directly in `psql` that the row landed in `orders`, **and** that
`audit_log` captured the insert automatically — the first time the audit
trigger fired from real application traffic rather than a manual `psql`
statement.

---

## Status as of this writing

Done: project skeleton, config loading, Postgres (via sqlx)/Redis
connections, `AppContext`, `ServerDependencies`/`WorkerDependencies`, the
generic worker pool + service delegator, the cobra CLI
(`server`/`worker`/`migrate`), and the full migration set (schema + outbox
+ audit triggers). Postgres and Redis are now actually running locally via
`podman-compose` (Postgres on host port `5433`, see section 14), all
migrations are applied and verified directly in `psql`, and both
`orderflow server` (`/healthz` → 200, pinging both dependencies) and
`orderflow worker` (worker pool starts, 4 goroutines) have been run
end-to-end successfully.

One real endpoint now exists: `POST /orders` (section 15) — no validation,
no outbox event, no worker involvement yet, just request → Postgres row →
response. Verified working end-to-end including the audit trigger firing
on real traffic.

Not yet done: Products/Users/Payments domains are still empty
(`contracts/`, `domains/`, `handlers/` only have Order-related files so
far; `routers/`, `views/` are still unused). No stock validation, no
outbox event written on order creation yet, and the worker pool/delegator
still have nothing registered — the worker process currently just idles.
Next up: add validation + the outbox write to `CreateOrder`, then build the
outbox dispatcher that actually gives the worker pool something to do.
