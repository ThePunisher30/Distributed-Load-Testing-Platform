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

## Step 9: Custom Closed-Loop Virtual-User Runner

Status:

```text
Completed
```

Files created:

```text
worker/internal/runner/runner.go     Config, Result, Run, runVirtualUser,
                                      doRequest, newHTTPClient
worker/internal/runner/stats.go       vuStats, record, mergeStats
worker/internal/runner/stats_test.go  unit tests for the aggregation
```

Files changed:

```text
worker/cmd/worker/main.go  (executeRun now calls the runner instead of
                            returning fabricated numbers)
```

What was done:

Replaced the placeholder `executeRun` with a real load engine. The runner is a
separate, self-contained package that takes a `Config` (target URL, method,
virtual users, duration, per-request timeout) and returns an aggregated
`Result` (request counts and avg/min/max latency). It deliberately knows nothing
about the backend, so it can be unit tested on its own.

The load model is closed-loop, one goroutine per virtual user:

```text
Run
  -> context.WithTimeout(ctx, Duration)   (stops all VUs when time is up
                                           OR the worker is cancelled)
  -> make([]vuStats, VirtualUsers)        (one private tally per VU)
  -> spawn VirtualUsers goroutines via a sync.WaitGroup; VU i writes ONLY
     to stats[i], so no locks are needed
  -> each VU loops: send request, measure latency, record, repeat until ctx done
  -> wg.Wait(), then mergeStats(stats) -> Result
```

Design points:

- Each virtual user owns its own `vuStats`; results are merged only after all
  goroutines finish. This is aggregation without shared mutation, so the design
  is race-free by construction (proven with `go test -race`).
- A single tuned `http.Client` is shared by all VUs. Go's default transport caps
  idle connections per host at 2, which would throttle the generator and make us
  measure our own connection churn; we raise the limits to the VU count.
- Each request gets its own timeout derived from the run context, so a slow
  request is cut off by whichever fires first: its timeout, the run duration, or
  worker shutdown.
- Success rule: HTTP status < 400 is a success; a transport error (no response)
  or status >= 400 is a failure. Latency is recorded for any response (a 500 has
  a real latency); transport errors contribute no sample.

Learning point:

```text
Give each goroutine its own memory to write, then combine after WaitGroup.Wait,
and you get correct aggregation with no mutex and no data race. FOR the load
client, one shared connection-pooled http.Client is essential -- the default
transport's per-host idle cap of 2 would otherwise bottleneck the whole test.
```

Windows dev caveat:

```text
On Windows the Go monotonic clock is coarse: fast localhost requests can measure
0 ns, so minLatencyMs often reads 0 locally. A raw probe confirmed ~48% of
requests measured exactly 0 ns OUTSIDE the runner, so this is a platform clock
limitation, not a bug. On Linux (where the worker runs in its container) the
resolution is nanoseconds and min becomes a real value.
```

Commands used:

```powershell
cd worker
gofmt -w .\internal\runner\ .\cmd\worker\main.go
go vet ./...
go build ./...
go test -race ./internal/runner/
```

Verification:

Unit tests (`go test -race ./internal/runner/`):

```text
TestMergeStats_Mix          avg 16.25, min 5, max 30 from a hand-built mix
TestMergeStats_ZeroSampleVU min stays 5 (a no-sample VU does not drag it to 0)
TestMergeStats_AllFailures  no divide-by-zero when nothing got a response
All PASS under the race detector.
```

Full end-to-end lifecycle (all services running: Postgres, target, backend,
worker):

```text
POST /test-runs  {20 virtual users, 5s, http://localhost:8081/fast}  -> id=5 queued
worker log: claimed run 5 ... ; run 5 reported as completed
GET /test-runs/5 -> completed with REAL results:
  totalRequests      204900   (~41k req/s)
  successfulRequests 204897
  failedRequests     3        (requests cut off at the 5s deadline)
  avgLatencyMs       0.465
  maxLatencyMs       34.13
  minLatencyMs       0        (Windows clock artifact, see caveat above)
  startedAt -> completedAt spans exactly the 5s run duration
```

## Step 10: Full Docker Compose (Phase 1 complete)

Status:

```text
Completed
```

Files created:

