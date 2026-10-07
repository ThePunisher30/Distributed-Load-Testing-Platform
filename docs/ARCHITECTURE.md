# Architecture

A distributed HTTP load-testing platform: you describe a test run (target, virtual
users, duration, …), the system fans it out across a pool of workers that generate
the load in parallel, measures it, and aggregates the results back into one record.
It is built to teach distributed-systems design, so every mechanism is hand-written
rather than pulled from a library.

This document describes the system as built (all nine roadmap phases complete). For
the phase-by-phase story and rationale see [PROGRESS.md](PROGRESS.md); for the
concepts it teaches see [LEARNING_GOALS.md](LEARNING_GOALS.md).

---

## 1. Topology

```text
                 ┌─────────────┐      create / list / cancel / read        ┌──────────────┐
   browser ─────▶│  Frontend   │────────────── HTTP /api ──────────────────▶│              │
                 │ (React/nginx)│                                            │   Backend    │
                 └─────────────┘                                            │ (control     │
   curl ──────────────────────────── HTTP ───────────────────────────────▶ │  plane)      │
                                                                            └──────┬───────┘
                                                        INSERT run+shards /        │
                                                        read/aggregate results     ▼
   ┌──────────┐   XADD shard jobs    ┌──────────┐                           ┌──────────────┐
   │  Redis   │◀─────────────────────│ Backend  │                           │  PostgreSQL  │
   │ (Streams │                      └──────────┘                           │ (source of   │
   │  broker) │   XREADGROUP / XACK / XCLAIM                                 │  truth)      │
   └────┬─────┘◀───────────────────────────┐                               └──────▲───────┘
        │ consumer group "workers"          │ start/complete/cancel (HTTP)        │
        ▼                                   │                                     │
   ┌──────────┐  shard = N VUs of load   ┌──┴───────┐   partial results           │
   │ Worker   │──────────────────────────│  Worker  │─────────────────────────────┘
   │ pod/replica  HTTP load ─────────────│  pod     │
   └────┬─────┘                          └──────────┘
        │ HTTP load                       (2..8, autoscaled on k8s)
        ▼
   ┌──────────┐        ┌────────────┐   scrape /metrics   ┌──────────┐   PromQL   ┌─────────┐
   │  Target  │        │ Prometheus │◀────────────────────│ backend  │◀───────────│ Grafana │
   │ (SUT)    │        └────────────┘   + every worker    └──────────┘            └─────────┘
   └──────────┘
```

Everything runs two ways: **Docker Compose** (one command, for dev) and
**Kubernetes** (`k8s/`, real-infra, with an autoscaling worker pool). The images are
identical; only the orchestration differs.

---

## 2. Components

| Component | Role |
|---|---|
| **Backend** (Go) | Control plane. Public API (create/list/read/cancel runs) + internal API (workers claim shards and report results). Owns the run/shard lifecycle, fan-out, the completion barrier, and safety checks. Generates no load. |
| **Worker** (Go) | Execution service. Consumes shard jobs from Redis, runs the load with the custom runner, reports partial results, acks. Horizontally scalable; stateless (all state is in Postgres/Redis). |
| **Runner** (Go, inside the worker) | The load engine, written from scratch. One goroutine per virtual user in a closed loop; race-free aggregation; a latency histogram for percentiles. Knows nothing about the backend, so it is unit-testable in isolation. |
| **Target** (Go) | A safe local system-under-test (`/fast`, `/slow`, `/error`, `/random`, `/health`) so load never hits third parties. |
| **PostgreSQL** | Durable source of truth: `test_runs` + `test_run_shards`. |
| **Redis Streams** | The job broker: one stream (`testruns`), one consumer group (`workers`), a dead-letter stream (`testruns:dead`). |
| **Prometheus + Grafana** | Pull-based live metrics and dashboards (RPS, error rate, latency percentiles, active VUs). |
| **Frontend** (React/Vite, served by nginx) | The dashboard: create runs, a live run list, a per-run detail page with charts and a cancel button. nginx reverse-proxies `/api` → backend, so it is one origin. |

The backend, worker, target, and runner are three separate Go modules
(`backend/`, `worker/`, `target/`) with clean boundaries - workers talk to the
backend only over HTTP, which is what lets them scale independently (and is why an
alternate C++ worker could slot in unchanged).

---

## 3. Data model

**`test_runs`** - one row per run: configuration (name, target URL, method, headers
[JSONB], body, virtual_users, duration_seconds, think_time_ms, shard_count), the
`status` lifecycle, the aggregated results (request counts, avg/min/max and
p50/p95/p99 latency), and timestamps.

**`test_run_shards`** - one row per shard of a run (`UNIQUE(run_id, shard_index)`):
the shard's slice of virtual users, its own `status`, its partial results, and its
**latency histogram** (`latency_buckets`, JSONB) plus `latency_count` so the backend
can combine shards correctly.

Every run has **≥ 1 shard** - an unsplit run is simply a 1-shard run, so there is a
single code path. Migrations live in `migrations/` and run in order on a fresh
database.

### Lifecycle

```text
run:    queued ──▶ running ──▶ completed
                      │   └────▶ failed        (a shard failed / dead-lettered)
                      └────────▶ cancelling ──▶ cancelled   (user cancel)
shard:  queued ──▶ running ──▶ completed | failed
```

State transitions are **guarded UPDATEs** (`WHERE status = 'running'`, etc.), which
is what makes duplicate message delivery safe (see §4).

---

## 4. Key mechanisms (the distributed-systems core)

