// Command worker is the load-generating service. It consumes load-test jobs from
// a Redis stream (via a consumer group); for each job it starts that run through
// the backend, executes the load test with the runner, reports the results, then
// acknowledges the message so the broker won't redeliver it.
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"distributed-load-testing-platform/worker/internal/client"
	"distributed-load-testing-platform/worker/internal/runner"
)

const (
	streamKey     = "testruns"      // the stream the backend publishes jobs to
	groupName     = "workers"       // the consumer group all workers share
	deadStreamKey = "testruns:dead" // where jobs that keep failing are set aside
)

func main() {
	backendURL := getenv("BACKEND_URL", "http://localhost:8080")
	redisAddr := getenv("REDIS_ADDR", "localhost:6379")
	// A message unacked longer than this is presumed orphaned (its worker died)
	// and is reclaimed. CAVEAT: this MUST exceed the longest job a worker can be
	// processing. A running worker does not ack until its job finishes, so its
	// in-flight message looks "idle" the whole time; if reclaimMinIdle is shorter
	// than the job, another live worker will reclaim a job that is still running,
	// causing double execution. Idempotency (the state-guarded complete) keeps the
	// stored result correct, but the work is wasted. The proper fix is a
	// heartbeat/lease that refreshes the claim during a long job (Phase 6).
	reclaimMinIdle := getdur("RECLAIM_MIN_IDLE", 30*time.Second)
	// A message delivered more than this many times is dead-lettered instead of
	// retried again (a poison job that can never be processed).
	maxDeliveries := getint("MAX_DELIVERIES", 5)

	// Cancel the root context on Ctrl-C / SIGTERM so an in-flight run can stop.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c := client.New(backendURL)

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatalf("redis connection failed: %v", err)
	}

	// Ensure the consumer group exists (MKSTREAM also creates the stream if it
	// doesn't exist yet). "$" means the group starts from new messages only. A
	// BUSYGROUP error just means the group already exists, which is fine.
	if err := rdb.XGroupCreateMkStream(ctx, streamKey, groupName, "$").Err(); err != nil &&
		!strings.Contains(err.Error(), "BUSYGROUP") {
		log.Fatalf("create consumer group: %v", err)
	}

	// A unique consumer name within the group. Each worker must have a distinct
	// name so the group load-balances across them and tracks pending messages per
	// worker (which reclaim relies on). In a container the hostname is the unique
	// container id; PID is a fallback (it is always 1 inside a container).
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = strconv.Itoa(os.Getpid())
	}
	consumerName := "worker-" + host
	log.Printf("worker started; consuming %q from redis %s as %q", streamKey, redisAddr, consumerName)

	for {
		if ctx.Err() != nil {
			log.Println("worker shutting down")
			return
		}

		// First reclaim any message a crashed worker left unacked past the timeout.
		reclaimStuck(ctx, c, rdb, consumerName, reclaimMinIdle, maxDeliveries)

		// Block up to 5s waiting for a message not yet delivered to the group (">").
		res, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    groupName,
			Consumer: consumerName,
			Streams:  []string{streamKey, ">"},
			Count:    1,
			Block:    5 * time.Second,
		}).Result()
		if err != nil {
			// redis.Nil = the block timed out with no new message (normal when
			// idle). On shutdown the context error surfaces here too.
			if errors.Is(err, redis.Nil) || ctx.Err() != nil {
				continue
			}
			log.Printf("read from stream failed: %v", err)
			time.Sleep(time.Second) // brief backoff on an unexpected error
			continue
		}

		for _, stream := range res {
			for _, msg := range stream.Messages {
				handleMessage(ctx, c, rdb, msg, false)
			}
		}
	}
}

// reclaimStuck finds messages pending (unacked) longer than minIdle -- the
// signal that the worker holding them has died -- and either reprocesses them or,
// if they have already been delivered too many times (a poison job), dead-letters
// them. We use XPENDING (not XAUTOCLAIM) because it reports each message's
// delivery count, which is what decides retry vs dead-letter.
func reclaimStuck(ctx context.Context, c *client.Client, rdb *redis.Client, consumer string, minIdle time.Duration, maxDeliveries int) {
	pending, err := rdb.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream: streamKey,
		Group:  groupName,
		Idle:   minIdle,
		Start:  "-",
		End:    "+",
		Count:  20,
	}).Result()
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("reclaim scan (XPENDING) failed: %v", err)
		}
		return
	}

	for _, p := range pending {
		if p.RetryCount > int64(maxDeliveries) {
			deadLetter(ctx, c, rdb, p.ID, p.RetryCount)
			continue
		}

		// Claim it to this consumer, then reprocess it as a reclaimed job.
		msgs, err := rdb.XClaim(ctx, &redis.XClaimArgs{
			Stream:   streamKey,
			Group:    groupName,
			Consumer: consumer,
			MinIdle:  minIdle,
			Messages: []string{p.ID},
		}).Result()
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("reclaim (XCLAIM %s) failed: %v", p.ID, err)
			}
			continue
		}
		for _, msg := range msgs {
			log.Printf("reclaiming idle message %s (delivery %d) — its worker is presumed dead", msg.ID, p.RetryCount)
			handleMessage(ctx, c, rdb, msg, true)
		}
	}
}

