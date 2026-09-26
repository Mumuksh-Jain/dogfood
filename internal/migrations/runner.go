package migrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed *.sql
var EmbeddedFS embed.FS

// Record represents a row in schema_migrations table.
type Record struct {
	Version        int
	Name           string
	ChecksumSHA256 string
	AppliedAtUTC   string
}

// ActiveFS holds the migration SQL files. Can be overridden or embedded.
var ActiveFS fs.FS = EmbeddedFS

// Run creates the schema_migrations table and executes any pending migrations from ActiveFS.
func Run(ctx context.Context, db *sql.DB) error {
	return RunWithFS(ctx, db, ActiveFS)
}

// RunWithFS executes migrations found in the provided filesystem.
func RunWithFS(ctx context.Context, db *sql.DB, fsys fs.FS) error {
	// Bootstrap schema_migrations table if not exists.
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		checksum_sha256 TEXT NOT NULL,
		applied_at_utc TEXT NOT NULL
	);`

	if _, err := db.ExecContext(ctx, createTableSQL); err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}

	if fsys == nil {
		return nil
	}

	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %w", err)
	}

	type migrationFile struct {
		version  int
		name     string
		filename string
	}

	var files []migrationFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		name := entry.Name()
		parts := strings.SplitN(name, "_", 2)
		if len(parts) < 2 {
			continue
		}
		ver, err := strconv.Atoi(parts[0])
		if err != nil {
			return fmt.Errorf("invalid migration version prefix in file %s: %w", name, err)
		}
		files = append(files, migrationFile{
			version:  ver,
			name:     strings.TrimSuffix(name, filepath.Ext(name)),
			filename: name,
		})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].version < files[j].version
	})

	applied, err := GetApplied(ctx, db)
	if err != nil {
		return fmt.Errorf("failed to retrieve applied migrations: %w", err)
	}

	appliedMap := make(map[int]Record, len(applied))
	for _, r := range applied {
		appliedMap[r.Version] = r
	}

	for _, file := range files {
		f, err := fsys.Open(file.filename)
		if err != nil {
			return fmt.Errorf("failed to open migration file %s: %w", file.filename, err)
		}
		content, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", file.filename, err)
		}

		hash := sha256.Sum256(content)
		checksum := hex.EncodeToString(hash[:])

		if existing, ok := appliedMap[file.version]; ok {
			if existing.ChecksumSHA256 != checksum {
				return fmt.Errorf("checksum mismatch for migration %04d (%s): recorded=%s current=%s",
					file.version, file.name, existing.ChecksumSHA256, checksum)
			}
			continue
		}

		// Execute migration in a single transaction.
		if err := applyMigration(ctx, db, file.version, file.name, string(content), checksum); err != nil {
			return fmt.Errorf("migration %04d (%s) failed: %w", file.version, file.name, err)
		}
	}

	return nil
}

func applyMigration(ctx context.Context, db *sql.DB, version int, name, query, checksum string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("exec error: %w", err)
	}

	insertSQL := `INSERT INTO schema_migrations (version, name, checksum_sha256, applied_at_utc) VALUES (?, ?, ?, ?);`
	nowUTC := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, insertSQL, version, name, checksum, nowUTC); err != nil {
		return fmt.Errorf("failed to record migration: %w", err)
	}

	return tx.Commit()
}

// GetApplied returns all applied migration records ordered by version.
func GetApplied(ctx context.Context, db *sql.DB) ([]Record, error) {
	rows, err := db.QueryContext(ctx, "SELECT version, name, checksum_sha256, applied_at_utc FROM schema_migrations ORDER BY version ASC;")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []Record
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.Version, &r.Name, &r.ChecksumSHA256, &r.AppliedAtUTC); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}
