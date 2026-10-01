// Command worker is the load-generating service. It consumes load-test jobs from
// a Redis stream (via a consumer group); for each job it starts that run through
// the backend, executes the load test with the runner, reports the results, then
// acknowledges the message so the broker won't redeliver it.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
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
	// A message whose idle time exceeds this is presumed orphaned (its worker
	// died) and is reclaimed. A live worker refreshes its claim every
	// heartbeatInterval (see heartbeat()), so its in-flight message never looks
	// idle while the worker is alive. This therefore only has to exceed the
	// heartbeat interval (plus margin), NOT the job duration. (Phase 6 fix: before
	// the heartbeat this had to outlast the longest job, or a slow-but-alive worker
	// got reclaimed and its job ran twice.)
	reclaimMinIdle := getdur("RECLAIM_MIN_IDLE", 15*time.Second)
	// A message delivered more than this many times is dead-lettered instead of
	// retried again (a poison job that can never be processed).
	maxDeliveries := getint("MAX_DELIVERIES", 5)
	// Phase 6: while processing a job, the worker refreshes its claim this often
	// (a heartbeat/lease) so the message never looks idle while the worker is
	// alive. reclaimMinIdle now only needs to exceed this interval, not the job
	// duration -- a dead worker simply stops heartbeating and is reclaimed.
	heartbeatInterval := getdur("HEARTBEAT_INTERVAL", 2*time.Second)

	// Cancel the root context on Ctrl-C / SIGTERM so an in-flight run can stop.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	c := client.New(backendURL)

	// Phase 4: unlike the backend, the worker has no HTTP server of its own, so we
	// run a small side server just to expose /metrics for Prometheus to scrape.
	// It runs alongside the consume loop and is shut down when the context is done.
	metricsSrv := startMetricsServer(getenv("METRICS_ADDR", ":9100"))
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = metricsSrv.Shutdown(shutdownCtx)
	}()

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
		reclaimStuck(ctx, c, rdb, consumerName, reclaimMinIdle, maxDeliveries, heartbeatInterval)

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
				handleMessage(ctx, c, rdb, msg, false, consumerName, heartbeatInterval)
			}
		}
	}
}

// heartbeat keeps this worker's claim on msgID fresh while a job runs: every
// interval it re-claims the message to itself (resetting its idle time, without
// bumping the delivery counter), proving the worker is still alive. It returns
// when ctx is cancelled (the job finished, or the worker is shutting down).
func heartbeat(ctx context.Context, rdb *redis.Client, consumer, msgID string, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			err := rdb.XClaimJustID(ctx, &redis.XClaimArgs{
				Stream:   streamKey,
				Group:    groupName,
				Consumer: consumer,
				MinIdle:  0, // claim regardless of current idle; this resets idle to 0
				Messages: []string{msgID},
			}).Err()
			if err != nil && ctx.Err() == nil {
				log.Printf("heartbeat (XCLAIM %s) failed: %v", msgID, err)
			}
		}
	}
}

// reclaimStuck finds messages pending (unacked) longer than minIdle -- the
// signal that the worker holding them has died -- and either reprocesses them or,
// if they have already been delivered too many times (a poison job), dead-letters
// them. We use XPENDING (not XAUTOCLAIM) because it reports each message's
// delivery count, which is what decides retry vs dead-letter.
func reclaimStuck(ctx context.Context, c *client.Client, rdb *redis.Client, consumer string, minIdle time.Duration, maxDeliveries int, heartbeatInterval time.Duration) {
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
			handleMessage(ctx, c, rdb, msg, true, consumer, heartbeatInterval)
		}
	}
}

// deadLetter sets aside a shard job that has been delivered too many times: it
// copies the message to the dead-letter stream, marks the shard failed (which
// may in turn fail its run once all shards are terminal), and acks the original.
func deadLetter(ctx context.Context, c *client.Client, rdb *redis.Client, msgID string, deliveries int64) {
	var shardID int64
	if msgs, err := rdb.XRange(ctx, streamKey, msgID, msgID).Result(); err == nil && len(msgs) == 1 {
		if s, ok := msgs[0].Values["shard_id"].(string); ok {
			shardID, _ = strconv.ParseInt(s, 10, 64)
		}
	}
	log.Printf("dead-lettering message %s (shard %d) after %d deliveries", msgID, shardID, deliveries)

	// Keep a copy in the dead-letter stream for later inspection.
	rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: deadStreamKey,
		Values: map[string]any{
			"shard_id":   strconv.FormatInt(shardID, 10),
			"orig_id":    msgID,
			"deliveries": deliveries,
		},
	})

	// Mark the shard failed so it doesn't linger in a non-terminal state.
	if shardID > 0 {
		if err := c.FailShard(ctx, shardID, "dead-lettered after too many delivery attempts"); err != nil {
			log.Printf("dead-letter: could not fail shard %d: %v", shardID, err)
		}
	}

	// Ack the original so it leaves the group's pending list for good.
	ack(ctx, rdb, msgID)
}

