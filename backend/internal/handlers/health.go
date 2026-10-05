// Package handlers holds the backend's HTTP handlers.
package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"distributed-load-testing-platform/backend/internal/queue"
	"distributed-load-testing-platform/backend/internal/store"
)

// Handler carries the dependencies our HTTP handlers need: the database pool
// (for the health ping), the stores that own each table's queries, the queue
// publisher for enqueuing jobs, and the target allowlist (the set of hosts a run
// is permitted to send load at).
type Handler struct {
	DB                 *sql.DB
	TestRuns           *store.TestRunStore
	Publisher          *queue.Publisher
	AllowedTargetHosts map[string]bool
	// MaxConcurrentRuns caps how many runs may be active (non-terminal) at once;
	// 0 means no limit.
	MaxConcurrentRuns int
}

// New builds a Handler with its dependencies. allowedHosts is the set of hostnames
// a test run may target (deny by default); maxConcurrent caps simultaneous active
// runs (0 = unlimited).
func New(db *sql.DB, publisher *queue.Publisher, allowedHosts map[string]bool, maxConcurrent int) *Handler {
	return &Handler{
		DB:                 db,
		TestRuns:           store.NewTestRunStore(db),
		Publisher:          publisher,
		AllowedTargetHosts: allowedHosts,
		MaxConcurrentRuns:  maxConcurrent,
	}
}

// Routes wires up the URL paths this handler serves. Go 1.22+ ServeMux supports
// method + path patterns and {id} wildcards directly. The mux is wrapped in CORS
// so the dashboard can call the API from a browser (see corsMiddleware).
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", h.Health)

	// Phase 4: expose Prometheus metrics for scraping. promhttp.Handler() serves
	// the default registry (Go runtime metrics now; our custom load metrics get
	// registered there too once we add them).
	mux.Handle("GET /metrics", promhttp.Handler())

	// Public API (used by end users and the dashboard).
	mux.HandleFunc("POST /test-runs", h.CreateTestRun)
	mux.HandleFunc("GET /test-runs", h.ListTestRuns)
	mux.HandleFunc("GET /test-runs/{id}", h.GetTestRun)
	mux.HandleFunc("POST /test-runs/{id}/cancel", h.CancelTestRun)

	// Internal API (used by workers).
	// Phase 2: workers receive a job id from Redis and call /start for that id.
	// /workers/claim (the Phase 1 SKIP-LOCKED poll) is kept as a fallback.
	mux.HandleFunc("POST /internal/workers/claim", h.ClaimNextRun)
	mux.HandleFunc("POST /internal/test-runs/{id}/start", h.StartTestRun)
	mux.HandleFunc("POST /internal/test-runs/{id}/takeover", h.TakeoverTestRun)
	mux.HandleFunc("POST /internal/test-runs/{id}/fail", h.FailTestRun)
	mux.HandleFunc("POST /internal/test-runs/{id}/complete", h.CompleteTestRun)

	// Phase 3 Level 1: a run is split into shards; workers drive shards.
	mux.HandleFunc("POST /internal/shards/{id}/start", h.StartShard)
	mux.HandleFunc("POST /internal/shards/{id}/takeover", h.TakeoverShard)
	mux.HandleFunc("POST /internal/shards/{id}/complete", h.CompleteShard)
	mux.HandleFunc("POST /internal/shards/{id}/fail", h.FailShard)

	return corsMiddleware(mux)
}

// corsMiddleware adds permissive CORS headers so a browser dashboard (served from
// a different origin in dev) can call the API, and answers preflight OPTIONS
// requests. In production the dashboard is served same-origin via an nginx proxy,
// so this is mainly a convenience for running the Vite dev server directly.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
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
