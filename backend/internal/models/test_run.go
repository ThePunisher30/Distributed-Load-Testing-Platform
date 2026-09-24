// Package models defines the core data types shared across the backend.
package models

import "time"

// Status values a test run moves through over its lifetime.
const (
	StatusQueued    = "queued"    // created, waiting for a worker
	StatusRunning   = "running"   // claimed by a worker, load in progress
	StatusCompleted = "completed" // finished successfully, results populated
	StatusFailed    = "failed"    // could not be completed
)

// TestRun mirrors one row of the test_runs table. It carries both the test
// configuration (set at creation) and the aggregated results (filled in by the
// worker when the run finishes).
//
// Result fields are pointers so they can be null: a queued run has no results
// yet, and JSON `omitempty` hides them until they exist.
type TestRun struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	TargetURL       string `json:"targetUrl"`
	Method          string `json:"method"`
	VirtualUsers    int    `json:"virtualUsers"`
	DurationSeconds int    `json:"durationSeconds"`
	ShardCount      int    `json:"shardCount"`
	Status          string `json:"status"`

	// Results (nil until the run completes).
	TotalRequests      *int64   `json:"totalRequests,omitempty"`
	SuccessfulRequests *int64   `json:"successfulRequests,omitempty"`
	FailedRequests     *int64   `json:"failedRequests,omitempty"`
	AvgLatencyMs       *float64 `json:"avgLatencyMs,omitempty"`
	MinLatencyMs       *float64 `json:"minLatencyMs,omitempty"`
	MaxLatencyMs       *float64 `json:"maxLatencyMs,omitempty"`
	ErrorMessage       *string  `json:"errorMessage,omitempty"`

	// Lifecycle timestamps.
	CreatedAt   time.Time  `json:"createdAt"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

// CreateTestRunRequest is the JSON body accepted by POST /test-runs. It contains
// only the fields a user supplies; the server fills in id, status, timestamps,
// and results.
type CreateTestRunRequest struct {
	Name            string `json:"name"`
	TargetURL       string `json:"targetUrl"`
	Method          string `json:"method"`
	VirtualUsers    int    `json:"virtualUsers"`
	DurationSeconds int    `json:"durationSeconds"`
	// Shards is how many workers the run is split across (default 1 = unsplit).
	// The virtual users are divided as evenly as possible across the shards.
	Shards int `json:"shards"`
}

// ShardAssignment is what the backend returns when a worker starts (or takes
// over) a shard: everything the worker needs to run that slice of the load.
type ShardAssignment struct {
	ShardID         int64  `json:"shardId"`
	RunID           int64  `json:"runId"`
	ShardIndex      int    `json:"shardIndex"`
	TargetURL       string `json:"targetUrl"`
	Method          string `json:"method"`
	VirtualUsers    int    `json:"virtualUsers"`
	DurationSeconds int    `json:"durationSeconds"`
}

// CompleteShardRequest is the partial result a worker reports for one shard.
// LatencyCount travels with AvgLatencyMs so the backend can compute a correct
// weighted average across shards.
type CompleteShardRequest struct {
	Status             string   `json:"status"`
	TotalRequests      *int64   `json:"totalRequests"`
	SuccessfulRequests *int64   `json:"successfulRequests"`
	FailedRequests     *int64   `json:"failedRequests"`
	LatencyCount       *int64   `json:"latencyCount"`
	AvgLatencyMs       *float64 `json:"avgLatencyMs"`
	MinLatencyMs       *float64 `json:"minLatencyMs"`
	MaxLatencyMs       *float64 `json:"maxLatencyMs"`
	ErrorMessage       *string  `json:"errorMessage"`
}

// CompleteTestRunRequest is the JSON body a worker sends to report the outcome
// of a run it claimed. Status is "completed" (results populated) or "failed"
// (errorMessage explains why). Numeric fields are pointers so a failed run can
// omit them.
type CompleteTestRunRequest struct {
	Status             string   `json:"status"`
	TotalRequests      *int64   `json:"totalRequests"`
	SuccessfulRequests *int64   `json:"successfulRequests"`
	FailedRequests     *int64   `json:"failedRequests"`
	AvgLatencyMs       *float64 `json:"avgLatencyMs"`
	MinLatencyMs       *float64 `json:"minLatencyMs"`
	MaxLatencyMs       *float64 `json:"maxLatencyMs"`
	ErrorMessage       *string  `json:"errorMessage"`
}
