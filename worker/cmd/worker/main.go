// Command worker is the load-generating service. It repeatedly asks the backend
// for a queued test run; when it claims one, it executes the load test with the
// runner and reports the aggregated results back.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"distributed-load-testing-platform/worker/internal/client"
	"distributed-load-testing-platform/worker/internal/runner"
)

func main() {
	backendURL := getenv("BACKEND_URL", "http://localhost:8080")
	pollInterval := getdur("POLL_INTERVAL", 2*time.Second)

	// Cancel the root context on Ctrl-C / SIGTERM so an in-flight run can stop.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c := client.New(backendURL)
	log.Printf("worker started; polling %s every %s", backendURL, pollInterval)

	// Poll on a ticker. We claim at most one run per tick; if we claimed one we
	// immediately look for another (no need to wait a full interval when busy).
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		claimed := pollOnce(ctx, c)

		if !claimed {
			// Nothing to do: wait for the next tick or shutdown.
			select {
			case <-ctx.Done():
				log.Println("worker shutting down")
				return
			case <-ticker.C:
			}
		} else {
			// We just finished a run; check for shutdown, then loop right away.
			select {
			case <-ctx.Done():
				log.Println("worker shutting down")
				return
			default:
			}
		}
	}
}

// pollOnce tries to claim and execute a single run. It returns true if a run was
// claimed (whether it succeeded or failed), so the caller can decide how long to
// wait before polling again.
func pollOnce(ctx context.Context, c *client.Client) bool {
	run, claimed, err := c.ClaimNext(ctx)
	if err != nil {
		// Backend unreachable or unexpected status: log and back off via the
		// normal poll interval.
		log.Printf("claim failed: %v", err)
		return false
	}
	if !claimed {
		return false // queue empty
	}

	log.Printf("claimed run %d (%q): %d VUs for %ds against %s",
		run.ID, run.Name, run.VirtualUsers, run.DurationSeconds, run.TargetURL)

	result := executeRun(ctx, run)

	if err := c.Complete(ctx, run.ID, result); err != nil {
		log.Printf("failed to report completion for run %d: %v", run.ID, err)
		return true
	}
	log.Printf("run %d reported as %s", run.ID, result.Status)
	return true
}

// executeRun runs the claimed load test using the runner and maps its Result
// into the completion payload the backend expects. A run that cannot start (bad
// config) is reported as failed; a run that executed is reported as completed,
// even if some individual requests failed.
func executeRun(ctx context.Context, run *client.TestRun) client.CompleteRequest {
	cfg := runner.Config{
		TargetURL:      run.TargetURL,
		Method:         run.Method,
		VirtualUsers:   run.VirtualUsers,
		Duration:       time.Duration(run.DurationSeconds) * time.Second,
		RequestTimeout: 5 * time.Second,
	}

	res, err := runner.Run(ctx, cfg)
	if err != nil {
		msg := err.Error()
		return client.CompleteRequest{
			Status:       "failed",
			ErrorMessage: &msg,
		}
	}

	return client.CompleteRequest{
		Status:             "completed",
		TotalRequests:      &res.TotalRequests,
		SuccessfulRequests: &res.SuccessfulRequests,
		FailedRequests:     &res.FailedRequests,
		AvgLatencyMs:       &res.AvgLatencyMs,
		MinLatencyMs:       &res.MinLatencyMs,
		MaxLatencyMs:       &res.MaxLatencyMs,
	}
}

// getenv returns the env var named key, or fallback if unset/empty.
func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// getdur parses a duration env var (e.g. "2s"), falling back on unset/invalid.
func getdur(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		log.Printf("invalid %s=%q, using default %s", key, v, fallback)
	}
	return fallback
}
