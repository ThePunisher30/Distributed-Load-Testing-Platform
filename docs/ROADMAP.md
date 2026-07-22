# Project Roadmap

This roadmap is intentionally phased. Each phase adds one major system design idea while keeping the platform working end to end.

## Phase 1: Single-Worker MVP

Goal:

```text
Build the complete lifecycle with one backend, one worker, one database, and one local target service.
```

Components:

- Backend API
- PostgreSQL database
- Worker service
- Custom Go load runner
- Local target service
- Docker Compose setup

What we will build:

1. Project structure.
2. Target service with `/health`, `/fast`, `/slow`, `/error`, and `/random`.
3. PostgreSQL schema for `test_runs`.
4. Backend health endpoint.
5. Backend database connection.
6. Public API for creating and reading test runs.
7. Internal API for workers to claim and complete test runs.
8. Worker polling loop.
9. Custom closed-loop virtual-user runner.
10. Final result aggregation and storage.
11. Docker Compose flow for running everything locally.

Key learning outcomes:

- HTTP service design
- Database-backed job lifecycle
- Worker polling
- Concurrency with goroutines
- Context cancellation
- Request timeouts
- Latency measurement
- Result aggregation
- Basic service boundaries

Success criteria:

```text
POST /test-runs creates a queued test.
The worker claims it.
The worker sends load to the target service.
The backend stores completed results.
GET /test-runs/{id} returns the final result.
```

## Phase 2: Queue-Based Job Distribution

Goal:

```text
Replace worker polling with a real message broker.
```

Likely tools:

- Redis Streams, or
- NATS

What we will build:

- Job publishing from backend
- Worker subscription
- Acknowledgement flow
- Retry behavior
- Dead-letter or failed-job handling
- Job lease or visibility timeout concept

Key learning outcomes:

- Asynchronous communication
- At-least-once delivery
- Duplicate job handling
- Idempotency
- Backpressure
- Broker availability tradeoffs

## Phase 3: Multiple Workers

Goal:

```text
Run one test across multiple workers.
```

What we will build:

- Worker registration
- Worker heartbeat
- Worker capacity metadata
- Test assignment splitting
- Per-worker results
- Aggregated run results

Example:

```text
Requested load: 1,000 virtual users
Workers: 4
Assignment: 250 virtual users per worker
```

Key learning outcomes:

- Distributed coordination
- Scheduling
- Capacity-aware assignment
- Partial failure handling
- Aggregating distributed results

## Phase 4: Metrics and Observability

Goal:

```text
Add live visibility into what the system is doing.
```

Likely tools:

- Prometheus
- Grafana

What we will build:

- Worker metrics endpoint
- Backend metrics endpoint
- Live RPS
- Error rate
- Latency summaries
- Active virtual users
- Worker health dashboard

Key learning outcomes:

- Time-series metrics
- Pull-based monitoring
- Metric cardinality
- Dashboards
- Service health signals

## Phase 5: Better Runner Capabilities

Goal:

```text
Evolve the custom runner from a basic GET runner into a more realistic load engine.
```

What we will add:

- HTTP methods beyond GET
- Headers
- Request body
- Per-request timeout
- p50, p95, and p99 latency
- Status code breakdown
- Per-second RPS buckets
- Multi-step scenarios
- Think time
- Basic assertions/checks

Key learning outcomes:

- Load model design
- Closed-loop vs open-loop traffic
- Percentile calculation
- Memory vs accuracy tradeoffs
- Scenario modeling

## Phase 6: Failure Handling

Goal:

```text
Make failure behavior explicit and observable.
```

Failure cases to test:

- Worker crashes during a test
- Backend restarts
- Database temporarily unavailable
- Broker temporarily unavailable
- Target service times out
- Duplicate job delivery
- Slow workers

What we will build:

- Worker heartbeat timeout
- Test run recovery states
- Retry rules
- Graceful shutdown
- Cancellation
- Failure event logging

Key learning outcomes:

- Timeouts
- Retries
- Leases
- Graceful degradation
- Eventual consistency
- Recovery design

## Phase 7: Dashboard

Goal:

```text
Build a usable interface for creating tests and reading results.
```

Likely tools:

- React
- Vite or Next.js
- Charting library

What we will build:

- Test run creation form
- Test run list
- Run detail page
- Result summary
- Charts for latency, RPS, and errors
- Worker status view

Key learning outcomes:

- API-driven UI
- Real-time-ish polling
- Data visualization
- Operational UX

## Phase 8: Safety and Multi-User Controls

Goal:

```text
Add guardrails so the platform cannot be misused casually.
```

What we will build:

- Authentication
- Projects or workspaces
- Target allowlist
- Max virtual users
- Max duration
- Audit logs
- API keys
- Basic role-based access

Key learning outcomes:

- Multi-tenancy
- Authorization
- Abuse prevention
- Quotas
- Secure defaults

## Phase 9: Local Orchestration and Deployment

Goal:

```text
Learn how this system behaves when deployed like real infrastructure.
```

Possible tools:

- Docker Compose
- kind
- minikube
- Helm
- GitHub Actions

What we will build:

- Multi-service Compose setup
- Local Kubernetes deployment
- Health checks
- Service discovery
- Basic CI

Key learning outcomes:

- Containerization
- Deployment topology
- Local Kubernetes
- Infrastructure as code concepts
- Service readiness and liveness

## Long-Term Optional Ideas

- Multi-region workers
- Cloud worker autoscaling
- Distributed tracing
- Report export as PDF/CSV
- Run comparison
- Threshold-based pass/fail
- Scriptable scenarios
- Open-loop request-rate mode
- WebSocket load tests
- gRPC load tests
- Alternate runner in C++ (experiment)

## Experiment: C++ Runner

Idea:

```text
Rewrite ONLY the runner (the load-generating hot path) as a separate C++ worker
and compare it against the Go worker.
```

Why this is possible without touching the rest of the system:

- Workers talk to the backend over plain HTTP (claim + complete). A C++ worker
  can implement the same two calls and slot in beside the Go worker with zero
  backend or database changes. This is a concrete payoff of clean service
  boundaries.

What we would measure:

- Requests/sec pushed from one C++ worker vs one Go worker (throughput per box).
- Tail latency accuracy: C++ has no garbage collector, so no GC pauses distort
  the measured p99. Go's GC can occasionally add pause time to a timed request.

Trade-offs to keep in mind:

- Go's "one goroutine per virtual user" model does not scale in C++ (OS threads
  are too heavy at ~1000s). A C++ runner would need async I/O (epoll / io_uring
  or a library such as Boost.Asio), which is a steeper concurrency model.
- No batteries-included HTTP client; needs a third-party library and build setup.

Verdict:

```text
Not needed for the core learning goals (distributed-systems design). Best treated
as an optional advanced systems experiment once the Go platform works end to end.
```

