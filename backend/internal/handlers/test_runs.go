package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"distributed-load-testing-platform/backend/internal/models"
	"distributed-load-testing-platform/backend/internal/store"
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

	return ""
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
