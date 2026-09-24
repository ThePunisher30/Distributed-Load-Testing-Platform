// Package store is the data-access layer. It translates between the database
// and our domain models, keeping SQL out of the HTTP handlers.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"distributed-load-testing-platform/backend/internal/models"
)

// ErrNotFound is returned when a lookup finds no matching row. Handlers map this
// to an HTTP 404.
var ErrNotFound = errors.New("test run not found")

// ErrNoQueuedRuns is returned by ClaimNext when the queue is empty. This is a
// normal, expected condition (the worker asks constantly), not an error the
// caller should log loudly.
var ErrNoQueuedRuns = errors.New("no queued test runs")

// ErrNotRunning is returned when a completion is attempted on a run that exists
// but is not currently in the running state. Handlers map this to a 409.
var ErrNotRunning = errors.New("test run is not running")

// ErrNotQueued is returned by StartByID when a run exists but is not queued
// (e.g. a duplicate job delivery for a run already started). Handlers map this
// to a 409.
var ErrNotQueued = errors.New("test run is not queued")

// ErrAlreadyDone is returned by TakeOver when a reclaimed run is already
// completed or failed, so there is nothing to re-run. Handlers map this to 409.
var ErrAlreadyDone = errors.New("test run is already completed or failed")

// TestRunStore runs queries against the test_runs table.
type TestRunStore struct {
	db *sql.DB
}

// NewTestRunStore builds a store backed by the given database pool.
func NewTestRunStore(db *sql.DB) *TestRunStore {
	return &TestRunStore{db: db}
}

// testRunColumns is the column list, in the order scanRow expects. Sharing it
// keeps Create and GetByID in sync.
const testRunColumns = `
	id, name, target_url, method, virtual_users, duration_seconds, status, shard_count,
	total_requests, successful_requests, failed_requests,
	avg_latency_ms, min_latency_ms, max_latency_ms, error_message,
	created_at, started_at, completed_at`

// scanRow reads one row (from Query or QueryRow) into a TestRun. Nullable
// columns scan into the model's pointer fields, which become nil on SQL NULL.
func scanRow(row interface{ Scan(...any) error }) (*models.TestRun, error) {
	var tr models.TestRun
	err := row.Scan(
		&tr.ID, &tr.Name, &tr.TargetURL, &tr.Method, &tr.VirtualUsers,
		&tr.DurationSeconds, &tr.Status, &tr.ShardCount,
		&tr.TotalRequests, &tr.SuccessfulRequests, &tr.FailedRequests,
		&tr.AvgLatencyMs, &tr.MinLatencyMs, &tr.MaxLatencyMs, &tr.ErrorMessage,
		&tr.CreatedAt, &tr.StartedAt, &tr.CompletedAt,
	)
	if err != nil {
		return nil, err
	}
	return &tr, nil
}

// Create inserts a new queued test run and returns the full row, including the
// database-assigned id, status, and created_at. RETURNING lets us do the insert
// and read-back in a single round trip.
func (s *TestRunStore) Create(ctx context.Context, req models.CreateTestRunRequest) (*models.TestRun, error) {
	query := `
		INSERT INTO test_runs (name, target_url, method, virtual_users, duration_seconds)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING ` + testRunColumns

	row := s.db.QueryRowContext(ctx, query,
		req.Name, req.TargetURL, req.Method, req.VirtualUsers, req.DurationSeconds,
	)

	tr, err := scanRow(row)
	if err != nil {
		return nil, fmt.Errorf("insert test run: %w", err)
	}
	return tr, nil
}