```text
backend/Dockerfile
worker/Dockerfile
target/Dockerfile
```

Files changed:

```text
docker-compose.yml  (added backend, worker, target services alongside postgres)
```

What was done:

Containerized all three services so the entire platform comes up with a single
command. Each Dockerfile is a multi-stage build: a golang:1.26 stage compiles a
static binary (CGO_ENABLED=0), then a minimal alpine stage runs it as a non-root
user. docker-compose.yml now defines four services on one network:

```text
docker compose up --build
  postgres  ->  backend  ->  worker
                   \-> target (independent; the worker sends load to it)
```

Design points:

- Services reach each other by SERVICE NAME on the compose network, not
  localhost. The backend connects to host "postgres"; the worker calls
  "http://backend:8080"; a test run targets "http://target:8081/fast". This is
  service discovery, the same idea Kubernetes uses.
- Startup order is enforced with depends_on + healthchecks: backend waits for
  postgres to be healthy, worker waits for backend to be healthy. The backend's
  healthcheck hits /health, which pings the DB, so "healthy" also means the DB is
  reachable.
- Env vars override the localhost defaults baked into each binary
  (DATABASE_URL, BACKEND_URL), so the same code runs on the host or in Compose.

Learning point:

```text
Inside Docker each container is its own host, so "localhost" no longer finds the
other services -- they are addressed by service name over the compose network.
depends_on + healthchecks replace "start them in the right order by hand".
```

Commands used:

```powershell
docker compose config --quiet
docker compose up --build -d
docker compose ps
docker compose logs worker
```

Verification (all four containers up, run created from the host):

```text
docker compose ps -> postgres, backend, target all healthy; worker up
POST http://localhost:8080/test-runs
  {15 VUs, 4s, targetUrl "http://target:8081/fast"}   -> id=6 queued
worker log: claimed run 6 ... http://target:8081/fast ; reported as completed
GET /test-runs/6 -> completed with REAL results:
  totalRequests      80518
  successfulRequests 80503
  failedRequests     15
  avgLatencyMs       0.725
  maxLatencyMs       13.44
  minLatencyMs       0.051   (a REAL value now -- worker runs on Linux in its
                              container, so the clock is not the coarse Windows one)
```

## Testing: a safety net before Phase 2

Status:

```text
Completed
```

Added the first automated tests (before this, everything was verified by hand):

- `worker/internal/runner/run_test.go`: drives the real `Run` (goroutines + HTTP)
  against an in-process httptest server. Covers a 200 target (counts consistent,
  min<=avg<=max), an all-500 target (zero successes), and bad-config errors. Runs
  under `-race`. No Docker needed.
- `backend/internal/store/test_run_store_test.go`: real SQL against Postgres.
  Covers Create/GetByID, ClaimNext (SKIP LOCKED, FIFO), StartByID, TakeOver, Fail,
  and Complete -- including the state-guard errors (ErrNotQueued, ErrNotRunning,
  ErrAlreadyDone) that make duplicate delivery safe. Reads TEST_DATABASE_URL and
  skips if unset, so `go test ./...` stays green without a DB.

How to run:

```powershell
cd worker; go test -race ./internal/runner/
docker compose up -d postgres
$env:TEST_DATABASE_URL = "postgres://dltp:dltp@localhost:5432/dltp_test?sslmode=disable"
cd backend; go test ./internal/store/
```

## Phase 2: Queue-Based Job Distribution

The worker no longer polls. Jobs flow through a Redis Streams broker. New service:
`redis:7-alpine`. Stream `testruns`, consumer group `workers`, dead stream
`testruns:dead`.

### Step 11: publish + consume via a consumer group

Backend publishes a job (the run id) to the stream on create (XADD), alongside the
Postgres INSERT. The worker consumes via a consumer group (XREADGROUP), starts the
specific run (POST /internal/test-runs/{id}/start, a state-guarded queued->running
that is idempotent -- a duplicate delivery of an already-started run gets 409 and
is skipped), executes, completes, then XACK.

```text
Verified: a run is picked up ~36ms after creation (vs up to 2s with polling),
completes with real results, and leaves zero pending messages after ack.
Learning: push (broker) vs pull (polling); consumer groups; acknowledgements.
```

