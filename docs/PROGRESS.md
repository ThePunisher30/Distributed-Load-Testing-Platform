# Progress Log

This file tracks what has been built so far, why each step exists, and how we verified it. It is meant to be a living project diary.

## Current Phase

We are working on:

```text
Phase 1: Single-Worker MVP
```

Phase 1 goal:

```text
Create test run -> worker claims it -> custom runner sends traffic -> backend stores results
```

## Step 1: Project Scaffold

Status:

```text
Completed
```

What we created:

```text
backend/
  cmd/api/
  internal/db/
  internal/handlers/
  internal/models/
  internal/store/

worker/
  cmd/worker/
  internal/runner/

target/
  cmd/target/

migrations/
  001_create_test_runs.sql

docker-compose.yml
```

Why this matters:

- `backend` will be the control plane API.
- `worker` will execute load tests.
- `target` is the safe local application we test against.
- `migrations` will contain database schema changes.
- `docker-compose.yml` will eventually run the local multi-service system.

Learning point:

```text
Good distributed systems start with clear service boundaries.
```

## Step 2: Go Modules

Status:

```text
Completed
```

What was done:

Each service was initialized as its own Go module:

```text
backend/go.mod
worker/go.mod
target/go.mod
```

Commands used:

```powershell
cd "C:\Users\Aniruddh\Documents\Distributed Load Testing Platform"

cd backend
go mod init distributed-load-testing-platform/backend

cd ..\worker
go mod init distributed-load-testing-platform/worker

cd ..\target
go mod init distributed-load-testing-platform/target
```

Why this matters:

Each service can be built, tested, and eventually deployed independently.

Learning point:

```text
Independent services should have independent build boundaries.
```

## Step 3: Target Service

Status:

```text
Completed
```

File created:

```text
target/cmd/target/main.go
```

What it does:

The target service is a small local HTTP app that runs on port `8081`.

It exposes:

```text
GET /health
GET /fast
GET /slow
GET /error
GET /random
```

Endpoint behavior:

```text
/health -> returns service health
/fast   -> returns 200 immediately
/slow   -> waits 500ms, then returns 200
/error  -> returns 500 intentionally
/random -> randomly behaves like fast, slow, or error
```

Why this matters:

Before we build a load runner, we need a safe and predictable system to test against. This avoids sending traffic to public websites or production systems.

Learning point:

```text
A load testing platform needs a controlled system under test while the platform itself is being built.
```

Commands used to run it:

```powershell
cd "C:\Users\Aniruddh\Documents\Distributed Load Testing Platform\target"
gofmt -w .\cmd\target\main.go
go test ./...
go run ./cmd/target
```

Commands used to verify it from another terminal:

```powershell
curl http://localhost:8081/health
curl http://localhost:8081/fast
curl http://localhost:8081/slow
curl http://localhost:8081/error
curl http://localhost:8081/random
```

Observed results:

```text
/health returned 200 OK with service status.
/fast returned 200 OK immediately.
/slow returned 200 OK after a short delay.
/error returned 500 with an intentional error response.
/random returned mixed behavior.
```

PowerShell note:

In PowerShell, `curl` is an alias for `Invoke-WebRequest`. It reports HTTP `500` responses as command errors. That is expected for `/error` because the service intentionally returns status code `500`.

Cleaner alternatives:

```powershell
curl.exe http://localhost:8081/error
```

or, in newer PowerShell versions:

```powershell
Invoke-WebRequest http://localhost:8081/error -SkipHttpErrorCheck
```

## Step 4: PostgreSQL with Docker Compose

Status:

```text
Completed
```

Files created:

```text
docker-compose.yml
migrations/001_create_test_runs.sql
```

What was done:

Defined a `postgres:16-alpine` service in `docker-compose.yml`:

- Database `dltp`, user `dltp`, password `dltp`.
- Host port `5432` mapped so local Go services and psql can connect.
- Named volume `postgres_data` for durable storage across restarts.
- `./migrations` mounted read-only into `/docker-entrypoint-initdb.d`, so any
  `.sql` file runs automatically the first time the data volume is created.
- A `pg_isready` healthcheck so dependents can wait for readiness.

