# Learning Goals

This project exists to teach **distributed-systems design by building it** - a real
load-testing platform, every mechanism hand-written rather than imported. This doc
is the retrospective map: the concepts it covers, where each one lives in the code,
and the one insight worth keeping from each. (The narrative is in
[PROGRESS.md](PROGRESS.md); the system is in [ARCHITECTURE.md](ARCHITECTURE.md).)

## Service boundaries & the job lifecycle
*Where:* `backend/` (control plane), `worker/` (execution), `target/`, PostgreSQL;
the `status` columns on `test_runs` / `test_run_shards`.
Separating "decide what work exists" from "do the work" is what makes the system
distributable. A run is a **job with explicit state** (`queued → running →
completed/failed`, plus `cancelling/cancelled`), and every transition is a
**guarded SQL UPDATE** (`WHERE status = …`). *Insight: idempotency isn't a feature
you add - it falls out of guarding every state change, which is what makes
at-least-once delivery safe.*

## Asynchronous work distribution
*Where:* `backend/internal/queue`, `worker/cmd/worker/main.go`, Redis Streams.
Polling (Phase 1) vs a **message broker** (Phase 2): `XADD`/`XREADGROUP`/`XACK` with
a **consumer group** so each job goes to exactly one worker. *Insight: push beats
pull for latency, and a consumer group turns "add workers" into free horizontal
scale - a decision that paid off twice later (k8s autoscaling needed no code).*

## Concurrency
*Where:* `worker/internal/runner/` (`Run`, `runVirtualUser`, `stats.go`,
`metrics.go`).
Goroutine-per-virtual-user in a closed loop; **race-free aggregation** by giving
each VU its own tally and merging after `WaitGroup.Wait()` (no locks); context
cancellation and per-request timeouts; a tuned shared `http.Client`. Later the hot
path also updates **atomic** shared Prometheus counters. *Insight: give each
goroutine its own memory and merge at the end, and correctness is free - but once a
reader (the scrape) needs live numbers, you need atomics; a plain `count++` is a
data race.*

## Sharding & distributed aggregation
*Where:* `store.CreateWithShards`, `aggregateRun`, `runPercentiles`,
`worker.../runner` histogram.
Split one run into N shards (atomic fan-out), run them in parallel, recombine with a
**completion barrier**. Counts sum; latency averages are **weighted** by sample
count; percentiles are merged by **adding histograms**, not averaging percentiles.
*Insight: splitting is the easy half - recombining correctly is the hard half, and a
concurrent barrier must be serialized (a `SELECT … FOR UPDATE` on the run) or two
simultaneous finishers both skip aggregation (a race this project hit and fixed).*

## Failure handling
*Where:* `reclaimStuck`, `heartbeat`, `deadLetter`, `watchCancel`; k8s probes.
At-least-once delivery → **reclaim** idle (orphaned) messages; a **heartbeat/lease**
(`XCLAIM JUSTID`) so a slow-but-alive worker isn't mistaken for dead; **dead-letter**
poison jobs; **cancellation** routed through the context the runner already respects;
and k8s **self-healing** (the backend restart-loops until Postgres is ready).
*Insight: a distributed system is defined by what it does when something breaks -
and "slow vs dead" is undecidable without a heartbeat.*

## Measurement you can trust
*Where:* `runner/histogram.go`, `store/histogram.go`.
avg/min/max hide the tail; **percentiles** (p50/p95/p99) describe real experience.
You can't keep every latency (memory) or derive p99 from an average, so you keep a
fixed **log-spaced histogram** - a deliberate **memory-vs-accuracy** trade, and the
one structure that also *merges* exactly across shards. *Insight: pick the data
structure for the question; a histogram answers "percentiles" cheaply and
mergeably.*

## Observability
*Where:* `/metrics` endpoints, `monitoring/`, Prometheus/Grafana (Compose + k8s).
**Pull-based** metrics (Prometheus scrapes `/metrics`); the three metric types
(counter/gauge/histogram); **cardinality** as a hard constraint (labels must be
bounded - `status_class`/`method`, never `run_id`/`url`); rates/percentiles derived
at query time. *Insight: metrics are live, aggregate, bounded-label trends;
per-entity detail belongs in the database or logs - don't make labels do the
database's job.*

## Load-model design
*Where:* `runner.Config` (`ThinkTime`), `runVirtualUser`.
Closed-loop (fire back-to-back - a stress test) vs adding **think-time** (paced,
realistic users). *Insight: the load model decides what question you're answering -
"what's the breaking point?" vs "can we serve N realistic users?"*

## Safety & multi-tenancy
*Where:* `handlers.targetAllowed`, `CountActiveRuns`, `CreateTestRun`.
An open load tester is a weapon, so: a **deny-by-default allowlist** (blocks
DDoS/SSRF; allowlist > blocklist; fail closed) and **admission control** (a
concurrent-run cap returning `429`). *Insight: secure-by-default means the default
config is restrictive and you opt in - and you can't enumerate every bad input, only
the permitted ones.*

## Deployment & scaling
*Where:* `docker-compose.yml`, `k8s/`, `.github/workflows/ci.yml`.
From Compose to **Kubernetes**: Deployments/Services, DNS service discovery,
liveness/readiness probes, ConfigMap/Secret config, a PVC, and a
**HorizontalPodAutoscaler** scaling the worker pool on CPU. Plus CI. *Insight:
declarative desired-state + self-healing is the real-infra model, and good upstream
design (fungible workers) makes autoscaling a config change, not a rewrite.*

## Systems-language concurrency (side experiment)
A standalone **C++ twin** of the runner (kept local, off the repo) explored
`std::thread`, `std::mutex`/`lock_guard` (RAII), `std::atomic`, and compare-and-swap
- the same concurrency ideas as the Go runner, without a garbage collector, to feel
the difference.

---

## The guiding method

> Build the smallest version of a concept first, prove it end to end, then make it
> realistic.

`polling worker → broker-backed queue → many workers → sharding → failure recovery
→ observability → safety → orchestration`. Each step left the whole system runnable
and inspectable, which is what kept a nine-phase build understandable.
