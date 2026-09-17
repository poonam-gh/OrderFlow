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

## 16. `GET /orders/:id`, and moving routing out of handlers

**Adding fetch-by-id** followed the same layered pattern as create:
- `domains/order/order.go` — added `var ErrNotFound = errors.New("order not found")`, a sentinel error the repository returns so callers can distinguish "doesn't exist" from "something broke," using `errors.Is` rather than string-matching an error message.
- `contracts/order_repository.go` — added `GetByID(ctx, id string) (*order.Order, error)` to the interface.
- `infra/postgres/order_repo.go` — `db.GetContext(ctx, &o, "SELECT * FROM orders WHERE id = $1", id)` (sqlx's struct-scanning doing its job — this is exactly why we switched to sqlx in section 10), translating `sql.ErrNoRows` into `order.ErrNotFound` right at the repository boundary, so nothing above this layer needs to know about `database/sql` error types.
- `services/order_service.go` — `GetOrder` is a thin passthrough for now.
- `handlers/order_handler.go` — `Get` checks `errors.Is(err, order.ErrNotFound)` → `404`, anything else → `500`.

**Then a refactor: routing moved out of `handlers/` into `routers/`.**
Originally `OrderHandler` had its own `Register(r *gin.Engine)` method,
called directly from `dependencies/server_dependencies.go`. That mixes two
different responsibilities in one type: *handling* a request (parsing
input, calling the service, shaping the response) and *deciding the URL
structure* (which path/method maps to which handler) — a violation of
single responsibility, and it meant the previously-unused `routers/`
folder (present since the original project skeleton, section 1) stayed
pointless.

Now:
- **`handlers/order_handler.go`** only exposes `Create` and `Get` as plain
  `gin.HandlerFunc`-compatible methods — it has no idea what path or HTTP
  method it's mounted on.
- **`routers/order_routes.go`** — `registerOrderRoutes(engine, h)` is the
  only place that knows `POST /orders` and `GET /orders/:id` map to
  `h.Create`/`h.Get`.
- **`routers/router.go`** — `Register(engine, appCtx, orderHandler)` is the
  single entry point called from `dependencies/server_dependencies.go`; it
  registers `/healthz` directly (not domain-specific, stays here) and
  delegates to `registerOrderRoutes` for anything order-related.

Why split routes into a per-resource file (`order_routes.go`) rather than
one growing `router.go`: adding Products/Users routes later means adding
`product_routes.go`/`user_routes.go` and one line in `Register` — existing
route files never need to change (open/closed). `dependencies/server_dependencies.go`
now only builds the handler and hands it to `routers.Register` — it no
longer touches `gin.Engine` route registration directly at all.

Re-verified end-to-end after the refactor: create → 200/201, fetch by real
id → 200 with the row, fetch by a nonexistent id → 404
`{"error":"order not found"}`, `/healthz` → 200.

---

## 17. Completing the project: every remaining piece, wired and tested live

Everything from here on was built in one pass to bring the project to the
full architecture described at the top of this log — Users (JWT + RBAC),
Products (stock + cache-aside), real order validation, the outbox write,
the outbox dispatcher actually driving the worker pool, the hand-rolled
circuit breaker in production use, a mock payment gateway, notifications,
Redis rate limiting, and Prometheus metrics on **both** processes. Then all
of it was actually run and exercised end-to-end against the live
Postgres/Redis — not just compiled.

### Users domain (JWT auth + RBAC)

`domains/user`, `contracts/user_repository.go`, `infra/postgres/user_repo.go`,
`services/user_service.go`, `handlers/user_handler.go`,
`routers/user_routes.go` — `POST /users/register` (bcrypt-hashes the
password, `user.ErrEmailTaken` on a duplicate email via detecting Postgres
error code `23505`) and `POST /users/login` (verifies via
`bcrypt.CompareHashAndPassword`, returns a signed HS256 JWT with the user's
id as `sub` and role as a custom claim, `config.yaml`'s `auth.jwt_secret` /
`auth.token_ttl_minutes`).

`routers/middleware/auth.go` — `AuthRequired(secret)` parses `Authorization:
Bearer <token>`, validates it, and puts `user_id`/`role` into the gin
context; `RequireRole(role)` gates admin-only routes. **`POST /orders` now
reads `user_id` from the validated JWT, not the request body** — this
closes the earlier testing gap (section 15) where any client-supplied
`user_id` was trusted; now an order can never be created for someone else,
and the FK to `users` is guaranteed satisfied by construction.

Known simplification: there's no admin-bootstrap flow — the first admin
user's role has to be promoted directly in Postgres (`UPDATE users SET
role='admin' ...`). Fine for a learning project, not something to ship.

### Products domain (stock + Redis cache-aside)

`domains/product`, `contracts/product_repository.go`,
`infra/postgres/product_repo.go`, `services/product_service.go`,
`handlers/product_handler.go`, `routers/product_routes.go` — `GET
/products/:id` is public; `POST /products` requires `AuthRequired` +
`RequireRole("admin")`.

`contracts/cache.go` defines `ProductCache` (cache-aside: a miss means "go
to Postgres," the cache is never authoritative); `infra/redis/product_cache.go`
implements it with a plain `GET`/`SET` + TTL (`product_cache.ttl_seconds`).
`ProductService.GetProduct` checks the cache first, falls back to the
repository on a miss, then populates the cache — this is exactly the
pattern described back in section 4, now real: verified live by reading
`product:<id>` straight out of Redis with `redis-cli GET` right after a
`GET /products/:id` call and seeing the exact row.

### Order validation + the outbox write, for real this time

`domains/order` grew `Item`/`ItemInput` and three new sentinel errors:
`ErrEmptyItems`, `ErrProductNotFound`, `ErrInsufficientStock`.
`contracts/order_repository.go`'s `Create` became `CreateWithItems(ctx, o,
items)`, plus `UpdateStatus`. The real logic lives in
`infra/postgres/order_repo.go`, all inside **one transaction**:

1. For each item, `SELECT price_cents, stock FROM products WHERE id = $1
   FOR UPDATE` — locks the row so concurrent orders for the same product
   can't both read stale stock and both succeed.
2. No such product → `order.ErrProductNotFound`. `stock < quantity` →
   `order.ErrInsufficientStock` (transaction rolls back via `defer
   tx.Rollback()`, which is a safe no-op after a successful `Commit`).
3. Decrement stock, accumulate `total_cents` server-side from the actual
   product prices (never trust a client-supplied total).
4. Insert the order, insert each `order_items` row, insert the
   `outbox_events` row (`event_type: "order.created"`, payload
   `{order_id, total_cents}`) — then `tx.Commit()`.

This is the outbox pattern finally made real: the order and its outbox
event either both commit or neither does. `handlers/order_handler.go` maps
`ErrProductNotFound` → 404, `ErrInsufficientStock`/`ErrEmptyItems` → 409.
Gin's built-in struct-tag validation (`binding:"required,min=1,dive"` etc.,
already part of gin via `go-playground/validator` — no new dependency)
handles shape validation (empty body, non-positive quantity) before the
service is even called.

**Verified live:** ordering more than available stock → `409`, ordering a
nonexistent product → `404`, an empty `items` array → `400` from gin's
binding — and in every failure case, `SELECT stock FROM products` showed
the stock **unchanged**, proving the transaction actually rolled back
rather than partially applying.

### The hand-rolled circuit breaker, in real use

`pkg/circuitbreaker/breaker.go` (built earlier, section — see the file
itself) is now actually wired into `services/payment_service.go`, wrapping
calls to `infra/payment/mock_gateway.go` — a fake gateway with a
configurable failure rate and latency (`payment.failure_rate`,
`payment.latency_ms`), simulating exactly the kind of flaky external
dependency a circuit breaker exists for. `PaymentService.ProcessPayment`
runs the charge through `breaker.Execute`, then **always** records the
payment attempt and updates the order status (`paid`/`payment_failed`)
regardless of whether the charge succeeded — persistence happens before
the error is returned to the caller.

**Verified live, not just unit-tested:** cranked `payment.failure_rate` to
`1.0`, restarted the worker, fired 3 orders → all 3 failed →
`circuit_breaker_state{name="payment_gateway"}` flipped from `0` to `1`
(Open) exactly on the 3rd consecutive failure (`circuit_breaker.
failure_threshold: 3`). A 4th order arrived ~26s later (past
`reset_timeout_seconds: 5`) and the breaker had already gone half-open,
let the trial through, watched it fail, and reopened — the same
`TestBreakerHalfOpenFailureReopens` behavior from the unit test, now
observed in a live process. Restored `failure_rate` to `0.3` and confirmed
the gauge dropped back to `0` (Closed) — the breaker resets its
consecutive-failure count on *any* success, so a realistic mixed
success/failure rate never trips it, only a run of consecutive failures
does.

### The outbox dispatcher — what finally gives the worker pool something to do

New package `outbox/dispatcher.go`: depends only on
`contracts.OutboxRepository` and `*worker.Pool` — same "no business
knowledge" discipline as `worker/` itself (section 6). `Run` ticks every
`outbox.poll_interval_seconds` and calls `Claim`, which lives in
`infra/postgres/outbox_repo.go`:

```sql
WITH claimed AS (
    SELECT id FROM outbox_events
    WHERE status = 'pending'
    ORDER BY created_at
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
UPDATE outbox_events SET status = 'processing'
WHERE id IN (SELECT id FROM claimed)
RETURNING id, event_type, payload
```

One statement atomically claims a batch *and* flips it to `processing` —
no transaction needs to stay open while the (potentially slow, external)
processing happens afterward. `FOR UPDATE SKIP LOCKED` means if this
project ever runs multiple worker instances, they'd claim disjoint batches
without blocking each other — which is also why the originally-planned
separate Redis distributed lock for this turned out to be unnecessary: the
SKIP LOCKED claim already solves the exact problem it would have solved,
and adding both would have been redundant complexity for no extra
correctness. (Redis still earns its keep elsewhere — rate limiting and
product caching, both genuinely using Redis's atomicity/TTL properties.)

Each claimed event becomes a `worker.Job` submitted to the pool.
`dependencies/worker_dependencies.go` registers the actual business logic
for `"order.created"` on the delegator: unmarshal the payload, call
`PaymentService.ProcessPayment`, call `NotificationService.Notify`, record
the circuit breaker's state and an outbox-processed counter as metrics,
then `MarkProcessed`/`MarkFailed` on the outbox row. `WorkerDependencies.Run`
now starts the pool, the dispatcher, *and* a metrics server (below)
concurrently via a `sync.WaitGroup`, all three shut down together on
context cancellation.

**Verified live end-to-end:** created an order → within one poll interval
the worker log showed `"notification sent"` → order status flipped to
`paid` in Postgres → a `payments` row appeared → product stock decremented
→ the `outbox_events` row showed `status: processed`. The entire chain
from section 15's original diagram, actually working, for the first time.

### A real bug found while testing: metrics split across two processes

`/metrics` was registered on the **server**'s gin engine, but
`circuit_breaker_state` and `outbox_events_processed_total` are only ever
updated inside the **worker** process — and Prometheus's default registry
is in-memory and per-process. Two separate `go run` processes have two
separate memories, so the worker's metrics were completely invisible on
the server's `/metrics`, silently. Caught by actually curling `/metrics`
after a processed order and finding the counters simply absent rather than
zero.

**Fix:** the worker now runs its own tiny `/metrics` HTTP server
(`metrics.worker_port`, default `9091`) via `promhttp.Handler()`, started
and shut down alongside the pool and dispatcher in
`WorkerDependencies.Run`. This is also just... correct architecture: a
horizontally-scaled worker fleet needs each instance scraped
independently anyway, the same way a real deployment would point
Prometheus at multiple worker pods, not just the API server.

### Redis rate limiting

`infra/redis/ratelimiter.go` — a fixed-window counter (`INCR` + `EXPIRE`
on first hit), atomic by construction since Redis executes `INCR` on its
single command thread — no separate GET-then-SET race is possible the way
there would be in application code. `routers/middleware/ratelimit.go`
applies it globally (`engine.Use(...)`, keyed by client IP) — fails *open*
on a Redis error (an infra hiccup shouldn't take the whole API down).

**Verified live:** looped `/healthz` past the configured limit (`rate_limit.
requests: 20` per `rate_limit.window_seconds: 60`) and got `429`s exactly
as expected — and confirmed the limit is global per IP across *every*
route, not per-endpoint, since it's one `engine.Use()` middleware, not a
per-route one. A simplification worth naming: this is a fixed window, not
a sliding window — simpler and still correct/atomic, but it allows a burst
of up to 2x the limit right at a window boundary. A sliding-window
sorted-set implementation would close that gap; not worth the extra
complexity here.

### Prometheus metrics

`pkg/metrics/metrics.go` — `http_request_duration_seconds` (histogram, by
method/path/status), `circuit_breaker_state` (gauge), `outbox_events_
processed_total` (counter, by event type and result).
`routers/middleware/metrics.go` records every HTTP request's duration on
the server; the worker records the other two (see above). Verified live:
`/metrics` on the server showed a distinct counter per route+status
combination actually hit during testing (`POST /orders` with `201`, `404`,
`409`, `400` all present as separate series).

### Verification summary (all run against the live containers, not mocked)

- Register → duplicate email → 409. Login → wrong password → 401.
- Customer tries `POST /products` → 403. No token → 401. Admin → 201.
- `GET /products/:id` twice → second read confirmed served from Redis
  (`redis-cli GET product:<id>` returned the exact cached row).
- Full order → outbox → worker → payment → notification chain, confirmed
  in Postgres at every step (order status, payment row, stock, outbox row).
- Insufficient stock / missing product / empty items → 409/404/400, stock
  provably unchanged after each rejected attempt.
- Circuit breaker: forced open at 3 consecutive failures, watched a
  half-open trial fail and reopen it, watched it return to closed once
  failures stopped being consecutive — all via the live `/metrics` gauge.
- Rate limiter: tripped at the configured threshold, globally per IP.
- `audit_log` row counts confirmed independent triggers fired on `orders`,
  `products`, and `payments` throughout the entire session (11 inserts + 8
  updates on `orders`, 8 inserts on `payments`, 1 insert + 8 updates on
  `products` — matching every write made during testing).
- `go build ./...`, `go vet ./...`, `gofmt -l .`, and `go test ./...`
  (circuit breaker unit tests) all clean at the end.

---

## 18. Closing the known gaps: ownership, request tracing, lists, idempotency

After section 17 the project was feature-complete but had a few named
gaps. This pass closed three of them (ownership, request tracing, list
endpoints + idempotency) and deliberately left the rest (Dockerfile/CI)
for later.

### Order ownership check

`domains/order` gained `ErrForbidden`. `OrderService.GetOrder(ctx, id,
requesterID string, isAdmin bool)` now fetches the order, then returns
`ErrForbidden` unless `isAdmin` or `o.UserID == requesterID`.
`handlers/order_handler.go`'s `Get` reads `user_id`/`role` from the gin
context (set by `AuthRequired`) and maps `ErrForbidden` → `403`. Admins
bypass the check entirely — a real support/ops use case (looking up a
customer's order), not just a technicality.

**Verified live:** a second registered user got `403 {"error":"order does
not belong to this user"}` fetching the first user's order; the owner got
`200`; the admin token got `200` for the same order.

### Request-id / correlation-id, propagated across processes

New `pkg/requestid` (generate/attach/read an ID on a `context.Context`)
and `pkg/logger` (the JSON `slog` constructor, now used by `AppContext`
instead of being inlined there, plus `logger.Ctx(ctx, base)` which adds a
`request_id` attribute to log lines if the context carries one).
`routers/middleware/requestid.go` reads an incoming `X-Request-ID` header
or generates one, puts it on the request's `context.Context` (so it flows
through handler → service → repository automatically — no signature
changes needed anywhere in that chain), and echoes it on the response.
Registered first in the middleware chain, before metrics/rate-limit.

The interesting part is making this survive the jump from the **server**
process to the **worker** process, which don't share memory or a request:
`infra/postgres/order_repo.go`'s `CreateWithItems` reads the request id off
`ctx` and includes it in the `"order.created"` outbox payload
(`{order_id, total_cents, request_id}`). `dependencies/worker_dependencies.go`'s
`handleOrderCreated` reads it back out of the payload and re-attaches it to
the job's `context.Context` via `requestid.WithID`, so every log line from
that point on (`PaymentService`, `NotificationService`) carries the same
`request_id` the original HTTP client saw in its response header.

**Verified live:** `curl -D -` on `/healthz` showed a generated
`X-Request-Id` header; sending one explicitly (`X-Request-ID:
my-custom-id-123`) got it echoed back unchanged; creating an order and
then grepping the worker's log for `request_id` showed the notification
log line carrying that same value. One request, traced across two OS
processes.

Named gap: gin's own built-in access-log line (`[GIN] ... 200 ... /orders`)
doesn't include the request id inline — only the explicit `slog` lines do.
Good enough to trace a specific request through business logic; not yet a
fully unified access log.

### List endpoints

`contracts.OrderRepository` gained `ListByUser(ctx, userID, limit,
offset)`; `contracts.ProductRepository` gained `List(ctx, limit, offset)`.
`GET /orders` (authenticated — lists **the caller's own** orders only,
consistent with the ownership work above, not a global list) and `GET
/products` (public) both take `?limit=&offset=` query params, clamped in
`handlers.paginationParams` (default 20, max 100, offset ≥ 0) — a shared
helper function in the `handlers` package used by both handlers, no need
to duplicate it per-domain.

### Idempotency-Key on `POST /orders`

`contracts.IdempotencyStore` (interface) + `infra/redis/idempotency.go`
(implementation): `Claim` atomically reserves a client-supplied
`Idempotency-Key` via Redis `SETNX` — the same atomicity property that
made the rate limiter and distributed-lock discussions correct earlier
(section 4) — storing a `"processing"` placeholder. If the key is new,
the caller proceeds to create the order normally, then calls `Resolve` to
overwrite the placeholder with the real order id. If the key already
exists: still `"processing"` → `409` ("already in progress"); already
resolved to an order id → that order is fetched and replayed as `200`
instead of creating a duplicate.

`OrderHandler.Create` wires this in via `replayIfClaimed`, which returns
`done=true` once it has fully handled the response itself (either a
replay or a conflict), so `Create` just returns early rather than falling
through to actually create anything. A Redis error during the check fails
*open* — proceeds as a non-idempotent request rather than blocking order
creation over an infra hiccup.

**Verified live:** two identical `POST /orders` calls with the same
`Idempotency-Key` — first returned `201` with a new order, second returned
`200` with the **exact same order id**, and — the real proof — product
stock was decremented exactly once, not twice.

---

---

## 19. Unit tests for the services layer: gomock + table-driven tests

The biggest named gap after section 18 was "no integration test suite —
only the circuit breaker has unit tests." This closed the unit-test half
of that (the services layer, where all the interesting business logic
lives) using generated mocks rather than hand-written fakes.

### Why generated mocks, and why `go.uber.org/mock`

Every service in this codebase already depends only on interfaces from
`contracts/` (`OrderRepository`, `ProductRepository`, `UserRepository`,
`PaymentGateway`, `PaymentRepository`, `ProductCache`) — that's the whole
point of the hexagonal split from section 1. That makes them a natural fit
for mocking: a test can swap in a fake `OrderRepository` and assert
exactly how the service calls it, without touching Postgres at all.
`go.uber.org/mock` is the actively maintained fork of the original
`golang/mock` (which is archived) — it was already sitting in `go.mod` as
an indirect dependency, so no new dependency was actually introduced, just
promoted to direct use.

Its `mockgen` tool generates a `MockX` type per interface directly from
the interface definition — no hand-written fakes to keep in sync by hand.
Installed once:
```
go install go.uber.org/mock/mockgen@v0.6.0
```
Every `contracts/*.go` file now starts with a `//go:generate` directive,
e.g.:
```go
//go:generate mockgen -source=order_repository.go -destination=mocks/mock_order_repository.go -package=mocks
package contracts
```
so `go generate ./...` regenerates every mock in `contracts/mocks/` after
any interface changes — the mocks are never edited by hand (`// Code
generated by MockGen. DO NOT EDIT.` at the top of each file is the real
rule, not just a comment).

### The table-driven test files

Four new files, one per service, each following the same shape: a
`[]struct{ name string; ...inputs...; ...expected... }` table, looped with
`t.Run(tt.name, func(t *testing.T) {...})` so `go test -v` prints each
case individually and `go test -run TestX/case_name` can target one case.

- **`services/order_service_test.go`** — `TestOrderService_GetOrder` is
  the most valuable one: a 4-row table exercising the entire ownership
  matrix from section 18 (owner ✓, admin bypass ✓, non-owner ✗ →
  `ErrForbidden`, repository error passes through unchanged) against a
  single mocked `OrderRepository`, no real database involved. Plus
  `CreateOrder` and `UpdateStatus` tables covering the pass-through-error
  behavior for `ErrInsufficientStock`/`ErrProductNotFound`.
- **`services/product_service_test.go`** — table with `cacheHit: true/false`.
  The cache-hit case deliberately sets **no expectation at all** on the
  mocked `ProductRepository` — gomock fails the test on any unexpected
  call, so this is a real enforcement that a cache hit skips the
  repository, not just an assertion after the fact.
- **`services/user_service_test.go`** — `Register` table covers success and
  `ErrEmailTaken` passthrough, and asserts the stored password is actually
  hashed (`PasswordHash != "password123"`). `Login` pre-hashes a password
  with `bcrypt.GenerateFromPassword(..., bcrypt.MinCost)` (cost 4, not the
  production default — keeps the test suite fast) and covers correct
  password / wrong password / unknown email, all collapsing to the same
  `user.ErrBadCreds` (so a caller — and an attacker — can't distinguish
  "wrong password" from "no such account" from the error alone).
- **`services/payment_service_test.go`** — the most interesting one.
  `TestPaymentService_ProcessPayment` covers success and failure with a
  real `circuitbreaker.Breaker` (cheap, deterministic, no need to mock the
  breaker itself — only its dependencies, the gateway and the two
  repositories, are mocked). `TestPaymentService_ProcessPayment_
  OpenBreakerSkipsGateway` is the one genuinely proving the circuit
  breaker integration works, not just the state machine in isolation
  (already covered in `pkg/circuitbreaker/breaker_test.go`): it sets
  `gateway.EXPECT().Charge(...).Times(1)` — exactly once — then calls
  `ProcessPayment` **twice**. If the breaker didn't actually short-circuit
  the second call, gomock would fail the test for an unexpected extra
  call. This is the same "cache hit skips repository" trick applied to
  circuit breaking.

**Verified:** `go test ./... -v` — 7 top-level test functions, ~20 total
table rows/subtests, all passing; `go build`, `go vet`, `gofmt -l .` all
still clean.

---

## Status as of this writing

The project is feature-complete against the architecture described at the
top of this log, hardened per section 18 (ownership, request tracing,
lists, idempotency), and now has real unit test coverage on the services
layer via generated gomock mocks and table-driven tests (section 19):
Users (JWT + RBAC), Products (stock + cache-aside), Orders (validated,
transactional, outbox-backed, ownership-checked, idempotent), a
hand-rolled circuit breaker protecting a mock payment gateway, a worker
pool driven by a real outbox dispatcher, notifications, Redis-backed rate
limiting, Prometheus metrics on both the server and worker processes, and
request-id tracing that survives the jump from the API process to the
worker process. Every piece has been exercised live against the actual
running Postgres/Redis containers, not just compiled — see sections 17–18
for that verification trail, and section 19 for the unit test suite.

Known gaps, all deliberate and named rather than accidental: no
admin-bootstrap flow (role promotion is a manual `psql` update), no
integration test suite against a real Postgres/Redis (only unit tests with
mocked dependencies — the live verification in sections 17–18 was manual,
via `curl`/`psql`, and doesn't run as part of `go test`), no tests yet for
`infra/postgres/*` (the actual SQL), `handlers/*`, or `cmd/*`, rate
limiting is fixed-window rather than sliding-window, no retry/dead-letter
path for a failed outbox event (`MarkFailed` is terminal), gin's own
access-log line doesn't carry the request id inline, and no
`Dockerfile`/CI workflow for the app itself yet (deliberately deferred,
not forgotten). `views/` remains unused — every domain's JSON response is
still its DB-row struct directly; introducing a separate response DTO
layer would be the next cleanup if the API's public shape ever needs to
diverge from its storage shape.
