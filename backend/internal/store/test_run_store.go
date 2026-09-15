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
	id, name, target_url, method, virtual_users, duration_seconds, status,
	total_requests, successful_requests, failed_requests,
	avg_latency_ms, min_latency_ms, max_latency_ms, error_message,
	created_at, started_at, completed_at`

// scanRow reads one row (from Query or QueryRow) into a TestRun. Nullable
// columns scan into the model's pointer fields, which become nil on SQL NULL.
func scanRow(row interface{ Scan(...any) error }) (*models.TestRun, error) {
	var tr models.TestRun
	err := row.Scan(
		&tr.ID, &tr.Name, &tr.TargetURL, &tr.Method, &tr.VirtualUsers,
		&tr.DurationSeconds, &tr.Status,
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
