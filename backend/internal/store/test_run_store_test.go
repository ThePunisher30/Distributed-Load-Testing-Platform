package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	// Registers the "pgx" driver with database/sql for sql.Open("pgx", ...).
	_ "github.com/jackc/pgx/v5/stdlib"

	"distributed-load-testing-platform/backend/internal/models"
)

// These tests exercise real SQL against a real Postgres, so they need a database.
// Point TEST_DATABASE_URL at a TEST database (they TRUNCATE the table):
//
//	docker compose up -d postgres
//	$env:TEST_DATABASE_URL = "postgres://dltp:dltp@localhost:5432/dltp_test?sslmode=disable"
//	go test ./internal/store/
//
// With TEST_DATABASE_URL unset, they skip, so `go test ./...` stays green.

// schemaDDL mirrors migrations/001_create_test_runs.sql so a fresh test database
// is self-bootstrapping. IF NOT EXISTS makes it harmless to run repeatedly.
const schemaDDL = `
CREATE TABLE IF NOT EXISTS test_runs (
    id               BIGSERIAL PRIMARY KEY,
    name             TEXT        NOT NULL,
    target_url       TEXT        NOT NULL,
    method           TEXT        NOT NULL DEFAULT 'GET',
    virtual_users    INTEGER     NOT NULL CHECK (virtual_users > 0),
    duration_seconds INTEGER     NOT NULL CHECK (duration_seconds > 0),
    status           TEXT        NOT NULL DEFAULT 'queued'
                     CHECK (status IN ('queued', 'running', 'completed', 'failed')),
    total_requests      BIGINT,
    successful_requests BIGINT,
    failed_requests     BIGINT,
    avg_latency_ms      DOUBLE PRECISION,
    min_latency_ms      DOUBLE PRECISION,
    max_latency_ms      DOUBLE PRECISION,
    error_message       TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at       TIMESTAMPTZ,
    completed_at     TIMESTAMPTZ
);`

// newTestStore opens the test database, ensures the schema, and truncates the
// table so each test starts from a clean, predictable state (ids reset to 1).
func newTestStore(t *testing.T) *TestRunStore {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping store tests (needs Postgres)")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping db (is postgres running?): %v", err)
	}
	if _, err := db.ExecContext(ctx, schemaDDL); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	if _, err := db.ExecContext(ctx, "TRUNCATE test_runs RESTART IDENTITY"); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	return NewTestRunStore(db)
}

func sampleCreate(name string) models.CreateTestRunRequest {
	return models.CreateTestRunRequest{
		Name:            name,
		TargetURL:       "http://target:8081/fast",
		Method:          "GET",
		VirtualUsers:    10,
		DurationSeconds: 5,
	}
}

func ptr[T any](v T) *T { return &v }

func TestStore_CreateAndGet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	created, err := s.Create(ctx, sampleCreate("first"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == 0 {
		t.Error("expected a non-zero id")
	}
	if created.Status != models.StatusQueued {
		t.Errorf("new run status = %q, want %q", created.Status, models.StatusQueued)
	}
	if created.VirtualUsers != 10 || created.DurationSeconds != 5 || created.Method != "GET" {
		t.Errorf("config not stored correctly: %+v", created)
	}
	if created.TotalRequests != nil || created.StartedAt != nil || created.CompletedAt != nil {
		t.Error("a queued run should have no results or start/complete times yet")
	}
	if created.CreatedAt.IsZero() {
		t.Error("createdAt should be set")
	}

	got, err := s.GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != created.ID || got.Name != "first" {
		t.Errorf("GetByID returned %+v, want id=%d name=first", got, created.ID)
	}

	if _, err := s.GetByID(ctx, 999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID(missing) error = %v, want ErrNotFound", err)
	}
}

func TestStore_ClaimNext(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Empty queue.
	if _, err := s.ClaimNext(ctx); !errors.Is(err, ErrNoQueuedRuns) {
		t.Errorf("ClaimNext(empty) error = %v, want ErrNoQueuedRuns", err)
	}

	// Two queued runs; the sleep guarantees distinct created_at ordering (FIFO).
	r1, _ := s.Create(ctx, sampleCreate("older"))
	time.Sleep(5 * time.Millisecond)
	r2, _ := s.Create(ctx, sampleCreate("newer"))

	first, err := s.ClaimNext(ctx)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if first.ID != r1.ID {
		t.Errorf("ClaimNext returned id=%d, want oldest id=%d (FIFO)", first.ID, r1.ID)
	}
	if first.Status != models.StatusRunning {
		t.Errorf("claimed run status = %q, want %q", first.Status, models.StatusRunning)
	}
	if first.StartedAt == nil {
		t.Error("claimed run should have startedAt set")
	}

	second, err := s.ClaimNext(ctx)
	if err != nil {
		t.Fatalf("ClaimNext (2nd): %v", err)
	}
	if second.ID != r2.ID {
		t.Errorf("second claim id=%d, want %d", second.ID, r2.ID)
	}

	// Queue drained again.
	if _, err := s.ClaimNext(ctx); !errors.Is(err, ErrNoQueuedRuns) {
		t.Errorf("ClaimNext(drained) error = %v, want ErrNoQueuedRuns", err)
	}
}

func TestStore_StartByID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	run, _ := s.Create(ctx, sampleCreate("to-start"))

	// A queued run starts and becomes running.
	started, err := s.StartByID(ctx, run.ID)
	if err != nil {
		t.Fatalf("StartByID: %v", err)
	}
	if started.Status != models.StatusRunning {
		t.Errorf("status = %q, want running", started.Status)
	}
	if started.StartedAt == nil {
		t.Error("startedAt should be set")
	}

	// Starting it AGAIN must fail: it is no longer queued. This is the
	// idempotency guard that makes duplicate Redis delivery safe in Phase 2.
	if _, err := s.StartByID(ctx, run.ID); !errors.Is(err, ErrNotQueued) {
		t.Errorf("double-start error = %v, want ErrNotQueued", err)
	}

	// Starting a non-existent run must fail with ErrNotFound.
	if _, err := s.StartByID(ctx, 999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("start-missing error = %v, want ErrNotFound", err)
	}
}

