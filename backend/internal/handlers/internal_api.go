package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"distributed-load-testing-platform/backend/internal/models"
	"distributed-load-testing-platform/backend/internal/store"
)

// These endpoints are for workers, not end users. They live under /internal to
// signal that separation; Phase 8 will lock them down with authentication.

// ClaimNextRun handles POST /internal/workers/claim. It atomically claims the
// oldest queued run and returns it. When the queue is empty it returns 204 No
// Content so the worker knows there is simply nothing to do right now.
func (h *Handler) ClaimNextRun(w http.ResponseWriter, r *http.Request) {
	tr, err := h.TestRuns.ClaimNext(r.Context())
	if errors.Is(err, store.ErrNoQueuedRuns) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not claim a test run")
		return
	}

	writeJSON(w, http.StatusOK, tr)
}

// StartTestRun handles POST /internal/test-runs/{id}/start. A worker calls it
// after receiving a job from the queue, to atomically move that run to running
// and get back its config. A duplicate delivery of an already-started run yields
// 409 Conflict, telling the worker to skip it.
func (h *Handler) StartTestRun(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id must be a positive integer")
		return
	}

	tr, err := h.TestRuns.StartByID(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "test run not found")
		return
	case errors.Is(err, store.ErrNotQueued):
		writeError(w, http.StatusConflict, "test run is not queued")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "could not start test run")
		return
	}

	writeJSON(w, http.StatusOK, tr)
}

// TakeoverTestRun handles POST /internal/test-runs/{id}/takeover. A worker calls
// it when it reclaims an idle (orphaned) message, to take over a run that may
// already be 'running' from a crashed worker. An already-finished run yields 409.
func (h *Handler) TakeoverTestRun(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id must be a positive integer")
		return
	}

	tr, err := h.TestRuns.TakeOver(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "test run not found")
		return
	case errors.Is(err, store.ErrAlreadyDone):
		writeError(w, http.StatusConflict, "test run is already completed or failed")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "could not take over test run")
		return
	}

	writeJSON(w, http.StatusOK, tr)
}

// CompleteTestRun handles POST /internal/test-runs/{id}/complete. A worker calls
// it to report the outcome of a run it previously claimed.
func (h *Handler) CompleteTestRun(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id must be a positive integer")
		return
	}

	var req models.CompleteTestRunRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}

	if msg := validateComplete(&req); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	tr, err := h.TestRuns.Complete(r.Context(), id, req)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "test run not found")
		return
	case errors.Is(err, store.ErrNotRunning):
		writeError(w, http.StatusConflict, "test run is not in the running state")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "could not complete test run")
		return
	}

	writeJSON(w, http.StatusOK, tr)
}

// validateComplete checks a completion request. A "completed" run must carry its
// request counts; a "failed" run must explain why.
func validateComplete(req *models.CompleteTestRunRequest) string {
	switch req.Status {
	case models.StatusCompleted:
		if req.TotalRequests == nil || req.SuccessfulRequests == nil || req.FailedRequests == nil {
			return "a completed run requires totalRequests, successfulRequests, and failedRequests"
		}
	case models.StatusFailed:
		if req.ErrorMessage == nil || *req.ErrorMessage == "" {
			return "a failed run requires a non-empty errorMessage"
		}
	default:
		return `status must be "completed" or "failed"`
	}
	return ""
}
