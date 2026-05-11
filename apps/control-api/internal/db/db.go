package db

import (
	"context"
	"embed"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Connect establishes a connection pool to the database and registers
// the pgvector type codec on every connection so that []float32 / pgvector.Vector
// values can be encoded into the vector(N) PostgreSQL type.
func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}

	config.MaxConns = 10
	config.MinConns = 2
	config.HealthCheckPeriod = 30 * time.Second

	// Harden connection pool with PostgreSQL runtime parameters.
	// These prevent long-running queries (especially pgvector similarity scans)
	// from starving the pool.
	config.ConnConfig.RuntimeParams["statement_timeout"] = "30000"          // 30s max query time
	config.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "10000" // 10s idle txn
	config.ConnConfig.RuntimeParams["application_name"] = "openserve-control-api"

	// Register the pgvector codec on every new connection. If the vector
	// extension isn't installed yet (e.g. before migrations run), skip
	// gracefully so the pool can still come up.
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if err := pgxvec.RegisterTypes(ctx, conn); err != nil {
			// Don't fail the pool if vector extension isn't installed yet
			// — migrations create it. We'll re-register on the next conn.
			return nil
		}
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return pool, nil
}

// Migrate runs all pending migrations in filename order.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	// Create the schema_migrations table if it doesn't exist
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS schema_migrations (
		filename TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	);
	`
	if _, err := pool.Exec(ctx, createTableSQL); err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}

	// Read all migration files
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %w", err)
	}

	// Sort by filename
	var filenames []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		filenames = append(filenames, entry.Name())
	}
	sort.Strings(filenames)

	// Apply each migration
	for _, filename := range filenames {
		// Check if already applied
		var exists bool
		err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE filename = $1)", filename).Scan(&exists)
		if err != nil {
			return fmt.Errorf("failed to check migration status for %s: %w", filename, err)
		}

		if exists {
			continue
		}

		// Read migration file
		migrationSQL, err := migrations.ReadFile(filepath.Join("migrations", filename))
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", filename, err)
		}

		// Execute migration
		if _, err := pool.Exec(ctx, string(migrationSQL)); err != nil {
			return fmt.Errorf("failed to apply migration %s: %w", filename, err)
		}

		// Record migration
		if _, err := pool.Exec(ctx, "INSERT INTO schema_migrations (filename) VALUES ($1)", filename); err != nil {
			return fmt.Errorf("failed to record migration %s: %w", filename, err)
		}
	}

	return nil
}
