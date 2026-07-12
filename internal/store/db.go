// Package store provides the database access layer backed by Postgres via pgx/v5.
// It also runs golang-migrate migrations on startup.
package store

import (
	"context"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps a pgx connection pool and exposes sub-stores.
type DB struct {
	pool *pgxpool.Pool
}

// Connect opens a pgx connection pool and pings Postgres.
func Connect(ctx context.Context, dbURL string) (*DB, error) {
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.New: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("db ping: %w", err)
	}
	return &DB{pool: pool}, nil
}

// Migrate runs all pending up-migrations from the given directory path.
// It is idempotent — already-applied migrations are skipped.
func Migrate(dbURL, migrationsPath string) error {
	m, err := migrate.New("file://"+migrationsPath, dbURL)
	if err != nil {
		return fmt.Errorf("migrate.New: %w", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// Close releases all pooled connections.
func (db *DB) Close() {
	db.pool.Close()
}

// Pool returns the underlying pgx pool for packages that need raw access.
func (db *DB) Pool() *pgxpool.Pool {
	return db.pool
}
