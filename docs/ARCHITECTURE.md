# Architecture Notes

## Phase 1 Architecture

Phase 1 is deliberately simple but shaped like a real distributed system.

```text
Client / curl
    |
    v
Backend API
    |
    v
PostgreSQL
    ^
    |
Worker
    |
    v
Target Service
```

## Component Responsibilities

### Backend API

The backend is the control plane.

Responsibilities:

- Accept test run creation requests.
- Validate test run input.
- Store test run configuration.
- Expose test run status and results.
- Provide internal endpoints for workers.
- Own the test run lifecycle state.

The backend does not generate load.

### Worker

The worker is the execution service.

Responsibilities:

- Look for queued test runs.
- Claim a test run.
- Execute the test using the custom runner.
- Aggregate results.
- Submit results back to the backend.

In Phase 1, the worker polls the backend. In Phase 2, polling will be replaced by a message broker.

### Custom Runner

The runner is the core learning component.

Phase 1 uses a closed-loop virtual-user model:

```text
virtual user sends request -> waits for response -> sends next request
```

Each virtual user will be represented by a goroutine.

The runner will support:

- Target URL
- HTTP method, starting with GET
- Virtual user count
- Duration
- Request timeout
- Basic latency and error metrics

### Target Service

The target service is a safe local app used for testing.

Endpoints:

- `GET /health`
- `GET /fast`
- `GET /slow`
- `GET /error`
- `GET /random`

This lets us test fast responses, slow responses, failures, and mixed behavior without sending traffic to public websites.

### PostgreSQL

PostgreSQL stores durable platform state.

Initial table:

- `test_runs`

In later phases, we may add:

- `workers`
- `worker_assignments`
- `test_run_events`
- `test_run_samples`
- `projects`
- `users`

## Test Run Lifecycle

Initial lifecycle:

```text
queued -> running -> completed
queued -> running -> failed
```

Later lifecycle states may include:

```text
cancelled
timed_out
partially_failed
```

## Why Start Without Redis

Phase 1 uses polling because it makes the core lifecycle easier to see:

```text
create work -> claim work -> execute work -> store result
```

Once that works, Redis Streams or NATS can replace polling without changing the purpose of each component.

## Why Build The Runner From Scratch

Using a mature tool like k6 would be faster, but this project is for learning.

Building the runner ourselves teaches:

- Goroutine-based concurrency
- Shared result collection
- Context cancellation
- Request timeouts
- HTTP client tuning
- Worker bottlenecks
- Latency measurement
- Result aggregation
- Load model tradeoffs

The first runner will not be as complete or accurate as professional tools. That is acceptable. We will improve it phase by phase.

## Important Design Principle

Each phase should leave the system working end to end.

We should avoid building isolated pieces that cannot run together. The project should always have a visible lifecycle we can execute and inspect.

