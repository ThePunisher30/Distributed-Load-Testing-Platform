# Learning Goals

This project is meant to teach system design through implementation.

## Core System Design Topics

### Service Boundaries

We will separate the control plane, worker, target service, and database. This makes the system easier to reason about and prepares it for distributed execution.

### Job Lifecycle

Every test run is a job with state.

Initial states:

```text
queued
running
completed
failed
```

Understanding state transitions is central to distributed systems.

### Worker Design

Workers perform execution outside the API server.

This teaches:

- Background processing
- Polling vs queue-based work
- Heartbeats
- Graceful shutdown
- Worker capacity
- Partial failures

### Concurrency

The custom runner will use goroutines to simulate virtual users.

This teaches:

- Concurrent execution
- Context cancellation
- Channels
- Synchronization
- Aggregation without unsafe shared mutation

### Metrics

Load testing is only useful if results are trustworthy.

We will start with:

- Total requests
- Successful requests
- Failed requests
- Average latency
- Minimum latency
- Maximum latency

Later, we will add:

- p50 latency
- p95 latency
- p99 latency
- RPS over time
- Status code breakdown
- Per-worker metrics

### Observability

We will eventually expose metrics and dashboards so the system can explain itself while it runs.

This teaches:

- Health checks
- Logs
- Metrics
- Dashboards
- Operational debugging

### Failure Handling

A distributed system is defined by what happens when something breaks.

We will deliberately test:

- Worker crash
- Backend restart
- Database downtime
- Target timeout
- Duplicate execution
- Slow workers

### Scaling

The project will gradually move from one worker to many workers.

This teaches:

- Load splitting
- Scheduling
- Worker registration
- Distributed aggregation
- Capacity planning

## Guiding Rule

Build the smallest version of a concept first, then make it more realistic.

For example:

```text
Polling worker -> Redis-backed worker queue -> multiple workers -> failure recovery
```

This keeps the project understandable while still moving toward real system design.

