-- 005_add_think_time.sql
--
-- Phase 5: think-time. Each virtual user can pause this many milliseconds between
-- requests, turning the pure back-to-back closed loop into a realistic, paced one
-- (users who read/think between actions). 0 means no pause (original behavior).

ALTER TABLE test_runs
    ADD COLUMN IF NOT EXISTS think_time_ms INT NOT NULL DEFAULT 0;
