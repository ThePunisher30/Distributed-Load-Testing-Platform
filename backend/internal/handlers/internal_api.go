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

// FailTestRun handles POST /internal/test-runs/{id}/fail. A worker calls it when
// dead-lettering a job that could not be processed after too many attempts, to
// mark the run failed with a reason no matter what state it was in.
func (h *Handler) FailTestRun(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id must be a positive integer")
		return
	}

	var req struct {
		ErrorMessage string `json:"errorMessage"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if req.ErrorMessage == "" {
		req.ErrorMessage = "run failed"
	}

	tr, err := h.TestRuns.Fail(r.Context(), id, req.ErrorMessage)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "test run not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not fail test run")
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

// ---- Phase 3 Level 1: shard endpoints (a run is split into shards) ----

// StartShard handles POST /internal/shards/{id}/start. Claims a queued shard and
// returns everything the worker needs to run it. A duplicate delivery of an
// already-started shard gets 409.
func (h *Handler) StartShard(w http.ResponseWriter, r *http.Request) {
	id, ok := shardID(w, r)
	if !ok {
		return
	}
	a, err := h.TestRuns.StartShard(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "shard not found")
	case errors.Is(err, store.ErrNotQueued):
		writeError(w, http.StatusConflict, "shard is not queued")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "could not start shard")
	default:
		writeJSON(w, http.StatusOK, a)
	}
}

// TakeoverShard handles POST /internal/shards/{id}/takeover. Reclaim path: takes
// over a shard that may already be running (its worker is presumed dead).
func (h *Handler) TakeoverShard(w http.ResponseWriter, r *http.Request) {
	id, ok := shardID(w, r)
	if !ok {
		return
	}
	a, err := h.TestRuns.TakeOverShard(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "shard not found")
	case errors.Is(err, store.ErrAlreadyDone):
		writeError(w, http.StatusConflict, "shard is already completed or failed")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "could not take over shard")
	default:
		writeJSON(w, http.StatusOK, a)
	}
}

// CompleteShard handles POST /internal/shards/{id}/complete. Records a shard's
// partial result; the store aggregates the run when its last shard finishes.
func (h *Handler) CompleteShard(w http.ResponseWriter, r *http.Request) {
	id, ok := shardID(w, r)
	if !ok {
		return
	}
	var req models.CompleteShardRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if req.Status != models.StatusCompleted && req.Status != models.StatusFailed {
		writeError(w, http.StatusBadRequest, `status must be "completed" or "failed"`)
		return
	}

	err := h.TestRuns.CompleteShard(r.Context(), id, req)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "shard not found")
	case errors.Is(err, store.ErrNotRunning):
		writeError(w, http.StatusConflict, "shard is not running")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "could not complete shard")
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// FailShard handles POST /internal/shards/{id}/fail. Dead-letter path: mark a
// shard failed with a reason regardless of its current state.
func (h *Handler) FailShard(w http.ResponseWriter, r *http.Request) {
	id, ok := shardID(w, r)
	if !ok {
		return
	}
	var req struct {
		ErrorMessage string `json:"errorMessage"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if req.ErrorMessage == "" {
		req.ErrorMessage = "shard failed"
	}

	err := h.TestRuns.FailShard(r.Context(), id, req.ErrorMessage)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "shard not found")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "could not fail shard")
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// shardID parses and validates the {id} path value shared by the shard handlers.
func shardID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id must be a positive integer")
		return 0, false
	}
	return id, true
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