Wrote `migrations/001_create_test_runs.sql` defining the central `test_runs`
table. It holds both the test configuration (name, target URL, method, virtual
users, duration) and the aggregated results (request counts, latency stats),
plus a `status` lifecycle column (`queued`, `running`, `completed`, `failed`)
and lifecycle timestamps. Includes CHECK constraints and an index on
`(status, created_at)` for the worker's "claim oldest queued run" query.

Why this matters:

The database is the shared source of truth for the whole lifecycle. The backend
writes queued runs, the worker claims and updates them, and results are read
back through the API. Bootstrapping the schema on first startup keeps local
setup a single command.

Learning point:

```text
Auto-running migrations from docker-entrypoint-initdb.d only fires on an EMPTY
data volume. To re-apply a changed schema locally, remove the volume
(docker compose down -v) or run the migration manually.
```

Commands used:

```powershell
docker compose up -d
docker compose ps
docker exec dltp-postgres psql -U dltp -d dltp -c "\d test_runs"
```

Verification:

```text
Container dltp-postgres reported STATUS: Up (healthy).
psql \d test_runs showed all columns, CHECK constraints, and the
idx_test_runs_status_created_at index, confirming the migration ran on init.
```

PowerShell note:

The `docker` CLI was not on PATH. It lives at:

```text
C:\Program Files\Docker\Docker\resources\bin\docker.exe
```

For a session, prepend it:

```powershell
$env:Path = "C:\Program Files\Docker\Docker\resources\bin;" + $env:Path
```

## Step 5: Backend Health Endpoint and Database Connection

Status:

```text
Completed
```

Files created:

```text
backend/cmd/api/main.go
backend/internal/db/db.go
backend/internal/handlers/health.go
```

Dependency added:

```text
github.com/jackc/pgx/v5 (used via the pgx stdlib driver)
```

What was done:

- `internal/db/db.go` opens a PostgreSQL connection pool using the standard
  `database/sql` interface with pgx as the driver. `sql.Open` is lazy, so we
  call `PingContext` (with a 5s timeout) to force a real connection at startup
  and fail fast if the database is down. Pool limits are set for local dev.
- `internal/handlers/health.go` defines a `Handler` struct that carries the
  `*sql.DB` (future endpoints hang off this same struct). Its `/health` handler
  pings the database and reports `status: ok` + `database: ok` on success, or
  `503` + `database: unreachable` if the ping fails. A liveness check that
  ignored the DB would lie, so it checks the real dependency.
- `cmd/api/main.go` wires it together: reads `DATABASE_URL` and `BACKEND_ADDR`
  from the environment (with localhost defaults), connects to the DB, serves the
  routes, and shuts down gracefully on Ctrl-C / SIGTERM.

Why this matters:

This is the first service that actually talks to the database. It proves the
control plane can reach its source of truth before we build endpoints that
read and write `test_runs`.

Learning point:

```text
database/sql.Open does not connect; it only prepares a lazy pool. Always Ping
once at startup so a bad connection fails immediately instead of on the first
query. A health check should verify real dependencies, not just report "up".
```

Configuration:

```text
DATABASE_URL  default: postgres://dltp:dltp@localhost:5432/dltp?sslmode=disable
BACKEND_ADDR  default: :8080
```

Commands used:

```powershell
cd backend
go get github.com/jackc/pgx/v5/stdlib
go mod tidy
gofmt -w .\cmd\api\main.go .\internal\db\db.go .\internal\handlers\health.go
go vet ./...
go build ./...
go run ./cmd/api
```

Verification:

```powershell
Invoke-WebRequest http://localhost:8080/health -UseBasicParsing
```

Observed result:

```text
Startup logs: "connected to database" then "backend API listening on :8080".
GET /health returned HTTP 200 with:
{"database":"ok","service":"backend","status":"ok"}
```

## Step 6: Public API for Creating and Reading Test Runs

Status:

```text
Completed
```

Files created:

```text
backend/internal/models/test_run.go
backend/internal/store/test_run_store.go
backend/internal/handlers/response.go
backend/internal/handlers/test_runs.go
```

Files changed:

```text
backend/internal/handlers/health.go  (Handler now holds a TestRunStore; routes
                                       registered with method+path patterns;
                                       writeJSON moved to response.go)
```

