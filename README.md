# Distributed Load Testing Platform

A learning-focused distributed load testing platform. The goal is not just to
build a tool that sends HTTP requests, but to understand the system design behind
distributed workers, orchestration, message queues, metrics, failure handling,
and observability — by building each piece from scratch.

The platform grows in phases. **Phases 1–5 are complete**: a full test-run
lifecycle, distributed through a Redis Streams message broker, with worker crash
recovery and dead-lettering, run across multiple worker replicas — including
splitting a single run into shards that execute in parallel and are re-aggregated
— with live Prometheus metrics and a Grafana dashboard showing what happens as a
run executes. The runner sends custom methods, headers, and bodies with optional
think-time, and reports latency percentiles (p50/p95/p99) merged across shards.

```text
create test run -> backend publishes a job -> worker consumes it ->
custom runner sends load -> results stored -> read back
```

## Core idea

A load testing platform simulates many users hitting a target application so we
can measure how it behaves under pressure. This project has these services:

```text
Backend API     control plane: creates/tracks test runs, publishes jobs
Worker          consumes jobs and executes them with a custom load runner
Target Service  safe local app to send load at (/fast, /slow, /error, /random)
PostgreSQL      source of truth for test-run config and results
Redis           message broker (Streams) that distributes jobs to workers
Prometheus      scrapes /metrics from backend + workers, stores time series
Grafana         live dashboard (RPS, error rate, latency percentiles, active VUs)
```

The load runner is written from scratch (no k6, JMeter, Locust, or Gatling) on
purpose: it is where the concurrency lessons live — a goroutine per virtual user,
context cancellation, per-request timeouts, and race-free result aggregation.

## Architecture

```text
create:  curl --POST /test-runs-->  Backend  --INSERT (queued)-->  PostgreSQL
                                    Backend  --XADD job (run id)-->  Redis "testruns"

run:     Redis  --XREADGROUP-->  Worker  --start / complete (HTTP)-->  Backend --> PostgreSQL
                                 Worker  --HTTP load-->  Target Service
                                 Worker  --XACK-->  Redis   (job done)

read:    curl --GET /test-runs/{id}-->  Backend  -->  PostgreSQL
```

The worker does not poll. The backend publishes a job to Redis; a worker consumes
it via a consumer group, runs the load, and acknowledges it. If a worker dies
mid-job, another reclaims the message after a visibility timeout; a job that keeps
failing is moved to a dead-letter stream.

A run can also be split into **shards** (`"shards": N` on create): the backend
fans the virtual users out across N jobs that run in parallel on different
workers, then re-aggregates the per-shard results (a weighted average over each
shard's sample count) once the last shard finishes. `shards` defaults to 1, the
single-worker path. If a worker dies mid-shard, another replica reclaims just that
shard and the run still aggregates exactly.

## Running it

Everything runs in Docker Compose:

```bash
docker compose up --build
```

Run several worker replicas — the Redis consumer group load-balances runs across them:

```bash
docker compose up -d --build --scale worker=3
```

Create a test run (from the host):

```bash
curl -X POST http://localhost:8080/test-runs \
  -H "Content-Type: application/json" \
  -d '{"name":"demo","targetUrl":"http://target:8081/fast","method":"GET","virtualUsers":10,"durationSeconds":5}'
```

Split one run across workers with `shards` (here 40 VUs as 4 × 10):

```bash
curl -X POST http://localhost:8080/test-runs \
  -H "Content-Type: application/json" \
  -d '{"name":"sharded","targetUrl":"http://target:8081/fast","method":"GET","virtualUsers":40,"durationSeconds":5,"shards":4}'
```

Read it back (use the id from the create response):

```bash
curl http://localhost:8080/test-runs/1
```

Example result:

```json
{
  "status": "completed",
  "totalRequests": 8421,
  "successfulRequests": 8421,
  "failedRequests": 0,
  "avgLatencyMs": 8.4,
  "minLatencyMs": 2.1,
  "maxLatencyMs": 91.7,
  "p50LatencyMs": 5,
  "p95LatencyMs": 25,
  "p99LatencyMs": 100
}
```

Requests can carry custom `method`, `headers`, and `body`, and a `thinkTimeMs`
pause between requests (0 = back-to-back). For example, a paced POST:

```bash
curl -X POST http://localhost:8080/test-runs \
  -H "Content-Type: application/json" \
  -d '{"name":"paced","targetUrl":"http://target:8081/fast","method":"POST",
       "virtualUsers":20,"durationSeconds":10,"shards":4,"thinkTimeMs":500,
       "headers":{"Authorization":"Bearer t"},"body":"{\"hello\":\"world\"}"}'
```

## Live metrics

While runs execute, the backend and every worker expose Prometheus metrics, and a
Grafana dashboard shows them live:

```text
Grafana      http://localhost:3000   (anonymous viewing on; "Load Testing Platform")
Prometheus   http://localhost:9090   (targets, and a PromQL query box)
```

Metrics the runner emits (bounded labels only; per-run detail stays in Postgres):

```text
loadtest_requests_total            counter    labels: status_class, method
loadtest_request_duration_seconds  histogram  label: method   (p50/p95/p99)
loadtest_active_vus                gauge      current live virtual users per worker
```

Rates and percentiles are derived at query time in PromQL, e.g. fleet RPS is
`sum(rate(loadtest_requests_total[30s]))` and p95 latency is
`histogram_quantile(0.95, sum by (le) (rate(loadtest_request_duration_seconds_bucket[1m])))`.

## Tests

```bash
# Runner tests (goroutines + HTTP), no Docker needed:
cd worker && go test -race ./internal/runner/

# Store tests need Postgres and a test database:
docker compose up -d postgres
export TEST_DATABASE_URL="postgres://dltp:dltp@localhost:5432/dltp_test?sslmode=disable"
cd backend && go test ./internal/store/
```

Store tests skip themselves when `TEST_DATABASE_URL` is unset, so `go test ./...`
stays green without a database.

## Stack

Current:

- Go — backend, worker, target service, and the custom runner
- PostgreSQL — persistent storage and source of truth
- Redis Streams — job-distribution message broker (consumer groups, reclaim, dead-letter)
- Prometheus + Grafana — pull-based metrics and live dashboards
- Docker Compose — local infrastructure, one-command startup; scale workers with `--scale worker=N` (they share the consumer group)

Planned for later phases:

- Heartbeat/lease to remove the reclaim double-execution caveat (Phase 6)
- A React dashboard for creating and viewing runs (Phase 7)
- Authentication, quotas, and target allowlists (Phase 8)
- Local Kubernetes with kind or minikube (Phase 9)
- Optional runner breadth: status-code breakdown, per-second RPS buckets, assertions/thresholds, multi-step scenarios

## Documentation

- [Project Roadmap](docs/ROADMAP.md)
- [Architecture Notes](docs/ARCHITECTURE.md)
- [Learning Goals](docs/LEARNING_GOALS.md)
- [Progress Log](docs/PROGRESS.md)
