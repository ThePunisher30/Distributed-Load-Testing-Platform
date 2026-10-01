-- 004_add_latency_percentiles.sql
--
-- Phase 5: latency percentiles (p50/p95/p99).
--
-- Percentiles cannot be averaged across shards, so each shard stores its latency
-- HISTOGRAM (the bucket counts, as a JSON array). When the completion barrier
-- fires, the backend merges the shards' histograms (element-wise add) and computes
-- the run's percentiles from the combined counts, which it stores on test_runs.

ALTER TABLE test_run_shards
    ADD COLUMN IF NOT EXISTS latency_buckets JSONB;

ALTER TABLE test_runs
    ADD COLUMN IF NOT EXISTS p50_latency_ms DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS p95_latency_ms DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS p99_latency_ms DOUBLE PRECISION;