// handleMessage processes one job: start the run, execute it, report the result,
// then acknowledge the message. Acking removes the message from the group's
// pending list so it is not redelivered. On a transient failure we deliberately
// do NOT ack, so the message stays pending and can be retried/reclaimed later.
func handleMessage(ctx context.Context, c *client.Client, rdb *redis.Client, msg redis.XMessage, reclaimed bool, consumer string, heartbeatInterval time.Duration) {
	idStr, _ := msg.Values["shard_id"].(string)
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		// A malformed message will never succeed; ack it so it stops coming back.
		log.Printf("bad shard_id %q in message %s; acking to discard", idStr, msg.ID)
		ack(ctx, rdb, msg.ID)
		return
	}

	// A reclaimed shard may have been left mid-run, so it can take over a running
	// shard; a normal delivery only starts a queued shard (rejecting duplicates).
	var a *client.ShardAssignment
	var ok bool
	if reclaimed {
		a, ok, err = c.TakeOverShard(ctx, id)
	} else {
		a, ok, err = c.StartShard(ctx, id)
	}
	if err != nil {
		// Transient (backend unreachable, etc.): leave unacked so it is retried.
		log.Printf("claim shard %d failed: %v (leaving message pending for retry)", id, err)
		return
	}
	if !ok {
		// Already done (duplicate/finished) or gone: nothing to do. Ack and skip.
		log.Printf("shard %d not claimable (duplicate/finished/missing); acking", id)
		ack(ctx, rdb, msg.ID)
		return
	}

	log.Printf("claimed run %d shard %d: %d VUs for %ds against %s",
		a.RunID, a.ShardIndex, a.VirtualUsers, a.DurationSeconds, a.TargetURL)

	// Keep our claim alive while the job runs (the lease/heartbeat), so a
	// slow-but-alive worker is never mistaken for a dead one and reclaimed. The
	// heartbeat stops the moment the job returns, before we complete/ack.
	hbCtx, stopHeartbeat := context.WithCancel(ctx)
	go heartbeat(hbCtx, rdb, consumer, msg.ID, heartbeatInterval)

	result := executeShard(ctx, a)
	stopHeartbeat()
	if err := c.CompleteShard(ctx, a.ShardID, result); err != nil {
		// Ran the load but couldn't report it: leave pending so completion retries.
		log.Printf("failed to report shard %d completion: %v (leaving pending)", id, err)
		return
	}
	log.Printf("run %d shard %d reported as %s", a.RunID, a.ShardIndex, result.Status)

	ack(ctx, rdb, msg.ID)
}

func ack(ctx context.Context, rdb *redis.Client, msgID string) {
	if err := rdb.XAck(ctx, streamKey, groupName, msgID).Err(); err != nil {
		log.Printf("ack of message %s failed: %v", msgID, err)
	}
}

// executeShard runs one shard's slice of the load and maps the runner Result
// into the shard's partial-result payload. A shard that cannot start (bad config)
// is reported failed; one that executed is reported completed, even if some of
// its individual requests failed. LatencyCount travels along so the backend can
// combine shard averages into a correct weighted run average.
func executeShard(ctx context.Context, a *client.ShardAssignment) client.CompleteShardRequest {
	cfg := runner.Config{
		TargetURL:      a.TargetURL,
		Method:         a.Method,
		VirtualUsers:   a.VirtualUsers,
		Duration:       time.Duration(a.DurationSeconds) * time.Second,
		RequestTimeout: 5 * time.Second,
		Headers:        a.Headers,
		Body:           a.Body,
		ThinkTime:      time.Duration(a.ThinkTimeMs) * time.Millisecond,
	}

	res, err := runner.Run(ctx, cfg)
	if err != nil {
		msg := err.Error()
		return client.CompleteShardRequest{
			Status:       "failed",
			ErrorMessage: &msg,
		}
	}

	return client.CompleteShardRequest{
		Status:             "completed",
		TotalRequests:      &res.TotalRequests,
		SuccessfulRequests: &res.SuccessfulRequests,
		FailedRequests:     &res.FailedRequests,
		LatencyCount:       &res.LatencyCount,
		AvgLatencyMs:       &res.AvgLatencyMs,
		MinLatencyMs:       &res.MinLatencyMs,
		MaxLatencyMs:       &res.MaxLatencyMs,
		LatencyBuckets:     res.LatencyBuckets,
	}
}

// startMetricsServer launches a background HTTP server that serves Prometheus
// metrics at /metrics on addr. Prometheus scrapes each worker replica here. The
// returned server is shut down by the caller on exit.
func startMetricsServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		log.Printf("worker metrics on %s/metrics", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("metrics server error: %v", err)
		}
	}()
	return srv
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