### Job queue & distribution
Runs are fanned out into shards; each shard is one message (`XADD` carrying only the
`shard_id`) on the `testruns` stream. Workers consume via a **consumer group**
(`XREADGROUP`), so each message goes to exactly one worker and adding workers is
free horizontal scale. (The Phase-1 `FOR UPDATE SKIP LOCKED` Postgres queue remains
as a fallback claim path.)

### Idempotency
Delivery is **at-least-once**, so every state change is a guarded UPDATE. Starting a
shard matches only a `queued` row; a duplicate delivery matches nothing and is
skipped. Completion matches only a `running` row. This makes re-delivery and
double-execution harmless.

### Sharding & the completion barrier
Fan-out (run row + N shard rows) happens in **one transaction**. Each shard runs
independently and reports a partial; the last shard to finish triggers aggregation.
The barrier is serialized by locking the parent run row (`SELECT … FOR UPDATE`)
before counting outstanding shards - without it, two shards finishing at the same
instant under READ COMMITTED each see the other still "running" and neither
aggregates (a real race that was found and fixed).

### Distributed aggregation
Counts sum; latency **averages are weighted** by each shard's sample count (not an
average of averages); **percentiles are merged** by adding the shards' histogram
bucket counts and computing p50/p95/p99 on the combined distribution (percentiles
cannot be averaged). Min-of-mins, max-of-maxs.

### Crash recovery + heartbeat/lease
A worker that dies leaves its message unacked. Peers periodically scan for messages
idle longer than `RECLAIM_MIN_IDLE` and `XCLAIM` them (taking over a shard that may
be mid-run). To tell "slow but alive" from "dead," a busy worker **refreshes its
claim every `HEARTBEAT_INTERVAL`** (`XCLAIM JUSTID`, which resets idle time without
inflating the delivery counter). So the reclaim timeout need only exceed the
heartbeat, not the job duration. A message delivered more than `MAX_DELIVERIES`
times is **dead-lettered**.

### User cancellation
`POST /test-runs/{id}/cancel` moves the run to `cancelling`. Each worker runs a
watcher goroutine that polls the run's status; on `cancelling` it cancels the
run's context - and because the runner already stops on context cancellation, the
load stops with zero new logic. Shards report partials; the barrier finalizes the
run to `cancelled`.

### Metrics (hot-path concurrency)
Each request updates shared Prometheus counters/histograms from every VU goroutine;
this is safe because the client increments **atomically**, so the lock-free per-VU
tally and the shared metrics coexist. An `active_vus` gauge is incremented on VU
spawn and decremented via `defer` on exit. Labels are kept **bounded**
(`status_class`, `method`) - never `run_id`/`url` - to avoid cardinality blow-ups;
rates/percentiles are derived at query time in PromQL.

### Safety
A **target allowlist** (`ALLOWED_TARGET_HOSTS`, deny-by-default) rejects any target
whose host isn't permitted (`403`) - blocking DDoS-for-hire and SSRF; unparseable
URLs fail closed. A **concurrent-run cap** (`MAX_CONCURRENT_RUNS`) is admission
control: once that many runs are active, new ones get `429`.

---

## 5. Request flow (a run, end to end)

```text
1. POST /test-runs            backend validates, checks allowlist + concurrency cap
2. CreateWithShards (1 tx)    insert run (status=queued, shard_count=N) + N shard rows
3. PublishShard × N           XADD one job per shard to the "testruns" stream
4. XREADGROUP                 each worker claims a shard job; StartShard flips it
                              (and the run, once) to running
5. runner.Run                 goroutine-per-VU load against the target for the duration,
                              with a heartbeat + cancel-watcher running alongside
6. CompleteShard              worker reports the shard's partial result
7. completion barrier         last shard → aggregateRun merges all shards → run terminal
8. GET /test-runs/{id}        read the aggregated result (dashboard polls this live)
```

---

## 6. Observability

The backend and every worker expose `/metrics`; Prometheus **pulls** them on a timer
(workers discovered by DNS - the scaled `worker` service name resolves to all
replicas/pods). Grafana reads Prometheus via PromQL and renders live dashboards.
Metrics are *live, aggregate, bounded-label* signals; **per-run detail lives in
Postgres** - the two are deliberately separate. The dashboard's "live" updates are
simple polling (the browser echo of the worker poll loops).

---

## 7. Deployment

**Docker Compose** - all services on one network; workers scale with
`--scale worker=N` sharing the consumer group; healthchecks + `depends_on` order
startup.

**Kubernetes** (`k8s/`) - each service is a Deployment + Service; config via
ConfigMap/Secret; Postgres on a PVC bootstrapped from a migrations ConfigMap;
liveness/readiness probes; service discovery by DNS (so the *same images* run
unchanged). The **worker pool autoscales** via a HorizontalPodAutoscaler (CPU-based,
2–8 replicas); new pods simply join the consumer group and pull queued shards - no
code change, because workers are fungible by design. A GitHub Actions workflow
(`.github/workflows/ci.yml`) builds, vets, and tests all three modules on every push.

---

## 8. Design principles

- **Every phase works end to end.** No isolated pieces; there is always a lifecycle
  you can run and inspect.
- **Postgres is the single source of truth.** Redis distributes work; Prometheus
  observes; neither owns state.
- **Guard every state transition.** Safe concurrency and idempotency come from
  `WHERE status = …` UPDATEs, not from trusting the caller.
- **Expose raw facts; combine at the edges.** Cumulative counters → rates in PromQL;
  per-shard histograms → run percentiles in the barrier. The producer stays simple.
- **Good distributed design makes the ops layer cheap.** Fungible, consumer-group
  workers are exactly what made Kubernetes autoscaling a config change rather than a
  rewrite.
- **Build the runner by hand.** It is slower and less accurate than k6/JMeter - and
  that is the point: the concurrency, measurement, and load-model lessons live there.
