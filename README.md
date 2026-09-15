# Distributed Load Testing Platform

A learning-focused distributed load testing platform. The goal is not just to
build a tool that sends HTTP requests, but to understand the system design behind
distributed workers, orchestration, message queues, metrics, failure handling,
and observability — by building each piece from scratch.

The platform grows in phases. **Phases 1 and 2 are complete**: a full test-run
lifecycle, distributed through a Redis Streams message broker, with worker crash
recovery and dead-lettering.

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

## Running it

Everything runs in Docker Compose:

```bash
docker compose up --build
```

Create a test run (from the host):

```bash
curl -X POST http://localhost:8080/test-runs \
  -H "Content-Type: application/json" \
  -d '{"name":"demo","targetUrl":"http://target:8081/fast","method":"GET","virtualUsers":10,"durationSeconds":5}'
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
  "maxLatencyMs": 91.7
}
```

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
- Docker Compose — local infrastructure, one-command startup

Planned for later phases:

- Multiple workers (Phase 3)
- Prometheus + Grafana or a custom dashboard for live metrics (Phases 4 / 7)
- Richer runner: percentiles, request bodies, more HTTP methods, think time (Phase 5)
- Authentication, quotas, and target allowlists (Phase 8)
- Local Kubernetes with kind or minikube (Phase 9)

## Documentation

- [Project Roadmap](docs/ROADMAP.md)
- [Architecture Notes](docs/ARCHITECTURE.md)
- [Learning Goals](docs/LEARNING_GOALS.md)
- [Progress Log](docs/PROGRESS.md)
