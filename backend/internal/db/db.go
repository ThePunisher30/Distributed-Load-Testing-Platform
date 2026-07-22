// Package db owns the backend's connection to PostgreSQL.
//
// We use the standard library's database/sql as the interface and pgx as the
// underlying driver. database/sql gives us a connection pool for free and is
// the idiomatic Go way to talk to SQL databases; pgx is a fast, well-maintained
// Postgres driver.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	// Registers the "pgx" driver with database/sql. The blank import runs the
	// package's init() for its side effect; we never call it directly.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Connect opens a connection pool to Postgres and verifies it is reachable.
//
// database/sql is lazy: sql.Open does NOT actually connect, it just validates
// arguments and prepares the pool. We call PingContext to force a real
// connection now, so the backend fails fast at startup if the database is down
// instead of failing later on the first query.
func Connect(ctx context.Context, dsn string) (*sql.DB, error) {
	pool, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Reasonable pool limits for local development. These cap how many
	// connections we hold open so we don't exhaust Postgres.
	pool.SetMaxOpenConns(10)
	pool.SetMaxIdleConns(5)
	pool.SetConnMaxLifetime(30 * time.Minute)

	// Give the initial ping a bounded window so startup can't hang forever.
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := pool.PingContext(pingCtx); err != nil {
		// Clean up the pool we just opened before returning the error.
		_ = pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}
