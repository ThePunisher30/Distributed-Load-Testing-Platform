package runner

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// These tests drive the REAL runner (goroutines + HTTP + aggregation) against an
// in-process test server. No Docker or external network is needed: httptest spins
// up a real HTTP server inside the test process. Run with -race to also verify
// the concurrent execution path has no data races.

// TestRun_Success: against a server that always returns 200, the runner should
// do real work, and the counts must be internally consistent.
func TestRun_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	res, err := Run(context.Background(), Config{
		TargetURL:      srv.URL,
		Method:         http.MethodGet,
		VirtualUsers:   4,
		Duration:       300 * time.Millisecond,
		RequestTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if res.TotalRequests == 0 {
		t.Fatal("expected some requests, got 0")
	}
	// Core invariant: every request is counted as exactly one of success/failure.
	if res.SuccessfulRequests+res.FailedRequests != res.TotalRequests {
		t.Errorf("success(%d) + failed(%d) != total(%d)",
			res.SuccessfulRequests, res.FailedRequests, res.TotalRequests)
	}
	if res.SuccessfulRequests == 0 {
		t.Error("expected most requests to succeed against a 200 server, got 0")
	}
	// Latency invariant when we have samples: min <= avg <= max.
	if res.SuccessfulRequests > 0 && !(res.MinLatencyMs <= res.AvgLatencyMs && res.AvgLatencyMs <= res.MaxLatencyMs) {
		t.Errorf("latency invariant broken: min=%.4f avg=%.4f max=%.4f",
			res.MinLatencyMs, res.AvgLatencyMs, res.MaxLatencyMs)
	}
}

// TestRun_AllErrors: against a server that always returns 500, every request is a
// failure (status >= 400 is our failure rule), so there are zero successes.
func TestRun_AllErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	res, err := Run(context.Background(), Config{
		TargetURL:      srv.URL,
		Method:         http.MethodGet,
		VirtualUsers:   4,
		Duration:       200 * time.Millisecond,
		RequestTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if res.TotalRequests == 0 {
		t.Fatal("expected some requests, got 0")
	}
	if res.SuccessfulRequests != 0 {
		t.Errorf("expected 0 successes against a 500 server, got %d", res.SuccessfulRequests)
	}
	if res.FailedRequests != res.TotalRequests {
		t.Errorf("expected all %d requests to fail, got %d", res.TotalRequests, res.FailedRequests)
	}
}

// TestRun_SendsHeadersAndBody: the runner must apply cfg.Method, cfg.Headers, and
// cfg.Body to every request. Capturing a request's body AFTER many have been sent
// also proves the body reader is fresh per request (a shared, consumed reader would
// leave later requests with an empty body).
func TestRun_SendsHeadersAndBody(t *testing.T) {
	var mu sync.Mutex
	var gotMethod, gotHeader, gotBody string
	var sawRequest bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotMethod, gotHeader, gotBody, sawRequest = r.Method, r.Header.Get("X-Test-Token"), string(b), true
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := Run(context.Background(), Config{
		TargetURL:      srv.URL,
		Method:         http.MethodPost,
		VirtualUsers:   2,
		Duration:       200 * time.Millisecond,
		RequestTimeout: time.Second,
		Headers:        map[string]string{"X-Test-Token": "abc123"},
		Body:           `{"hello":"world"}`,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !sawRequest {
		t.Fatal("server received no request")
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method: got %q, want POST", gotMethod)
	}
	if gotHeader != "abc123" {
		t.Errorf("header X-Test-Token: got %q, want abc123", gotHeader)
	}
	if gotBody != `{"hello":"world"}` {
		t.Errorf("body: got %q, want the JSON payload", gotBody)
	}
}

// TestRun_ThinkTimeInterruptible: a think-time pause must be cut short when the
// run's duration ends, so the run never overruns its deadline by the think-time.
func TestRun_ThinkTimeInterruptible(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	start := time.Now()
	res, err := Run(context.Background(), Config{
		TargetURL:      srv.URL,
		Method:         http.MethodGet,
		VirtualUsers:   2,
		Duration:       200 * time.Millisecond,
		RequestTimeout: time.Second,
		ThinkTime:      10 * time.Second, // far longer than the run itself
	})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	// The run must end at ~Duration, not block for the 10s think-time: the pause is
	// interrupted when the run context is cancelled.
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("Run took %v; the think-time pause was not interrupted by the deadline", elapsed)
	}
	if res.TotalRequests < 1 {
		t.Errorf("expected each VU to fire at least once before pausing, got %d", res.TotalRequests)
	}
}

// TestRun_BadConfig: Run returns an error for a config it cannot start.
func TestRun_BadConfig(t *testing.T) {
	if _, err := Run(context.Background(), Config{TargetURL: "http://x", VirtualUsers: 0, Duration: time.Second}); err == nil {
		t.Error("expected an error for VirtualUsers <= 0, got nil")
	}
	if _, err := Run(context.Background(), Config{TargetURL: "", VirtualUsers: 2, Duration: time.Second}); err == nil {
		t.Error("expected an error for empty TargetURL, got nil")
	}
}
