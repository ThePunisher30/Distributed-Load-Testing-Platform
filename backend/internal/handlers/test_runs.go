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

	tr, err := h.TestRuns.Create(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create test run")
		return
	}

	// Publish the job so a worker picks it up immediately. The queued row already
	// exists in Postgres; if publishing fails, the run is created but won't be
	// delivered until we add a fallback (the dual-write gap Phase 2 will address
	// with an outbox). Log it rather than failing the request.
	if err := h.Publisher.PublishJob(r.Context(), tr.ID); err != nil {
		log.Printf("run %d created but failed to publish job: %v", tr.ID, err)
	}

	writeJSON(w, http.StatusCreated, tr)
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
