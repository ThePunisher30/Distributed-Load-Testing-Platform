-- 003_add_request_shape.sql
--
-- Phase 5: richer request shape. A run can now carry custom request headers and
-- a request body (so POST/PUT/PATCH tests send real payloads, not just GETs).
--
-- headers is JSONB (a small {"Header-Name": "value"} object); body is the raw
-- request payload. Both are part of the run's configuration, so they live on
-- test_runs and flow to each worker through the shard assignment.

ALTER TABLE test_runs
    ADD COLUMN IF NOT EXISTS headers JSONB,
    ADD COLUMN IF NOT EXISTS body    TEXT;