// CreateWithShards inserts a new queued run split into len(shardVUs) shards (one
// per entry, carrying that many virtual users) and returns the run plus the new
// shard ids in order. The run and its shards are inserted in one transaction, so
// a run never exists without its shards.
func (s *TestRunStore) CreateWithShards(ctx context.Context, req models.CreateTestRunRequest, shardVUs []int) (*models.TestRun, []int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() // no-op once Commit has succeeded

	run, err := scanRow(tx.QueryRowContext(ctx, `
		INSERT INTO test_runs (name, target_url, method, virtual_users, duration_seconds, shard_count)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+testRunColumns,
		req.Name, req.TargetURL, req.Method, req.VirtualUsers, req.DurationSeconds, len(shardVUs)))
	if err != nil {
		return nil, nil, fmt.Errorf("insert run: %w", err)
	}

	shardIDs := make([]int64, len(shardVUs))
	for i, vus := range shardVUs {
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO test_run_shards (run_id, shard_index, virtual_users) VALUES ($1, $2, $3) RETURNING id`,
			run.ID, i, vus,
		).Scan(&shardIDs[i]); err != nil {
			return nil, nil, fmt.Errorf("insert shard %d: %w", i, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit: %w", err)
	}
	return run, shardIDs, nil
}

// GetByID returns one test run, or ErrNotFound if no row has that id.
func (s *TestRunStore) GetByID(ctx context.Context, id int64) (*models.TestRun, error) {
	query := `SELECT ` + testRunColumns + ` FROM test_runs WHERE id = $1`

	tr, err := scanRow(s.db.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get test run: %w", err)
	}
	return tr, nil
}

// ClaimNext atomically claims the oldest queued run for a worker: it flips the
// row to "running", stamps started_at, and returns it. Returns ErrNoQueuedRuns
// when the queue is empty.
//
// The pattern is the heart of a database-backed job queue:
//
//		UPDATE ... WHERE id = (SELECT ... FOR UPDATE SKIP LOCKED LIMIT 1)
//
//	  - The inner SELECT locks the single oldest queued row so no other worker can
//	    grab it (FOR UPDATE).
//	  - SKIP LOCKED means a second worker running at the same instant steps over
//	    the locked row and takes the next one instead of blocking.
//	  - Doing the select-and-update as one statement makes the claim atomic: two
//	    workers can never claim the same run.
//
// We have one worker today, but writing it correctly now means adding workers in
// Phase 3 needs no change here.
func (s *TestRunStore) ClaimNext(ctx context.Context) (*models.TestRun, error) {
	query := `
		UPDATE test_runs
		SET status = 'running', started_at = now()
		WHERE id = (
			SELECT id FROM test_runs
			WHERE status = 'queued'
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING ` + testRunColumns

	tr, err := scanRow(s.db.QueryRowContext(ctx, query))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoQueuedRuns
	}
	if err != nil {
		return nil, fmt.Errorf("claim next test run: %w", err)
	}
	return tr, nil
}

// StartByID transitions one specific queued run to running and returns it. It is
// the Phase 2 counterpart of ClaimNext: instead of the worker polling for the
// next queued run, the broker hands it a run id, and the worker starts THAT run.
//
// The `WHERE status = 'queued'` guard makes it idempotent — the key property for
// at-least-once delivery. If the broker delivers the same job twice, the second
// StartByID matches no row (the run is already running/completed), so we return
// ErrNotQueued and the worker knows to skip it. A missing id returns ErrNotFound.
func (s *TestRunStore) StartByID(ctx context.Context, id int64) (*models.TestRun, error) {
	query := `
		UPDATE test_runs
		SET status = 'running', started_at = now()
		WHERE id = $1 AND status = 'queued'
		RETURNING ` + testRunColumns

	tr, err := scanRow(s.db.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		// Nothing updated: distinguish "does not exist" from "not queued".
		if _, getErr := s.GetByID(ctx, id); errors.Is(getErr, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, ErrNotQueued
	}
	if err != nil {
		return nil, fmt.Errorf("start test run: %w", err)
	}
	return tr, nil
}

// TakeOver claims a run whose Redis message was reclaimed after being idle too
// long (its previous worker is presumed dead). Unlike StartByID, it accepts a
// run that is already 'running' and re-stamps it, so a worker that crashed
// mid-execution can be recovered. A run that is already 'completed' or 'failed'
// is left alone (ErrAlreadyDone); a missing run is ErrNotFound.
//
// This is deliberately more permissive than StartByID: it is only ever called
// for a message that has sat unacked past the reclaim timeout, which is the
// signal that the run really is orphaned rather than actively in progress.
func (s *TestRunStore) TakeOver(ctx context.Context, id int64) (*models.TestRun, error) {
	query := `
		UPDATE test_runs
		SET status = 'running', started_at = now()
		WHERE id = $1 AND status IN ('queued', 'running')
		RETURNING ` + testRunColumns

	tr, err := scanRow(s.db.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		if _, getErr := s.GetByID(ctx, id); errors.Is(getErr, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, ErrAlreadyDone // completed or failed already
	}
	if err != nil {
		return nil, fmt.Errorf("take over test run: %w", err)
	}
	return tr, nil
}

// Fail marks a run as failed with a reason, regardless of whether it was queued
// or running. It is used to dead-letter a job that could not be processed after
// too many delivery attempts. A run that is already terminal (completed/failed)
// is returned unchanged; a missing run is ErrNotFound.
func (s *TestRunStore) Fail(ctx context.Context, id int64, reason string) (*models.TestRun, error) {
	query := `
		UPDATE test_runs
		SET status = 'failed', error_message = $2, completed_at = now()
		WHERE id = $1 AND status IN ('queued', 'running')
		RETURNING ` + testRunColumns

	tr, err := scanRow(s.db.QueryRowContext(ctx, query, id, reason))
	if errors.Is(err, sql.ErrNoRows) {
		// Nothing updated: already terminal, or missing.
		existing, getErr := s.GetByID(ctx, id)
		if getErr != nil {
			return nil, getErr // ErrNotFound or a real error
		}
		return existing, nil // already completed/failed; leave as-is
	}
	if err != nil {
		return nil, fmt.Errorf("fail test run: %w", err)
	}
	return tr, nil
}

// Complete records the outcome of a run and returns the updated row. The status
// must be "completed" or "failed". The WHERE guard (status = 'running') ensures
// only a claimed, in-progress run can be completed; if nothing is updated we
// distinguish "does not exist" (ErrNotFound) from "wrong state" (ErrNotRunning).
func (s *TestRunStore) Complete(ctx context.Context, id int64, req models.CompleteTestRunRequest) (*models.TestRun, error) {
	query := `
		UPDATE test_runs
		SET status = $2,
			total_requests = $3,
			successful_requests = $4,
			failed_requests = $5,
			avg_latency_ms = $6,
			min_latency_ms = $7,
			max_latency_ms = $8,
			error_message = $9,
			completed_at = now()
		WHERE id = $1 AND status = 'running'
		RETURNING ` + testRunColumns

	row := s.db.QueryRowContext(ctx, query,
		id, req.Status,
		req.TotalRequests, req.SuccessfulRequests, req.FailedRequests,
		req.AvgLatencyMs, req.MinLatencyMs, req.MaxLatencyMs, req.ErrorMessage,
	)

	tr, err := scanRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		// The update matched nothing. Look the row up to give a precise reason.
		if _, getErr := s.GetByID(ctx, id); errors.Is(getErr, ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, ErrNotRunning
	}
	if err != nil {
		return nil, fmt.Errorf("complete test run: %w", err)
	}
	return tr, nil
}

// ---- Phase 3 Level 1: shard lifecycle ----

// shardMiss classifies a "no row updated" for a shard: ErrNotFound if the shard
// does not exist, otherwise notThisState (its status did not match the guard).
func (s *TestRunStore) shardMiss(ctx context.Context, shardID int64, notThisState error) error {
	var exists bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM test_run_shards WHERE id = $1)`, shardID).Scan(&exists); err != nil {
		return fmt.Errorf("check shard: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return notThisState
}

// startShard moves a shard to running (guarded by wantQueuedOrRunning) and
// returns everything a worker needs to run it, joining the run for its config.
// It also flips the run to running the first time any of its shards starts.
func (s *TestRunStore) startShard(ctx context.Context, shardID int64, allowRunning bool, miss error) (*models.ShardAssignment, error) {
	guard := "sh.status = 'queued'"
	if allowRunning {
		guard = "sh.status IN ('queued', 'running')"
	}
	a := models.ShardAssignment{ShardID: shardID}
	err := s.db.QueryRowContext(ctx, `
		UPDATE test_run_shards sh
		SET status = 'running', started_at = now()
		FROM test_runs r
		WHERE sh.id = $1 AND r.id = sh.run_id AND `+guard+`
		RETURNING sh.run_id, sh.shard_index, sh.virtual_users, r.target_url, r.method, r.duration_seconds`,
		shardID,
	).Scan(&a.RunID, &a.ShardIndex, &a.VirtualUsers, &a.TargetURL, &a.Method, &a.DurationSeconds)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, s.shardMiss(ctx, shardID, miss)
	}
	if err != nil {
		return nil, fmt.Errorf("start shard: %w", err)
	}
	// First shard to start flips the run to running (idempotent via the guard).
	if _, err := s.db.ExecContext(ctx,
		`UPDATE test_runs SET status = 'running', started_at = now() WHERE id = $1 AND status = 'queued'`,
		a.RunID); err != nil {
		return nil, fmt.Errorf("mark run running: %w", err)
	}
	return &a, nil
}

// StartShard claims a queued shard (normal delivery). A non-queued shard is
// rejected as ErrNotQueued (a duplicate delivery), a missing one as ErrNotFound.
func (s *TestRunStore) StartShard(ctx context.Context, shardID int64) (*models.ShardAssignment, error) {
	return s.startShard(ctx, shardID, false, ErrNotQueued)
}

// TakeOverShard claims a shard whose message was reclaimed after going idle. It
// accepts an already-running shard (its previous worker is presumed dead); an
// already-finished shard is ErrAlreadyDone, a missing one ErrNotFound.
func (s *TestRunStore) TakeOverShard(ctx context.Context, shardID int64) (*models.ShardAssignment, error) {
	return s.startShard(ctx, shardID, true, ErrAlreadyDone)
}

// aggregateRun combines every shard's partial into the run's result and marks
// the run terminal. The WHERE status = 'running' guard makes it the completion
// barrier: only the shard that finishes last actually writes the run result.
func aggregateRun(ctx context.Context, tx *sql.Tx, runID int64) error {
	var total, success, failed, latCount int64
	var sumLat float64
	var minMs, maxMs sql.NullFloat64
	var failedShards int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(total_requests), 0), COALESCE(SUM(successful_requests), 0),
		       COALESCE(SUM(failed_requests), 0), COALESCE(SUM(latency_count), 0),
		       COALESCE(SUM(avg_latency_ms * latency_count), 0),
		       MIN(min_latency_ms) FILTER (WHERE latency_count > 0),
		       MAX(max_latency_ms) FILTER (WHERE latency_count > 0),
		       COUNT(*) FILTER (WHERE status = 'failed')
		FROM test_run_shards WHERE run_id = $1`,
		runID,
	).Scan(&total, &success, &failed, &latCount, &sumLat, &minMs, &maxMs, &failedShards); err != nil {
		return fmt.Errorf("aggregate shards: %w", err)
	}

	var avg float64
	if latCount > 0 {
		avg = sumLat / float64(latCount) // weighted across shards, not avg-of-avgs
	}
	status := models.StatusCompleted
	var errMsg any
	if failedShards > 0 {
		status = models.StatusFailed
		errMsg = fmt.Sprintf("%d shard(s) failed", failedShards)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE test_runs
		SET status = $2, total_requests = $3, successful_requests = $4, failed_requests = $5,
		    avg_latency_ms = $6, min_latency_ms = $7, max_latency_ms = $8,
		    error_message = $9, completed_at = now()
		WHERE id = $1 AND status = 'running'`,
		runID, status, total, success, failed, avg, minMs.Float64, maxMs.Float64, errMsg,
	); err != nil {
		return fmt.Errorf("aggregate run: %w", err)
	}
	return nil
}

// finishShard records a shard as terminal and, if it was the run's last
// outstanding shard, aggregates the run. Used by both complete and fail so the
// completion barrier logic lives in one place.
func (s *TestRunStore) finishShard(ctx context.Context, shardID int64, set string, miss error, args ...any) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var runID int64
	if err := tx.QueryRowContext(ctx, set, args...).Scan(&runID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return s.shardMiss(ctx, shardID, miss)
		}
		return fmt.Errorf("finish shard: %w", err)
	}

	var remaining int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM test_run_shards WHERE run_id = $1 AND status IN ('queued', 'running')`,
		runID).Scan(&remaining); err != nil {
		return fmt.Errorf("count shards: %w", err)
	}
	if remaining == 0 {
		if err := aggregateRun(ctx, tx, runID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CompleteShard records a running shard's partial result. If it is the run's
// last shard, the run is aggregated and marked terminal.
func (s *TestRunStore) CompleteShard(ctx context.Context, shardID int64, req models.CompleteShardRequest) error {
	const q = `
		UPDATE test_run_shards
		SET status = $2, error_message = $3,
		    total_requests = $4, successful_requests = $5, failed_requests = $6,
		    latency_count = $7, avg_latency_ms = $8, min_latency_ms = $9, max_latency_ms = $10,
		    completed_at = now()
		WHERE id = $1 AND status = 'running'
		RETURNING run_id`
	return s.finishShard(ctx, shardID, q, ErrNotRunning,
		shardID, req.Status, req.ErrorMessage,
		req.TotalRequests, req.SuccessfulRequests, req.FailedRequests,
		req.LatencyCount, req.AvgLatencyMs, req.MinLatencyMs, req.MaxLatencyMs)
}

// FailShard marks a shard failed (dead-letter). It accepts a queued or running
// shard; an already-terminal shard is a no-op. If it was the run's last shard,
// the run is aggregated and marked terminal.
func (s *TestRunStore) FailShard(ctx context.Context, shardID int64, reason string) error {
	const q = `
		UPDATE test_run_shards
		SET status = 'failed', error_message = $2, completed_at = now()
		WHERE id = $1 AND status IN ('queued', 'running')
		RETURNING run_id`
	err := s.finishShard(ctx, shardID, q, nil, shardID, reason)
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	return err // nil miss -> already-terminal shard is a no-op success
}
