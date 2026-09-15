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

// readBody reads a response body for error messages, ignoring read errors.
func readBody(r io.Reader) string {
	b, _ := io.ReadAll(io.LimitReader(r, 2048))
	return string(bytes.TrimSpace(b))
}
