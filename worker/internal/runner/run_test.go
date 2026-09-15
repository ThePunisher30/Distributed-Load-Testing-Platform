package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
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

// TestRun_BadConfig: Run returns an error for a config it cannot start.
func TestRun_BadConfig(t *testing.T) {
	if _, err := Run(context.Background(), Config{TargetURL: "http://x", VirtualUsers: 0, Duration: time.Second}); err == nil {
		t.Error("expected an error for VirtualUsers <= 0, got nil")
	}
	if _, err := Run(context.Background(), Config{TargetURL: "", VirtualUsers: 2, Duration: time.Second}); err == nil {
		t.Error("expected an error for empty TargetURL, got nil")
	}
}