func TestStore_TakeOver(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// TakeOver accepts a queued run.
	q, _ := s.Create(ctx, sampleCreate("queued-takeover"))
	if got, err := s.TakeOver(ctx, q.ID); err != nil || got.Status != models.StatusRunning {
		t.Errorf("TakeOver(queued) = (%v, %v), want running, nil", got, err)
	}

	// TakeOver also accepts an already-running run (the crash-recovery case):
	// the previous worker died mid-execution, so we re-claim it.
	r, _ := s.Create(ctx, sampleCreate("running-takeover"))
	if _, err := s.StartByID(ctx, r.ID); err != nil { // now running
		t.Fatalf("StartByID: %v", err)
	}
	if got, err := s.TakeOver(ctx, r.ID); err != nil || got.Status != models.StatusRunning {
		t.Errorf("TakeOver(running) = (%v, %v), want running, nil", got, err)
	}

	// A completed run is left alone: nothing to re-run.
	done, _ := s.Create(ctx, sampleCreate("done-takeover"))
	s.StartByID(ctx, done.ID)
	s.Complete(ctx, done.ID, models.CompleteTestRunRequest{
		Status: models.StatusCompleted, TotalRequests: ptr(int64(1)),
		SuccessfulRequests: ptr(int64(1)), FailedRequests: ptr(int64(0)),
	})
	if _, err := s.TakeOver(ctx, done.ID); !errors.Is(err, ErrAlreadyDone) {
		t.Errorf("TakeOver(completed) error = %v, want ErrAlreadyDone", err)
	}

	// A missing run is ErrNotFound.
	if _, err := s.TakeOver(ctx, 999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("TakeOver(missing) error = %v, want ErrNotFound", err)
	}
}

func TestStore_Complete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	completed := models.CompleteTestRunRequest{
		Status:             models.StatusCompleted,
		TotalRequests:      ptr(int64(1000)),
		SuccessfulRequests: ptr(int64(990)),
		FailedRequests:     ptr(int64(10)),
		AvgLatencyMs:       ptr(4.5),
		MinLatencyMs:       ptr(1.0),
		MaxLatencyMs:       ptr(20.0),
	}

	// A claimed (running) run can be completed.
	run, _ := s.Create(ctx, sampleCreate("to-complete"))
	if _, err := s.ClaimNext(ctx); err != nil { // moves it to running
		t.Fatalf("ClaimNext: %v", err)
	}

	done, err := s.Complete(ctx, run.ID, completed)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if done.Status != models.StatusCompleted {
		t.Errorf("status = %q, want completed", done.Status)
	}
	if done.TotalRequests == nil || *done.TotalRequests != 1000 {
		t.Errorf("totalRequests not stored: %+v", done.TotalRequests)
	}
	if done.CompletedAt == nil {
		t.Error("completedAt should be set")
	}

	// Completing it AGAIN must fail: it is no longer running (idempotency guard —
	// this is what protects against duplicate delivery in Phase 2).
	if _, err := s.Complete(ctx, run.ID, completed); !errors.Is(err, ErrNotRunning) {
		t.Errorf("double-complete error = %v, want ErrNotRunning", err)
	}

	// Completing a queued (never-claimed) run must fail with ErrNotRunning.
	queued, _ := s.Create(ctx, sampleCreate("never-claimed"))
	if _, err := s.Complete(ctx, queued.ID, completed); !errors.Is(err, ErrNotRunning) {
		t.Errorf("complete-queued error = %v, want ErrNotRunning", err)
	}

	// Completing a non-existent run must fail with ErrNotFound.
	if _, err := s.Complete(ctx, 999999, completed); !errors.Is(err, ErrNotFound) {
		t.Errorf("complete-missing error = %v, want ErrNotFound", err)
	}
}
