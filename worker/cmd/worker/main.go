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
	streamKey = "testruns" // the stream the backend publishes jobs to
	groupName = "workers"  // the consumer group all workers share
)

func main() {
	backendURL := getenv("BACKEND_URL", "http://localhost:8080")
	redisAddr := getenv("REDIS_ADDR", "localhost:6379")

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

	// A unique consumer name within the group. With multiple workers (Phase 3),
	// the group load-balances messages across distinct consumer names.
	consumerName := "worker-" + strconv.Itoa(os.Getpid())
	log.Printf("worker started; consuming %q from redis %s as %q", streamKey, redisAddr, consumerName)

	for {
		if ctx.Err() != nil {
			log.Println("worker shutting down")
			return
		}

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
				handleMessage(ctx, c, rdb, msg)
			}
		}
	}
}

// handleMessage processes one job: start the run, execute it, report the result,
// then acknowledge the message. Acking removes the message from the group's
// pending list so it is not redelivered. On a transient failure we deliberately
// do NOT ack, so the message stays pending and can be retried/reclaimed later.
func handleMessage(ctx context.Context, c *client.Client, rdb *redis.Client, msg redis.XMessage) {
	idStr, _ := msg.Values["run_id"].(string)
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		// A malformed message will never succeed; ack it so it stops coming back.
		log.Printf("bad run_id %q in message %s; acking to discard", idStr, msg.ID)
		ack(ctx, rdb, msg.ID)
		return
	}

	run, ok, err := c.StartRun(ctx, id)
	if err != nil {
		// Transient (backend unreachable, etc.): leave unacked so it is retried.
		log.Printf("start run %d failed: %v (leaving message pending for retry)", id, err)
		return
	}
	if !ok {
		// Already started/completed (duplicate delivery) or gone: nothing to do.
		// Ack it — this is idempotency in action.
		log.Printf("run %d not startable (duplicate or missing); acking", id)
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
