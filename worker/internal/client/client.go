// Package client is the worker's HTTP client for the backend's internal API.
// It knows how to claim the next queued run and report a run's outcome.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Client talks to the backend control plane.
type Client struct {
	baseURL string
	http    *http.Client
}

// New builds a Client pointed at baseURL (e.g. "http://localhost:8080").
func New(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// TestRun holds the fields of a claimed run the worker needs to execute it. It
// is a subset of the backend's model (the worker is a separate module, so it
// defines only what it uses).
type TestRun struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	TargetURL       string `json:"targetUrl"`
	Method          string `json:"method"`
	VirtualUsers    int    `json:"virtualUsers"`
	DurationSeconds int    `json:"durationSeconds"`
}

// CompleteRequest is the outcome payload sent to the backend. Numeric fields are
// pointers so a failed run can omit them. Field names match the backend's API.
type CompleteRequest struct {
	Status             string   `json:"status"`
	TotalRequests      *int64   `json:"totalRequests,omitempty"`
	SuccessfulRequests *int64   `json:"successfulRequests,omitempty"`
	FailedRequests     *int64   `json:"failedRequests,omitempty"`
	AvgLatencyMs       *float64 `json:"avgLatencyMs,omitempty"`
	MinLatencyMs       *float64 `json:"minLatencyMs,omitempty"`
	MaxLatencyMs       *float64 `json:"maxLatencyMs,omitempty"`
	ErrorMessage       *string  `json:"errorMessage,omitempty"`
}

// ShardAssignment is what the backend returns when the worker starts/takes over a
// shard: the slice of load this worker should run.
type ShardAssignment struct {
	ShardID         int64  `json:"shardId"`
	RunID           int64  `json:"runId"`
	ShardIndex      int    `json:"shardIndex"`
	TargetURL       string `json:"targetUrl"`
	Method          string `json:"method"`
	VirtualUsers    int    `json:"virtualUsers"`
	DurationSeconds int    `json:"durationSeconds"`
}

// CompleteShardRequest is the partial result the worker reports for one shard.
type CompleteShardRequest struct {
	Status             string   `json:"status"`
	TotalRequests      *int64   `json:"totalRequests,omitempty"`
	SuccessfulRequests *int64   `json:"successfulRequests,omitempty"`
	FailedRequests     *int64   `json:"failedRequests,omitempty"`
	LatencyCount       *int64   `json:"latencyCount,omitempty"`
	AvgLatencyMs       *float64 `json:"avgLatencyMs,omitempty"`
	MinLatencyMs       *float64 `json:"minLatencyMs,omitempty"`
	MaxLatencyMs       *float64 `json:"maxLatencyMs,omitempty"`
	ErrorMessage       *string  `json:"errorMessage,omitempty"`
}

// ClaimNext asks the backend for the next queued run. The bool return is
// "claimed": false with a nil run means the queue is empty (HTTP 204), which is
// a normal idle state, not an error.
func (c *Client) ClaimNext(ctx context.Context) (*TestRun, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/internal/workers/claim", nil)
	if err != nil {
		return nil, false, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("claim request failed: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil, false, nil // queue empty
	case http.StatusOK:
		var run TestRun
		if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
			return nil, false, fmt.Errorf("decode claimed run: %w", err)
		}
		return &run, true, nil
	default:
		return nil, false, fmt.Errorf("claim returned unexpected status %s: %s", resp.Status, readBody(resp.Body))
	}
}

// StartRun asks the backend to start a specific run by id (Phase 2: the worker
// gets the id from Redis, then starts that run). The bool is "started": false
// means the run was not startable — already started/completed (a duplicate
// delivery) or not found — which the worker should ack and skip, not retry.
func (c *Client) StartRun(ctx context.Context, id int64) (*TestRun, bool, error) {
	url := c.baseURL + "/internal/test-runs/" + strconv.FormatInt(id, 10) + "/start"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, false, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("start request failed: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var run TestRun
		if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
			return nil, false, fmt.Errorf("decode started run: %w", err)
		}
		return &run, true, nil
	case http.StatusConflict, http.StatusNotFound:
		return nil, false, nil // not startable: duplicate delivery or gone
	default:
		return nil, false, fmt.Errorf("start returned unexpected status %s: %s", resp.Status, readBody(resp.Body))
	}
}

