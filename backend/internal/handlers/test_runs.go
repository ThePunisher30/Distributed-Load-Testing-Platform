package handlers

import (
	"distributed-load-testing-platform/backend/internal/models"
	"distributed-load-testing-platform/backend/internal/store"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// allowedMethods is the set of HTTP methods a test run may use against a target.
var allowedMethods = map[string]bool{
	http.MethodGet:    true,
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodPatch:  true,
	http.MethodDelete: true,
	http.MethodHead:   true,
}

// Guardrails so a single test can't ask for something absurd. These are
// deliberately generous for local development; Phase 8 adds real quotas.
const (
	maxVirtualUsers    = 1000
	maxDurationSeconds = 3600 // 1 hour
	maxShards          = 64
	maxHeaders         = 50
	maxBodyBytes       = 1 << 20   // 1 MiB
	maxThinkTimeMs     = 60 * 1000 // 60s between requests
)

// CreateTestRun handles POST /test-runs. It decodes and validates the request,
// inserts a queued run, and returns the created record with 201 Created.
func (h *Handler) CreateTestRun(w http.ResponseWriter, r *http.Request) {
	var req models.CreateTestRunRequest

	// DisallowUnknownFields makes typos in field names an error instead of a
	// silent no-op, which is friendlier while learning the API.
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	if msg := validateCreate(&req); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	if !h.targetAllowed(req.TargetURL) {
		writeError(w, http.StatusForbidden, "target host is not allowed")
		return
	}

	// Admission control: refuse new runs when the platform is already at its
	// concurrent-run cap (best-effort; see CountActiveRuns).
	if h.MaxConcurrentRuns > 0 {
		active, err := h.TestRuns.CountActiveRuns(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not check run capacity")
			return
		}
		if active >= h.MaxConcurrentRuns {
			writeError(w, http.StatusTooManyRequests, "too many runs in progress; try again once some finish")
			return
		}
	}

	// Fan the run out into shards (>= 1) and insert run + shards atomically.
	shardVUs := splitVUs(req.VirtualUsers, req.Shards)
	tr, shardIDs, err := h.TestRuns.CreateWithShards(r.Context(), req, shardVUs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create test run")
		return
	}

	// Publish one job per shard; the consumer group spreads them across workers.
	// The shard rows already exist in Postgres; a failed publish is logged rather
	// than failing the request (the dual-write gap, to be closed with an outbox).
	for i, sid := range shardIDs {
		if err := h.Publisher.PublishShard(r.Context(), sid); err != nil {
			log.Printf("run %d shard %d created but failed to publish: %v", tr.ID, i, err)
		}
	}

	writeJSON(w, http.StatusCreated, tr)
}

// splitVUs divides total virtual users across n shards as evenly as possible;
// the first (total mod n) shards get one extra. Callers guarantee 1 <= n <= total.
func splitVUs(total, n int) []int {
	base, rem := total/n, total%n
	out := make([]int, n)
	for i := range out {
		out[i] = base
		if i < rem {
			out[i]++
		}
	}
	return out
}

// validateCreate checks the request and normalizes it in place. It returns an
// empty string when valid, or a human-readable message describing the first
// problem found.
func validateCreate(req *models.CreateTestRunRequest) string {
	req.Name = strings.TrimSpace(req.Name)
	req.TargetURL = strings.TrimSpace(req.TargetURL)

	switch {
	case req.Name == "":
		return "name is required"
	case req.TargetURL == "":
		return "targetUrl is required"
	case !strings.HasPrefix(req.TargetURL, "http://") && !strings.HasPrefix(req.TargetURL, "https://"):
		return "targetUrl must start with http:// or https://"
	case req.VirtualUsers <= 0:
		return "virtualUsers must be greater than 0"
	case req.VirtualUsers > maxVirtualUsers:
		return "virtualUsers exceeds the maximum of " + strconv.Itoa(maxVirtualUsers)
	case req.DurationSeconds <= 0:
		return "durationSeconds must be greater than 0"
	case req.DurationSeconds > maxDurationSeconds:
		return "durationSeconds exceeds the maximum of " + strconv.Itoa(maxDurationSeconds)
	}

	// Default and normalize the HTTP method.
	if req.Method == "" {
		req.Method = http.MethodGet
	}
	req.Method = strings.ToUpper(req.Method)
	if !allowedMethods[req.Method] {
		return "method must be one of GET, POST, PUT, PATCH, DELETE, HEAD"
	}

	// Default and validate the shard count (how many workers the run splits over).
	if req.Shards == 0 {
		req.Shards = 1
	}
	switch {
	case req.Shards < 1:
		return "shards must be at least 1"
	case req.Shards > maxShards:
		return "shards exceeds the maximum of " + strconv.Itoa(maxShards)
	case req.Shards > req.VirtualUsers:
		return "shards cannot exceed virtualUsers (each shard needs at least one VU)"
	}

	// Phase 5: optional request shape.
	if len(req.Headers) > maxHeaders {
		return "too many headers (max " + strconv.Itoa(maxHeaders) + ")"
	}
	if len(req.Body) > maxBodyBytes {
		return "body exceeds the maximum of " + strconv.Itoa(maxBodyBytes) + " bytes"
	}
	if req.ThinkTimeMs < 0 {
		return "thinkTimeMs must not be negative"
	}
	if req.ThinkTimeMs > maxThinkTimeMs {
		return "thinkTimeMs exceeds the maximum of " + strconv.Itoa(maxThinkTimeMs)
	}

	return ""
}

// ListTestRuns handles GET /test-runs. It returns recent runs, newest first, for
// the dashboard's run list. An optional ?limit (default 50, max 200) caps the count.
func (h *Handler) ListTestRuns(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}

	runs, err := h.TestRuns.List(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list test runs")
		return
	}

	writeJSON(w, http.StatusOK, runs)
}

// GetTestRun handles GET /test-runs/{id}. It parses the id, looks the run up,
// and returns it, or 404 if it does not exist.
func (h *Handler) GetTestRun(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id must be a positive integer")
		return
	}

	tr, err := h.TestRuns.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "test run not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not fetch test run")
		return
	}

	writeJSON(w, http.StatusOK, tr)
}

// CancelTestRun handles POST /test-runs/{id}/cancel. It asks a queued or running
// run to stop: the run moves to "cancelling", workers polling its status wind
// down, and the completion barrier finalizes it to "cancelled" (keeping any
// partial results). Cancelling an already-finished run is a no-op that returns
// the run unchanged.
func (h *Handler) CancelTestRun(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id must be a positive integer")
		return
	}

	tr, err := h.TestRuns.CancelRun(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "test run not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not cancel test run")
		return
	}

	writeJSON(w, http.StatusOK, tr)
}

func (h *Handler) targetAllowed(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return h.AllowedTargetHosts[host]

}
