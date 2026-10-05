// Command api is the backend control-plane service for the load testing
// platform. In this step it does two things: connect to PostgreSQL and serve a
// /health endpoint. Endpoints for creating and reading test runs come next.
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

	"distributed-load-testing-platform/backend/internal/db"
	"distributed-load-testing-platform/backend/internal/handlers"
	"distributed-load-testing-platform/backend/internal/queue"
)

func main() {
	// Configuration comes from the environment so the same binary works both on
	// the host (localhost) and inside Docker Compose (service name "postgres"),
	// with sensible local defaults.
	dsn := getenv("DATABASE_URL", "postgres://dltp:dltp@localhost:5432/dltp?sslmode=disable")
	addr := getenv("BACKEND_ADDR", ":8080")
	redisAddr := getenv("REDIS_ADDR", "localhost:6379")
	// Phase 8 safety: runs may only target these hosts (deny by default). The
	// default permits the bundled target service and local addresses; set
	// ALLOWED_TARGET_HOSTS (comma-separated) to opt more in.
	allowedHosts := parseHostSet(getenv("ALLOWED_TARGET_HOSTS", "target,localhost,127.0.0.1"))
	// Phase 8 safety: cap how many runs may be active (queued/running/cancelling)
	// at once, so the platform can't be swamped with unbounded concurrent load.
	maxConcurrent := getint("MAX_CONCURRENT_RUNS", 10)

	// A root context we cancel on shutdown so in-flight work can wind down.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		log.Fatalf("database connection failed: %v", err)
	}
	defer pool.Close()
	log.Println("connected to database")

	rdb, err := queue.Connect(ctx, redisAddr)
	if err != nil {
		log.Fatalf("redis connection failed: %v", err)
	}
	defer rdb.Close()
	log.Println("connected to redis")

	h := handlers.New(pool, queue.NewPublisher(rdb), allowedHosts, maxConcurrent)

	server := &http.Server{
		Addr:         addr,
		Handler:      h.Routes(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	// Run the server in the background so main can wait for a shutdown signal.
	serverErr := make(chan error, 1)
	go func() {
		log.Printf("backend API listening on %s", addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// Block until either the server dies or we get Ctrl-C / SIGTERM.
	select {
	case err := <-serverErr:
		log.Fatalf("server error: %v", err)
	case <-ctx.Done():
		log.Println("shutdown signal received")
	}

	// Give in-flight requests up to 10 seconds to finish before exiting.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
	log.Println("backend stopped")
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

// parseHostSet turns a comma-separated host list into a lookup set (lowercased,
// trimmed, empties dropped).
func parseHostSet(csv string) map[string]bool {
	set := make(map[string]bool)
	for _, h := range strings.Split(csv, ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			set[h] = true
		}
	}
	return set
}

// getenv returns the environment variable named key, or fallback if it is unset
// or empty.
func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
