-- 006_add_cancel_states.sql
--
-- Phase 6: user-initiated cancellation. A run can be cancelled while queued or
-- running: it moves to 'cancelling' (workers notice and stop early), then the
-- completion barrier finalizes it to 'cancelled' (keeping any partial results).
--
-- The status CHECK constraint on test_runs was created inline in migration 001,
-- so Postgres auto-named it test_runs_status_check. Drop and re-add it with the
-- two new states. Shards still only ever reach completed/failed, so their
-- constraint is unchanged.

ALTER TABLE test_runs DROP CONSTRAINT IF EXISTS test_runs_status_check;
ALTER TABLE test_runs ADD CONSTRAINT test_runs_status_check
    CHECK (status IN ('queued', 'running', 'completed', 'failed', 'cancelling', 'cancelled'));
