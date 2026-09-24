-- 002_create_test_run_shards.sql
--
-- Phase 3 Level 1: split one run across workers.
--
-- A run is fanned out into shard_count shards. Each shard runs a slice of the
-- run's virtual users on whichever worker picks it up, and reports a partial
-- result. When every shard has finished, the backend aggregates the partials
-- into the run's result columns and marks the run completed (or failed).
--
-- Every run has at least one shard: a normal (unsplit) run is a 1-shard run, so
-- the worker always processes shards and there is a single code path.

ALTER TABLE test_runs
    ADD COLUMN IF NOT EXISTS shard_count INT NOT NULL DEFAULT 1;

CREATE TABLE IF NOT EXISTS test_run_shards (
    id             BIGSERIAL PRIMARY KEY,
    run_id         BIGINT  NOT NULL REFERENCES test_runs(id) ON DELETE CASCADE,
    shard_index    INT     NOT NULL,          -- 0 .. shard_count-1
    virtual_users  INT     NOT NULL CHECK (virtual_users > 0),

    -- Lifecycle of this one shard (mirrors the run's states).
    status         TEXT    NOT NULL DEFAULT 'queued'
                   CHECK (status IN ('queued', 'running', 'completed', 'failed')),
    error_message  TEXT,

    -- Partial results for this shard (NULL until it finishes). latency_count is
    -- how many requests produced a latency sample; it lets the backend compute a
    -- correct weighted average across shards, not an average of averages.
    total_requests      BIGINT,
    successful_requests BIGINT,
    failed_requests     BIGINT,
    latency_count       BIGINT,
    avg_latency_ms      DOUBLE PRECISION,
    min_latency_ms      DOUBLE PRECISION,
    max_latency_ms      DOUBLE PRECISION,

    started_at     TIMESTAMPTZ,
    completed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),

    UNIQUE (run_id, shard_index)
);

-- Workers claim shards by (run_id) and the completion barrier counts a run's
-- shards by status, so index the run_id.
CREATE INDEX IF NOT EXISTS idx_test_run_shards_run_id
    ON test_run_shards (run_id);
