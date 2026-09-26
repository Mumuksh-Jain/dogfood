package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Config holds SQLite connection options.
type Config struct {
	DSN          string
	MaxOpenConns int
	MaxIdleConns int
}

// Open initializes and verifies a SQLite database connection with required pragmas.
func Open(cfg Config) (*sql.DB, error) {
	if cfg.DSN == "" {
		cfg.DSN = "dogfood.db"
	}

	// If DSN is a file path and not in-memory, ensure parent directory exists.
	if cfg.DSN != ":memory:" && cfg.DSN != "file::memory:?cache=shared" {
		dir := filepath.Dir(cfg.DSN)
		if dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, fmt.Errorf("failed to create database directory %s: %w", dir, err)
			}
		}
	}

	// SQLite connection string with busy timeout and WAL pragmas if file-backed.
	dsn := cfg.DSN
	if cfg.DSN != ":memory:" && cfg.DSN != "file::memory:?cache=shared" {
		separator := "?"
		if contains(dsn, "?") {
			separator = "&"
		}
		dsn = fmt.Sprintf("%s%s_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)", dsn, separator)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// Set connection limits. Default to 1 open connection to prevent SQLite lock contention.
	maxOpen := 1
	if cfg.MaxOpenConns > 0 {
		maxOpen = cfg.MaxOpenConns
	}
	db.SetMaxOpenConns(maxOpen)

	maxIdle := 1
	if cfg.MaxIdleConns > 0 {
		maxIdle = cfg.MaxIdleConns
	}
	db.SetMaxIdleConns(maxIdle)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping sqlite database: %w", err)
	}

	// Explicitly enforce foreign keys pragma.
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to enable foreign_keys pragma: %w", err)
	}

	return db, nil
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && searchString(s, substr))
}

func searchString(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
