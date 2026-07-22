// Package handlers holds the backend's HTTP handlers.
package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"distributed-load-testing-platform/backend/internal/store"
)

// Handler carries the dependencies our HTTP handlers need: the database pool
// (for the health ping) and the stores that own each table's queries.
type Handler struct {
	DB       *sql.DB
	TestRuns *store.TestRunStore
}

// New builds a Handler with its dependencies.
func New(db *sql.DB) *Handler {
	return &Handler{
		DB:       db,
		TestRuns: store.NewTestRunStore(db),
	}
}

// Routes wires up the URL paths this handler serves and returns a mux. Go 1.22+
// ServeMux supports method + path patterns and {id} wildcards directly.
func (h *Handler) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.Health)

	// Public API (used by end users).
	mux.HandleFunc("POST /test-runs", h.CreateTestRun)
	mux.HandleFunc("GET /test-runs/{id}", h.GetTestRun)

	// Internal API (used by workers).
	mux.HandleFunc("POST /internal/workers/claim", h.ClaimNextRun)
	mux.HandleFunc("POST /internal/test-runs/{id}/complete", h.CompleteTestRun)

	return mux
}

// Health reports whether the backend is up AND whether it can reach the
// database. A liveness check that ignored the database would happily report
// "ok" while every real request failed, so we ping Postgres here.
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	dbStatus := "ok"
	statusCode := http.StatusOK

	if err := h.DB.PingContext(ctx); err != nil {
		dbStatus = "unreachable"
		statusCode = http.StatusServiceUnavailable
	}

	writeJSON(w, statusCode, map[string]string{
		"service":  "backend",
		"status":   map[bool]string{true: "ok", false: "degraded"}[statusCode == http.StatusOK],
		"database": dbStatus,
	})
}
