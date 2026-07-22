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
	"syscall"
	"time"

	"distributed-load-testing-platform/backend/internal/db"
	"distributed-load-testing-platform/backend/internal/handlers"
)

func main() {
	// Configuration comes from the environment so the same binary works both on
	// the host (localhost) and inside Docker Compose (service name "postgres"),
	// with sensible local defaults.
	dsn := getenv("DATABASE_URL", "postgres://dltp:dltp@localhost:5432/dltp?sslmode=disable")
	addr := getenv("BACKEND_ADDR", ":8080")

	// A root context we cancel on shutdown so in-flight work can wind down.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		log.Fatalf("database connection failed: %v", err)
	}
	defer pool.Close()
	log.Println("connected to database")

	h := handlers.New(pool)

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

// getenv returns the environment variable named key, or fallback if it is unset
// or empty.
func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