What was done:

Introduced a clean three-layer split so HTTP, business rules, and SQL stay
separate:

- `models/test_run.go` defines `TestRun` (mirrors the table; nullable result
  fields are pointers so they are null until a run finishes) and
  `CreateTestRunRequest` (the fields a user supplies).
- `store/test_run_store.go` owns the SQL. `Create` uses INSERT ... RETURNING to
  insert and read back the full row in one round trip; `GetByID` returns a
  sentinel `ErrNotFound` on a missing row. A shared column list and scan helper
  keep the two queries in sync.
- `handlers/test_runs.go` adds `POST /test-runs` and `GET /test-runs/{id}`,
  with validation: required name and targetUrl, http/https URL, virtualUsers and
  durationSeconds within sane bounds, and a defaulted/allowlisted HTTP method.
  Unknown JSON fields are rejected to catch typos early.
- `handlers/response.go` centralizes `writeJSON` and adds `writeError` for a
  consistent `{"error": "..."}` shape.

Routing uses Go 1.22+ ServeMux method+path patterns (`POST /test-runs`) and the
`{id}` wildcard read via `r.PathValue("id")`.

Why this matters:

This is the first time a user action creates real state. A queued row in
`test_runs` is exactly what the worker will look for in the next steps.

Learning point:

```text
Separate HTTP handlers (validation, status codes), the store (SQL), and models
(data shape). Handlers should never write SQL, and the store should never know
about HTTP. INSERT ... RETURNING avoids a second SELECT after an insert.
```

Commands used:

```powershell
cd backend
gofmt -w .\internal\handlers\ .\internal\models\ .\internal\store\
go vet ./...
go build -o $env:TEMP\dltp-api.exe ./cmd/api
```

Verification (server running, requests via Invoke-WebRequest):

```text
POST /test-runs (valid)            -> 201, id=1, status=queued, createdAt set,
                                      result fields omitted (still null)
GET  /test-runs/1                  -> 200, same record read back from Postgres
POST /test-runs (virtualUsers=0)   -> 400
POST /test-runs (targetUrl ftp://) -> 400
POST /test-runs (unknown field)    -> 400
GET  /test-runs/999                -> 404
GET  /test-runs/abc                -> 400
```

## Step 7: Internal API for Workers to Claim and Complete Runs

Status:

```text
Completed
```

Files created:

```text
backend/internal/handlers/internal_api.go
```

Files changed:

```text
backend/internal/models/test_run.go   (added CompleteTestRunRequest)
backend/internal/store/test_run_store.go (added ClaimNext, Complete, and the
                                          ErrNoQueuedRuns / ErrNotRunning sentinels)
backend/internal/handlers/health.go    (registered the two /internal routes)
```

Endpoints added:

```text
POST /internal/workers/claim              -> claim the oldest queued run
POST /internal/test-runs/{id}/complete    -> report a run's outcome
```

These live under /internal to mark them worker-facing; Phase 8 adds auth.

What was done:

- `ClaimNext` atomically claims the oldest queued run with the canonical
  database-job-queue pattern:
  `UPDATE ... WHERE id = (SELECT id ... WHERE status='queued' ORDER BY created_at
  FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING ...`. It flips the row to running and
  stamps started_at. Empty queue returns ErrNoQueuedRuns -> HTTP 204.
- `Complete` writes the outcome guarded by `WHERE id=$1 AND status='running'`, so
  only a claimed, in-progress run can be completed. Zero rows updated triggers a
  follow-up lookup to distinguish 404 (ErrNotFound) from 409 (ErrNotRunning).
- Handler validation: a "completed" run must include the three request counts; a
  "failed" run must include a non-empty errorMessage; any other status is 400.

Why this matters:

This is the hand-off channel between backend and worker. With it, the ENTIRE
test-run lifecycle is now exercisable over HTTP (done here by hand); Steps 8-10
just automate the worker side of these same two calls.

Learning point:

```text
FOR UPDATE SKIP LOCKED is the key to a safe DB job queue: FOR UPDATE locks the
claimed row, SKIP LOCKED lets a concurrent worker take the next row instead of
blocking, and doing select+update in one statement makes claiming atomic. Writing
it correctly with one worker means multi-worker (Phase 3) needs no query change.
A state-guarded UPDATE (WHERE status='running') prevents illegal transitions.
```

