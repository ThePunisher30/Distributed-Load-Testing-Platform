-- Migration 001: create the test_runs table
--
-- A test_run is the central record of the platform. It holds both the
-- configuration of a load test (what to send) and, once finished, the
-- aggregated results (what happened). The backend creates rows here, a
-- worker claims one, runs the load, then writes results back.

CREATE TABLE IF NOT EXISTS test_runs (
    id               BIGSERIAL PRIMARY KEY,

    -- Test configuration (provided when the run is created).
    name             TEXT        NOT NULL,
    target_url       TEXT        NOT NULL,
    method           TEXT        NOT NULL DEFAULT 'GET',
    virtual_users    INTEGER     NOT NULL CHECK (virtual_users > 0),
    duration_seconds INTEGER     NOT NULL CHECK (duration_seconds > 0),

    -- Lifecycle state:
    --   queued    -> created, waiting for a worker
    --   running   -> claimed by a worker, load in progress
    --   completed -> finished successfully, results populated
    --   failed    -> could not be completed
    status           TEXT        NOT NULL DEFAULT 'queued'
                     CHECK (status IN ('queued', 'running', 'completed', 'failed')),

    -- Aggregated results (populated by the worker when the run finishes).
    total_requests      BIGINT,
    successful_requests BIGINT,
    failed_requests     BIGINT,
    avg_latency_ms      DOUBLE PRECISION,
    min_latency_ms      DOUBLE PRECISION,
    max_latency_ms      DOUBLE PRECISION,
    error_message       TEXT,

    -- Timestamps for lifecycle tracking.
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at       TIMESTAMPTZ,
    completed_at     TIMESTAMPTZ
);

-- Workers repeatedly look for the oldest queued run to claim, so index the
-- columns that query touches.
CREATE INDEX IF NOT EXISTS idx_test_runs_status_created_at
    ON test_runs (status, created_at);
