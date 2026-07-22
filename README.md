# Distributed Load Testing Platform

This project is a learning-focused distributed load testing platform. The goal is not just to build a tool that sends HTTP requests, but to understand the system design behind distributed workers, orchestration, queues, metrics, failure handling, and observability.

The platform will start small and grow in phases. In the first phase, we will build a complete single-worker lifecycle:

```text
Create test run -> worker claims it -> custom runner sends load -> results are stored
```

Over time, this will evolve into a distributed system with multiple workers, a message broker, live metrics, a dashboard, and safety controls.

## Core Idea

A load testing platform simulates many users hitting a target application so we can measure how the application behaves under pressure.

This project will have four initial services:

```text
Backend API      -> control plane that creates and tracks test runs
Worker           -> executes load tests using our own custom runner
Target Service   -> safe local app to test against
PostgreSQL       -> stores test definitions and results
```

The runner will be written from scratch instead of using k6, JMeter, Locust, or Gatling. This is intentional because the project is meant to teach concurrency, request scheduling, timeouts, cancellation, aggregation, and worker design.

## First Milestone

The first milestone is complete when we can:

1. Start PostgreSQL locally.
2. Run the backend API.
3. Run the target service.
4. Run one worker.
5. Create a test run through the backend API.
6. Have the worker claim and execute that test.
7. Store final results in PostgreSQL.
8. Fetch the completed test run from the API.

Example test:

```json
{
  "name": "Fast endpoint test",
  "targetUrl": "http://target:8081/fast",
  "method": "GET",
  "virtualUsers": 10,
  "durationSeconds": 30
}
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

## Planned Stack

Phase 1 stack:

- Go for backend, worker, target service, and custom runner
- PostgreSQL for persistent storage
- Docker Compose for local infrastructure
- Plain HTTP APIs for service communication

Later phases may add:

- Redis Streams or NATS for job distribution
- Prometheus for metrics
- Grafana or a custom React dashboard
- Multiple workers
- Authentication, quotas, and target allowlists
- Local Kubernetes using kind or minikube

## Documentation

Start here:

- [Project Roadmap](docs/ROADMAP.md)
- [Architecture Notes](docs/ARCHITECTURE.md)
- [Learning Goals](docs/LEARNING_GOALS.md)
- [Progress Log](docs/PROGRESS.md)