// TakeOverRun claims a run whose message this worker reclaimed after it sat idle
// (its previous worker is presumed dead). Unlike StartRun it succeeds even if the
// run is already "running". The bool is "taken": false means the run is already
// completed/failed or gone, so the worker should ack and skip.
func (c *Client) TakeOverRun(ctx context.Context, id int64) (*TestRun, bool, error) {
	url := c.baseURL + "/internal/test-runs/" + strconv.FormatInt(id, 10) + "/takeover"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, false, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("takeover request failed: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var run TestRun
		if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
			return nil, false, fmt.Errorf("decode taken-over run: %w", err)
		}
		return &run, true, nil
	case http.StatusConflict, http.StatusNotFound:
		return nil, false, nil // already done or gone
	default:
		return nil, false, fmt.Errorf("takeover returned unexpected status %s: %s", resp.Status, readBody(resp.Body))
	}
}

// FailRun marks a run as failed with a reason. The worker calls it when
// dead-lettering a job that could not be processed after too many attempts.
func (c *Client) FailRun(ctx context.Context, id int64, reason string) error {
	payload, err := json.Marshal(map[string]string{"errorMessage": reason})
	if err != nil {
		return err
	}

	url := c.baseURL + "/internal/test-runs/" + strconv.FormatInt(id, 10) + "/fail"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("fail request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fail returned status %s: %s", resp.Status, readBody(resp.Body))
	}
	return nil
}

// Complete reports the outcome of a run the worker executed.
func (c *Client) Complete(ctx context.Context, id int64, body CompleteRequest) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	url := c.baseURL + "/internal/test-runs/" + strconv.FormatInt(id, 10) + "/complete"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("complete request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("complete returned status %s: %s", resp.Status, readBody(resp.Body))
	}
	return nil
}

// claimShard is the shared POST-and-decode for start/takeover. started=false
// means the shard was not claimable (already done / gone) -> ack and skip.
func (c *Client) claimShard(ctx context.Context, path string, shardID int64) (*ShardAssignment, bool, error) {
	url := c.baseURL + "/internal/shards/" + strconv.FormatInt(shardID, 10) + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, false, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("shard %s request failed: %w", path, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var a ShardAssignment
		if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
			return nil, false, fmt.Errorf("decode shard assignment: %w", err)
		}
		return &a, true, nil
	case http.StatusConflict, http.StatusNotFound:
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("shard %s returned %s: %s", path, resp.Status, readBody(resp.Body))
	}
}

// StartShard claims a queued shard (normal delivery).
func (c *Client) StartShard(ctx context.Context, shardID int64) (*ShardAssignment, bool, error) {
	return c.claimShard(ctx, "/start", shardID)
}

// TakeOverShard claims a reclaimed shard, taking over one that may be running.
func (c *Client) TakeOverShard(ctx context.Context, shardID int64) (*ShardAssignment, bool, error) {
	return c.claimShard(ctx, "/takeover", shardID)
}

// CompleteShard reports a shard's partial result.
func (c *Client) CompleteShard(ctx context.Context, shardID int64, body CompleteShardRequest) error {
	return c.postJSON(ctx, "/internal/shards/"+strconv.FormatInt(shardID, 10)+"/complete", body)
}

// FailShard marks a shard failed (dead-letter path).
func (c *Client) FailShard(ctx context.Context, shardID int64, reason string) error {
	return c.postJSON(ctx, "/internal/shards/"+strconv.FormatInt(shardID, 10)+"/fail",
		map[string]string{"errorMessage": reason})
}

// postJSON POSTs body as JSON and treats any non-200 as an error.
func (c *Client) postJSON(ctx context.Context, path string, body any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s request failed: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s: %s", path, resp.Status, readBody(resp.Body))
	}
	return nil
}

// readBody reads a response body for error messages, ignoring read errors.
func readBody(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 2048))
	return string(bytes.TrimSpace(b))
}
