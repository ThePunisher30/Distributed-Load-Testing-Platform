// Package queue publishes load-test jobs to the Redis stream that workers
// consume. It is the backend's side of the Phase 2 message broker.
package queue

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// StreamKey is the Redis stream jobs are published to and workers read from.
const StreamKey = "testruns"

// Connect opens a Redis client and verifies it is reachable, so the backend
// fails fast at startup if the broker is down (same idea as the DB ping).
func Connect(ctx context.Context, addr string) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{Addr: addr})

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("ping redis at %s: %w", addr, err)
	}
	return rdb, nil
}

// Publisher appends jobs to the stream.
type Publisher struct {
	rdb *redis.Client
}

// NewPublisher builds a Publisher backed by the given Redis client.
func NewPublisher(rdb *redis.Client) *Publisher {
	return &Publisher{rdb: rdb}
}

// PublishJob appends one job carrying the run id to the stream. XADD returns a
// server-assigned message id; workers consume these via a consumer group. Only
// the id travels in the message — the worker fetches the run's config from the
// backend when it starts the run, keeping Postgres the single source of truth.
func (p *Publisher) PublishJob(ctx context.Context, runID int64) error {
	err := p.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamKey,
		Values: map[string]any{"run_id": strconv.FormatInt(runID, 10)},
	}).Err()
	if err != nil {
		return fmt.Errorf("publish job for run %d: %w", runID, err)
	}
	return nil
}