// deadLetter sets aside a job that has been delivered too many times: it copies
// the message to the dead-letter stream, marks the run failed, and acks the
// original so it stops being redelivered.
func deadLetter(ctx context.Context, c *client.Client, rdb *redis.Client, msgID string, deliveries int64) {
	var runID int64
	if msgs, err := rdb.XRange(ctx, streamKey, msgID, msgID).Result(); err == nil && len(msgs) == 1 {
		if s, ok := msgs[0].Values["run_id"].(string); ok {
			runID, _ = strconv.ParseInt(s, 10, 64)
		}
	}
	log.Printf("dead-lettering message %s (run %d) after %d deliveries", msgID, runID, deliveries)

	// Keep a copy in the dead-letter stream for later inspection.
	rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: deadStreamKey,
		Values: map[string]any{
			"run_id":     strconv.FormatInt(runID, 10),
			"orig_id":    msgID,
			"deliveries": deliveries,
		},
	})

	// Mark the run failed so it doesn't linger in a non-terminal state.
	if runID > 0 {
		if err := c.FailRun(ctx, runID, "dead-lettered after too many delivery attempts"); err != nil {
			log.Printf("dead-letter: could not mark run %d failed: %v", runID, err)
		}
	}

	// Ack the original so it leaves the group's pending list for good.
	ack(ctx, rdb, msgID)
}

// handleMessage processes one job: start the run, execute it, report the result,
// then acknowledge the message. Acking removes the message from the group's
// pending list so it is not redelivered. On a transient failure we deliberately
// do NOT ack, so the message stays pending and can be retried/reclaimed later.
func handleMessage(ctx context.Context, c *client.Client, rdb *redis.Client, msg redis.XMessage, reclaimed bool) {
	idStr, _ := msg.Values["run_id"].(string)
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		// A malformed message will never succeed; ack it so it stops coming back.
		log.Printf("bad run_id %q in message %s; acking to discard", idStr, msg.ID)
		ack(ctx, rdb, msg.ID)
		return
	}

	// A reclaimed job may have been left mid-run, so it can take over a 'running'
	// run; a normal delivery only starts a 'queued' run (rejecting duplicates).
	var run *client.TestRun
	var ok bool
	if reclaimed {
		run, ok, err = c.TakeOverRun(ctx, id)
	} else {
		run, ok, err = c.StartRun(ctx, id)
	}
	if err != nil {
		// Transient (backend unreachable, etc.): leave unacked so it is retried.
		log.Printf("claim run %d failed: %v (leaving message pending for retry)", id, err)
		return
	}
	if !ok {
		// Already done (duplicate/finished) or gone: nothing to do. Ack and skip.
		// Ack it — this is idempotency in action.
		log.Printf("run %d not claimable (duplicate/finished/missing); acking", id)
		ack(ctx, rdb, msg.ID)
		return
	}

	log.Printf("claimed run %d (%q): %d VUs for %ds against %s",
		run.ID, run.Name, run.VirtualUsers, run.DurationSeconds, run.TargetURL)

	result := executeRun(ctx, run)
	if err := c.Complete(ctx, run.ID, result); err != nil {
		// Ran the load but couldn't report it: leave pending so completion retries.
		log.Printf("failed to report completion for run %d: %v (leaving pending)", run.ID, err)
		return
	}
	log.Printf("run %d reported as %s", run.ID, result.Status)

	ack(ctx, rdb, msg.ID)
}

func ack(ctx context.Context, rdb *redis.Client, msgID string) {
	if err := rdb.XAck(ctx, streamKey, groupName, msgID).Err(); err != nil {
		log.Printf("ack of message %s failed: %v", msgID, err)
	}
}

// executeRun runs the load test using the runner and maps its Result into the
// completion payload the backend expects. A run that cannot start (bad config)
// is reported as failed; a run that executed is reported as completed, even if
// some individual requests failed.
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

// getdur parses a duration env var (e.g. "30s"), falling back on unset/invalid.
func getdur(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		log.Printf("invalid %s=%q, using default %s", key, v, fallback)
	}
	return fallback
}

// getint parses an integer env var, falling back on unset/invalid.
func getint(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		log.Printf("invalid %s=%q, using default %d", key, v, fallback)
	}
	return fallback
}