Verification (table reset first for a clean demo, server via Invoke-WebRequest):

```text
Seed two queued runs (id=1, id=2).
CLAIM                  -> 200, id=1 running, startedAt set
COMPLETE id=1 success  -> 200, completed, results + completedAt set
GET id=1               -> 200, final completed record
CLAIM                  -> 200, id=2 running (FIFO order)
COMPLETE id=2 failed   -> 200, failed + errorMessage
CLAIM (queue empty)    -> 204 No Content
COMPLETE id=1 again    -> 409 Conflict (not running)
COMPLETE id=999        -> 404 Not Found
COMPLETE bad status    -> 400 Bad Request
```

## Step 8: Worker Polling Loop

Status:

```text
Completed
```

Files created:

```text
worker/internal/client/client.go
worker/cmd/worker/main.go
```

What was done:

- `internal/client/client.go` is the worker's HTTP client for the backend's
  internal API. `ClaimNext` POSTs to /internal/workers/claim and returns
  (run, claimed, err); a 204 becomes claimed=false (normal idle), a 200 decodes
  the run. `Complete` POSTs the outcome to /internal/test-runs/{id}/complete. The
  worker is its own module, so the client defines only the TestRun/CompleteRequest
  fields it needs (a subset of the backend model), matching the API's JSON names.
- `cmd/worker/main.go` runs the poll loop: on a ticker (POLL_INTERVAL, default
  2s) it calls ClaimNext. On an empty queue it waits for the next tick; after
  finishing a run it loops immediately (no idle wait when there is more work).
  It shuts down cleanly on Ctrl-C / SIGTERM via a signal-cancelled context.

Placeholder note:

```text
executeRun() in main.go is a STUB for this step: it sleeps briefly and returns
fabricated results (totalRequests = virtualUsers * 100). Step 9 replaces it with
the real concurrent runner. The claim -> execute -> complete seam is already in
place, so Step 9 only swaps the body of executeRun.
```

Why this matters:

This is the first time the system advances with no human in the loop. A run
created through the public API is now picked up and finished automatically.

Learning point:

```text
A polling worker is the simplest form of job distribution: ask, do, repeat.
Poll interval trades latency (how fast work is picked up) against load (how often
we hit the backend when idle). Looping immediately after a successful claim keeps
throughput high while a slow tick keeps idle cost low. Phase 2 replaces polling
with a message broker (push instead of pull).
```

Configuration:

```text
BACKEND_URL    default: http://localhost:8080
POLL_INTERVAL  default: 2s
```

Commands used:

```powershell
cd worker
gofmt -w .\cmd\ .\internal\
go vet ./...
go build -o $env:TEMP\dltp-worker.exe ./cmd/worker
```

Verification (backend + worker running together):

```text
Reset table, then POST two runs (id=1 fast, id=2 slow) via the public API.
Worker log:
  worker started; polling http://localhost:8080 every 2s
  claimed run 1 ("auto run A") ... ; run 1 reported as completed
  claimed run 2 ("auto run B") ... ; run 2 reported as completed
GET /test-runs/1 -> completed, totalRequests 1000 (10 VUs * 100), startedAt and
                    completedAt set.
GET /test-runs/2 -> completed, totalRequests 300 (3 VUs * 100).
No manual claim/complete calls were made: the worker drove the lifecycle.
```

## Current System State

Working:

```text
Target service
PostgreSQL (running in Docker, schema bootstrapped)
Backend API (public + internal endpoints)
Worker (polls, claims, completes runs automatically)
End-to-end lifecycle runs on its own -- but load results are FAKE (placeholder)
```

Not built yet:

```text
Custom runner (real load generation)
Real result aggregation
```

## Next Step

Next planned step:

```text
Step 9: Custom closed-loop virtual-user runner
```

What Step 9 will achieve:

```text
Replace the placeholder executeRun with a real load runner: spin up N virtual
users as goroutines that send HTTP requests to the target for the configured
duration, with per-request timeouts and clean cancellation. This is the core
concurrency step.
```