### Step 12: reclaim orphaned jobs (visibility timeout)

Each loop the worker reclaims messages idle longer than RECLAIM_MIN_IDLE -- the
signal that the worker holding them died -- and takes them over via
POST .../takeover (store.TakeOver accepts a run that is already 'running', so a
worker that crashed mid-execution can be recovered; normal delivery uses the
stricter StartByID).

```text
Verified: a 20s run whose worker was SIGKILLed mid-execution (left 'running',
XPENDING 1) was reclaimed ~10s later by a restarted worker, taken over, and
completed (XPENDING 0). No job lost.
Learning: at-least-once delivery, visibility timeout, idempotency. The reclaim
path must be more permissive than the normal start -- safe because it only fires
for a message idle past the timeout.
```

### Step 13: dead-letter poison jobs

Reclaim scans with XPENDING (which reports each message's delivery count) + XCLAIM.
A message delivered more than MAX_DELIVERIES times is dead-lettered: copied to
`testruns:dead`, its run marked failed (POST .../fail, store.Fail), and acked out
of the pending list so it stops cycling.

```text
Verified: a message pumped to 5 deliveries (> threshold 3) was dead-lettered --
run marked failed with a reason, one entry in testruns:dead, zero left pending.
A normal run still completes unaffected.
Learning: poison-message handling; a delivery-count cap prevents infinite retries.
```

## Phase 3: Multiple Workers

### Level 0: run worker replicas (horizontal scale for concurrent runs)

The worker is now horizontally scalable: several identical replicas share the one
Redis consumer group, so different runs process in parallel. No coordination code
was needed -- the consumer group already load-balances whole jobs across
consumers. A single run still runs entirely on one worker (splitting one run
across workers is Level 1, below).

Changes:
- worker: consumer name derived from the hostname instead of the PID. In a
  container the PID is always 1, so PID-based names collided across replicas and
  broke both load-balancing and per-consumer reclaim tracking. Hostname is the
  unique container id (PID kept as a fallback outside containers).
- docker-compose: removed the worker's fixed container_name so it can be scaled.

Run it:

```powershell
docker compose up -d --build --scale worker=3
```

Verification:

```text
3 replicas registered as 3 distinct consumers; a burst of 6 runs split 2/2/2
across them, running in parallel. Cross-worker recovery: killing the worker
running a job (SIGKILL) let a DIFFERENT replica reclaim and finish it
(run completed, XPENDING 0).
Learning: a consumer group turns "add workers" into near-free horizontal scale;
each job still goes to exactly one worker.
```

Caveat found during the demo (recorded in worker main.go):

```text
Reclaim's idle timeout (RECLAIM_MIN_IDLE) MUST exceed the longest job. A running
worker does not ack until its job finishes, so its in-flight message looks idle;
if the timeout is shorter than the job, another live worker reclaims a job that is
still running -> DOUBLE execution. Idempotency (the state-guarded complete) keeps
the stored result correct, but the work is wasted. The 10s demo timeout against a
20s job triggered exactly this. Proper fix: a heartbeat/lease that refreshes the
claim during a long job (Phase 6).
```

### Level 1: split ONE run across workers (sharding)

A single run is now fanned out into N shards, each a slice of the virtual users,
so several workers execute one run in parallel. When the last shard finishes, the
per-shard partial results are aggregated back into the run. A run with `shards=1`
is exactly the old single-worker path, so the change is backward compatible.

Files:

```text
migrations/002_create_test_run_shards.sql   shard_count column + test_run_shards
backend/internal/models/test_run.go          ShardAssignment, CompleteShardRequest,
                                              ShardCount / Shards fields
backend/internal/store/test_run_store.go     CreateWithShards, StartShard,
                                              TakeOverShard, CompleteShard, FailShard,
                                              aggregateRun (the completion barrier)
backend/internal/queue/publisher.go          PublishShard (one job per shard)
backend/internal/handlers/test_runs.go       splitVUs + fan-out on create
backend/internal/handlers/internal_api.go    /internal/shards/{id}/{start,takeover,
                                              complete,fail}
worker/cmd/worker/main.go                     consume shard jobs, take over reclaimed
                                              shards, dead-letter poison shards
worker/internal/client/client.go             shard client calls
worker/internal/runner/runner.go             Result.LatencyCount (for weighting)
```

Design points:

- Fan-out is one transaction: insert the run with `shard_count = N` and N
  `test_run_shards` rows, then publish one job per shard. `splitVUs` divides the
  VUs evenly and hands the remainder to the first few shards (e.g. 20 VUs / 4 =
  5,5,5,5; 10 VUs / 3 = 4,3,3).
- Each shard has its own lifecycle (`queued -> running -> completed|failed`),
  driven exactly like a run was: `StartShard` (strict, queued-only) for a normal
  delivery, `TakeOverShard` (accepts a still-`running` shard) for a reclaim.
- Completion barrier: `CompleteShard`/`FailShard` records the shard's partial,
  then counts how many shards are still non-terminal. Only when that count hits
  zero does it aggregate the run and flip it to completed. The aggregate is
  guarded by `WHERE status='running'`, so exactly one shard "wins" the barrier.
- Aggregation is a WEIGHTED average, not an average of averages:
  `avg = SUM(shard_avg * shard_latency_count) / SUM(shard_latency_count)`, with
  min = MIN of shard mins and max = MAX of shard maxs (mins filtered to shards
  that actually recorded a sample). `LatencyCount` travels from runner to shard
  row precisely so this weighting is correct.

Learning point:

```text
Splitting work is the easy half; recombining it correctly is the hard half. The
completion barrier ("aggregate only when the last shard is terminal") plus a
state-guarded aggregate makes the fan-in safe under concurrency and duplicate
delivery. Averaging averages is wrong when shards do different amounts of work --
weight each shard's average by its sample count.
```

Verification (3 worker replicas, run created from the host):

```text
Backward compat: a run with shards=1 completes unchanged on one worker.
Fan-out: a run with 40 VUs / 4 shards split across 3 workers (one worker ran two
shards); the run aggregated to totalRequests 193864 -- the exact sum of the four
shard partials.
```

Cross-worker shard reclaim (kill a worker mid-shard):

```text
Run 25: 20 VUs / 4 shards (6s each). Worker A was SIGKILLed while running shard 1
(its row left 'running', its Redis message stuck pending). ~10s later a DIFFERENT
replica saw the idle message, XCLAIMed it, took it over (TakeOverShard on a
running shard), re-ran it, and reported it. The barrier held the run open until
all 4 shards were terminal, then aggregated: all shards completed, run completed,
totalRequests = exact sum of the four partials. No shard lost.
```

Note (deliberately clean demo): 6s shards under the 10s reclaim window mean the
healthy shards ack well before anything can reclaim them, so only the genuinely
orphaned shard crosses the idle threshold. That is the flip-side of the Level 0
caveat: short jobs -> reclaim fires only on real deaths; long jobs (> the idle
timeout) -> healthy shards also look idle and get double-run (correct but wasted)
until the Phase 6 heartbeat/lease.

## Phase 4: Metrics and Observability

The platform now has LIVE visibility while a run executes, not just a final
aggregate. The backend and every worker expose a Prometheus `/metrics` endpoint;
Prometheus scrapes them and stores time series; Grafana draws live dashboards.

New services (docker-compose): `prometheus` (scrapes backend:8080 and every
worker replica, discovered by DNS on the "worker" service name at :9100) and
`grafana` (Prometheus data source + the dashboard auto-provisioned from
./monitoring). The worker had no HTTP server, so it now runs a small side server
just for /metrics.

The metrics (defined in worker/internal/runner/metrics.go, updated in the runner
hot path):

```text
loadtest_requests_total          counter,   labels: status_class, method
loadtest_request_duration_seconds histogram, label: method (le auto-added)
loadtest_active_vus              gauge,     no labels (instance = which worker)
```

Design decisions (the learning of this phase):

- Metric TYPE follows behavior: a monotonic count is a counter; a current level
  that moves both ways is a gauge; a distribution you want percentiles from is a
  histogram. Latency is a histogram, NOT a gauge -- a gauge holds one value and
  can't yield p50/p95/p99; a histogram keeps bucket counts, so any percentile is
  cheap (histogram_quantile).
- LABEL CARDINALITY is the constraint: every distinct label-value combination is
  its own stored series, so labels must be BOUNDED. status_class (2xx/4xx/5xx/
  error) and method are bounded and kept. run_id, target_url, vu_id are unbounded
  (over time / user-supplied) and were REJECTED -- they would explode the series
  count, worst of all on a histogram (cost is multiplied by the bucket count).
- Per-run detail stays in Postgres (test_runs already stores each run's result);
  metrics are for live, aggregate, bounded-label trends. Errors are a status_class
  SLICE of requests_total, not a separate metric.
- Expose RAW cumulative counters and derive rates at query time (rate() in PromQL),
  never pre-compute a rate in code. This also dissolves the consistent-snapshot
  problem: individual metrics are atomic (safe to scrape mid-write), and because
  nothing in the hot path combines two metrics, no coherent multi-value snapshot
  is ever needed.

Concurrency (the hot path): each request updates the shared Prometheus metrics
(.Inc()/.Observe()) alongside the existing lock-free per-VU vuStats. Those calls
run from every VU goroutine at once and are safe because the client increments
ATOMICALLY under the hood -- the atomic option done for us, no lock. The
active_vus gauge is incremented when each VU goroutine spawns and decremented via
`defer` when it exits, so it is balanced no matter how the goroutine returns
(rises to the VU count during a run, drains to 0 at the end).

```text
Verified live: a sharded run's loadtest_requests_total climbs per worker mid-run;
Prometheus gives fleet RPS via sum(rate(...[30s])); the histogram _count equals the
2xx count (observe fires only on a response); p95 comes from histogram_quantile;
active_vus rises to the requested VU count across workers and drains to 0.
Grafana "Load Testing Platform" dashboard shows RPS, RPS-by-class, error rate,
p50/p95/p99 latency, and active VUs per worker, refreshing live.
```

Learning point:

```text
Instrumenting a hot path is a concurrency + trade-off problem, not a checkbox. Use
atomic shared counters (the Prometheus client) so many goroutines can update one
number safely; model each metric by behavior (counter/gauge/histogram); keep every
label bounded or cardinality explodes; and expose raw facts, deriving rates and
percentiles at query time so the query layer -- not your hot path -- does the math.
```

## Phase 5: Better Runner Capabilities

The runner grew from a GET-only engine into a realistic one. Three features, each
threading through the full stack (create API -> Postgres -> shard assignment ->
worker -> runner):

Custom request shape (migration 003):

```text
Headers (jsonb) and a request body (text) on a run, so POST/PUT/PATCH tests send
real payloads. The runner sets the headers on each request and sends a FRESH body
reader per request -- a shared io.Reader is consumed after the first request, so
every request builds its own strings.NewReader over the (immutable) body string.
```

Latency percentiles -- p50/p95/p99 (migrations 004):

```text
Averages lie about user experience: an avg smears outliers across everyone and no
real request takes "the average". Percentiles answer the real question (typical vs
tail). You cannot derive p99 from avg/min/max, and you cannot keep every latency
(memory blows up), so each VU keeps a fixed, log-spaced HISTOGRAM (bounded memory,
bucket-width accuracy -- the memory/accuracy tradeoff). mergeStats sums the per-VU
bucket arrays; percentile() walks the cumulative counts (nearest-rank).

Percentiles do NOT average across shards, so each shard reports its bucket COUNTS;
the backend merges the shard histograms (element-wise add) and computes the run's
percentiles from the combined distribution. Verified: a /random run showed avg
154ms but p50 1ms / p95 1000ms -- the average hid a bimodal fast/slow split.
```

Think-time (migration 005):

```text
Each VU can pause between requests, turning the pure back-to-back CLOSED loop
(hammer as fast as possible -- a stress test) into a realistic paced one (users who
think between actions). The pause is interruptible -- select on ctx.Done() vs
time.After -- so a long think-time never makes a run overrun its deadline; a plain
time.Sleep would. Verified: 5 VUs/4s went from 66k requests (no think-time) to 40
with a 500ms think-time -- load paced by think-time, not the target's speed.
```

Also fixed a latent completion-barrier race (not Phase-5-specific): two shards
finishing at the same instant each saw the other still 'running' under READ
COMMITTED and neither aggregated, hanging the run. Fixed by locking the parent run
row (SELECT ... FOR UPDATE) before the barrier count, serializing it.

```text
Learning: request shape is per-request construction (separate from the concurrency
of firing many); a histogram trades memory for accuracy and, crucially, MERGES
exactly (which averaging percentiles cannot); think-time is the closed-loop vs
open-loop load model; and a concurrent completion barrier needs serialization.
```

Deferred Phase 5 items (optional, not built): status-code breakdown, per-second
RPS buckets, assertions/checks, multi-step scenarios.

## Phase 6: Failure Handling

Two pieces: a heartbeat/lease that removes the reclaim double-execution caveat,
and user-initiated run cancellation. The rest of the roadmap's Phase 6 list
(graceful shutdown, ctx cancellation, retries, dead-letter, recovery states) was
already in place from earlier phases.

Heartbeat / lease:

```text
A reclaimer cannot tell "slow but alive" from "dead" -- both look like an unacked
message. Before, RECLAIM_MIN_IDLE had to exceed the longest job or a busy worker's
still-running job got reclaimed and run twice. Now, while processing, a worker
refreshes its claim every HEARTBEAT_INTERVAL (XCLAIM JUSTID resets the message's
idle time WITHOUT bumping the delivery counter -- a plain XCLAIM would inflate it
and falsely dead-letter). So a live worker's message never looks idle, and
RECLAIM_MIN_IDLE only needs to exceed the heartbeat interval, not the job. A dead
worker stops heartbeating and is reclaimed. heartbeat() is a goroutine (ticker +
select{ctx.Done()/tick}) started before executeShard and stopped when it returns.
Verified: a 20s job under a 10s reclaim threshold ran exactly ONCE; a worker
killed mid-job was still reclaimed by a peer ~12s later.
```

User-initiated cancellation:

```text
POST /test-runs/{id}/cancel moves a queued/running run to 'cancelling'. The runner
already stops on context cancellation (duration elapsed / worker shutdown), so
cancellation is just a third reason to cancel that context. Each worker runs a
watchCancel() goroutine that polls the run's status (client.RunStatus) alongside
the heartbeat; on 'cancelling' it cancels runCtx -> the VUs stop early. Shards
report their PARTIAL results, and the completion barrier (aggregateRun, which now
reads the run status under its row lock) finalizes 'cancelling' -> 'cancelled',
keeping the partials. Signal path: user -> backend (DB status) -> worker (poll) ->
context cancel -> load stops. Verified: a 30s run cancelled ~5s in reached
'cancelled' ~3s later with 53,785 partial requests preserved.
```

```text
Learning: a lease is a time-bounded claim you RENEW to keep -- renewal is the
proof of life, and it decouples failure detection from job duration. Distributed
cancellation is the same background-goroutine pattern as the heartbeat, but it
WATCHES for a stop signal instead of sending a keep-alive; routing it through the
context the runner already respects means zero new stop logic in the hot path.
```

## Phase 7: Dashboard

A web UI (React + Vite) over the existing JSON API, so runs can be created and
inspected in the browser instead of with curl.

```text
Backend: a GET /test-runs list endpoint (newest first, ?limit) and a CORS
middleware; Routes() now returns an http.Handler wrapped in CORS.

Frontend (frontend/, a Vite + React SPA with react-router + recharts): the Home
page owns the runs data and polls GET /test-runs every 3s; summary tiles (total /
running / completed / failed); a create form that POSTs a run and navigates to it;
a run list; and a run detail page that polls GET /test-runs/{id} every 2s WHILE the
run is non-terminal (then stops), can POST .../cancel, and charts latency
percentiles (bar) and success/failed (donut). Dark theme, inline SVG icons.

Serving: a multi-stage image (Node builds the SPA, nginx serves it) that also
reverse-proxies /api -> backend, so the browser talks to one origin (no CORS in
prod; the header is a dev backstop). New compose service "frontend" on :3001.
```

```text
Learning: the dashboard is a pure API CLIENT -- no business logic, just fetch +
render. "Live" is just polling (setInterval / self-re-arming setTimeout), the
browser echo of the worker's poll loops; the detail page polls only until the run
is terminal. One nginx origin reverse-proxying /api is the clean way to avoid CORS.
```

Deferred: a worker-status view (Grafana already shows live per-worker metrics, and
there is a link to it in the header).

## Phase 8: Safety and Multi-User Controls

Two safety controls so the platform can't be misused (the roadmap's auth / projects
/ RBAC / audit-log items are deliberately deferred -- see follow-ups).

Target allowlist (deny by default):

```text
An open load tester is a weapon: it would send N workers x V virtual users of
traffic at ANY URL a caller supplies -- DDoS-for-hire, and SSRF (workers sit inside
the network, so they could hit internal / cloud-metadata addresses an outsider
can't). The fix is an allowlist, not a blocklist: you can't enumerate every bad
host, but you can enumerate the few permitted ones, and deny everything else.
CreateTestRun parses the target URL, takes its host (url.Hostname(), port stripped,
lowercased), and rejects anything not in ALLOWED_TARGET_HOSTS (default
target,localhost,127.0.0.1) with 403. Unparseable URLs fail CLOSED (denied).
Verified: example.com and 169.254.169.254 -> 403; target -> 201.
```

Concurrent-run cap (admission control):

```text
Per-run caps (VUs/duration/shards) bound one run's size; this bounds how many runs
are in flight at once, so the worker pool / DB / target can't be swamped. At
admission (create time), CountActiveRuns counts non-terminal runs
(queued/running/cancelling) and CreateTestRun refuses new ones with 429 Too Many
Requests once MAX_CONCURRENT_RUNS (default 10) are active. Best-effort: a
check-then-create race exists; a hard guarantee would count inside the insert tx.
Verified: with the cap reached, further runs got 429.
```

```text
Learning: secure by default (deny-all, opt in); allowlist vs blocklist; fail
closed; SSRF; admission control / backpressure (reject new work at capacity, with
429); the TOCTOU check-then-act race and why a hard limit needs the check in the
write transaction.
```

## Current System State

```text
Whole platform runs in Docker Compose: postgres + redis + backend + N worker
replicas + target + prometheus + grafana + frontend. Jobs are distributed via a
Redis Streams consumer group (no polling), load-balanced across workers, with crash
recovery (reclaim), a heartbeat/lease so slow workers are not falsely reclaimed, and
dead-lettering. A single run is fanned out into shards that run in parallel across
workers and are re-aggregated on completion (counts, weighted avg, and
merged-histogram percentiles). The runner sends custom methods/headers/bodies with
optional think-time. A running test can be cancelled by the user. Runs may only
target allowlisted hosts, and a concurrent-run cap bounds in-flight load. Live
metrics (RPS, error rate, latency percentiles, active VUs) are exposed to Prometheus
and shown on a Grafana dashboard while runs execute. A React dashboard
(localhost:3001) drives create/list/detail/cancel over the API. End-to-end lifecycle
runs on its own with REAL load results.
```

Phases 1 through 8 are COMPLETE (Phase 8 = the safety controls; multi-user/auth
deferred).

Known follow-ups (not blocking):

```text
- Multi-user layer deferred from Phase 8: authentication / API keys, projects or
  workspaces, role-based access, and audit logs (the allowlist + concurrency cap
  are in; these are the hosted-product pieces).
- The concurrent-run cap is best-effort (check-then-create race); a hard guarantee
  would count active runs inside the insert transaction.
- Dual-write gap: if the INSERT succeeds but XADD fails, the run is queued with
  no message and won't be delivered (add an outbox pattern later).
- Optional runner breadth: status-code breakdown, per-second RPS buckets,
  assertions/thresholds, multi-step scenarios with data correlation.
- The runner's latency bucket boundaries are duplicated in the backend
  (store/histogram.go) and MUST stay in sync with worker/.../runner/histogram.go.
- Cancellation signalling is poll-based (every heartbeat interval); Redis pub/sub
  would make it near-instant.
```

## Next Step

Phase 9: local orchestration and deployment.

```text
Deploy the platform like real infrastructure: local Kubernetes (kind or minikube)
with Deployments/Services for each component, health/readiness probes, service
discovery, and -- the interesting part -- autoscaling the worker pool (HPA) so
replicas scale with load. Plus a basic CI pipeline. This is the final roadmap
phase. (Multi-user auth and the other deferred items above can follow after.)
```

