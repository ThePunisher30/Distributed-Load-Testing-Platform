// Package runner is the worker's load engine. It takes a Config describing a
// load test, generates traffic against the target, and returns aggregated
// Result metrics. It deliberately knows nothing about the backend control
// plane, so it can be tested on its own.
package runner

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"
)

// Config is everything the runner needs to generate load.
type Config struct {
	TargetURL      string
	Method         string
	VirtualUsers   int
	Duration       time.Duration
	RequestTimeout time.Duration
}

// Result is the aggregated outcome of a run. Its fields map 1:1 onto the
// numbers the worker reports back to the backend.
type Result struct {
	TotalRequests      int64
	SuccessfulRequests int64
	FailedRequests     int64
	AvgLatencyMs       float64
	MinLatencyMs       float64
	MaxLatencyMs       float64
}

// Run executes the load test described by cfg and returns aggregated metrics.
// It returns an error only if the run could not start (bad config); a run in
// which every request failed is still a valid, non-error Result.
func Run(ctx context.Context, cfg Config) (Result, error) {
	if cfg.VirtualUsers <= 0 {
		return Result{}, errors.New("virtual users must be greater than 0")
	}
	if cfg.TargetURL == "" {
		return Result{}, errors.New("target URL must not be empty")
	}

	// runCtx stops every virtual user when the duration elapses or the parent
	// context is cancelled (whichever comes first).
	runCtx, cancel := context.WithTimeout(ctx, cfg.Duration)
	defer cancel()

	client := newHTTPClient(cfg)

	// One stats slot per VU. VU i writes only to stats[i], so the goroutines
	// never share memory and no lock is needed; the slots are merged after they
	// all finish.
	stats := make([]vuStats, cfg.VirtualUsers)

	var wg sync.WaitGroup
	for i := 0; i < cfg.VirtualUsers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			runVirtualUser(runCtx, client, cfg, &stats[idx])
		}(i)
	}
	wg.Wait()

	return mergeStats(stats), nil
}

// runVirtualUser is one virtual user's closed loop: send a request, wait for the
// response, send the next — back to back until the run context is done (its
// duration elapsed, or the worker was cancelled). Every outcome is recorded into
// this VU's own s, which no other goroutine touches.
func runVirtualUser(ctx context.Context, client *http.Client, cfg Config, s *vuStats) {
	for ctx.Err() == nil {
		doRequest(ctx, client, cfg, s)
	}
}

// doRequest performs a single HTTP request against the target and folds the
// outcome into s.
func doRequest(ctx context.Context, client *http.Client, cfg Config, s *vuStats) {
	// Each request gets its own timeout, derived from the run context so it is
	// ALSO cancelled if the run's duration ends or the worker shuts down first.
	reqCtx, cancel := context.WithTimeout(ctx, cfg.RequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, cfg.Method, cfg.TargetURL, nil)
	if err != nil {
		s.record(0, false, false) // couldn't even build the request
		return
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		s.record(0, false, false) // transport error: timeout, connection refused, etc.
		return
	}

	// Drain then close the body, or the connection can't return to the pool.
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	// NOTE: on Windows the monotonic clock is coarse — fast localhost requests
	// can measure 0 ns here, so min latency may read 0.0 in local dev. On Linux
	// (where the worker runs in its container) the resolution is nanoseconds.
	elapsedMs := float64(time.Since(start).Nanoseconds()) / 1e6
	success := resp.StatusCode < 400 // our agreed rule: >= 400 is a failure
	s.record(elapsedMs, success, true)
}

// newHTTPClient builds ONE client shared by all virtual users. Go's default
// transport caps idle connections per host at 2, which would throttle the load
// generator and make us measure our own connection churn; we raise the limits so
// every VU can keep a warm, reusable connection to the target.
func newHTTPClient(cfg Config) *http.Client {
	transport := &http.Transport{
		MaxIdleConns:        cfg.VirtualUsers * 2,
		MaxIdleConnsPerHost: cfg.VirtualUsers,
		IdleConnTimeout:     90 * time.Second,
	}
	return &http.Client{Transport: transport}
}
