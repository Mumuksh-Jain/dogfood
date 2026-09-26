package migrations

import (
	"context"
	"database/sql"
	"testing"
	"testing/fstest"

	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestRunner_EmptyDB(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if err := RunWithFS(ctx, db, nil); err != nil {
		t.Fatalf("RunWithFS failed with nil fs: %v", err)
	}

	applied, err := GetApplied(ctx, db)
	if err != nil {
		t.Fatalf("GetApplied failed: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("expected 0 applied migrations, got %d", len(applied))
	}
}

func TestRunner_AppliesMigrationsAndIdempotency(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	mockFS := fstest.MapFS{
		"0001_initial.sql": &fstest.MapFile{
			Data: []byte("CREATE TABLE users (id TEXT PRIMARY KEY, name TEXT);"),
		},
		"0002_add_email.sql": &fstest.MapFile{
			Data: []byte("ALTER TABLE users ADD COLUMN email TEXT;"),
		},
	}

	if err := RunWithFS(ctx, db, mockFS); err != nil {
		t.Fatalf("RunWithFS failed: %v", err)
	}

	applied, err := GetApplied(ctx, db)
	if err != nil {
		t.Fatalf("GetApplied failed: %v", err)
	}
	if len(applied) != 2 {
		t.Fatalf("expected 2 applied migrations, got %d", len(applied))
	}
	if applied[0].Version != 1 || applied[1].Version != 2 {
		t.Fatalf("expected versions 1 and 2, got %d and %d", applied[0].Version, applied[1].Version)
	}

	// Re-run should be a no-op
	if err := RunWithFS(ctx, db, mockFS); err != nil {
		t.Fatalf("re-running migrations failed: %v", err)
	}

	applied2, err := GetApplied(ctx, db)
	if err != nil {
		t.Fatalf("GetApplied after second run failed: %v", err)
	}
	if len(applied2) != 2 {
		t.Fatalf("expected still 2 applied migrations, got %d", len(applied2))
	}
}

func TestRunner_ChecksumMismatchFails(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	mockFS := fstest.MapFS{
		"0001_initial.sql": &fstest.MapFile{
			Data: []byte("CREATE TABLE users (id TEXT PRIMARY KEY);"),
		},
	}

	if err := RunWithFS(ctx, db, mockFS); err != nil {
		t.Fatalf("initial RunWithFS failed: %v", err)
	}

	// Mutate the migration content
	mutatedFS := fstest.MapFS{
		"0001_initial.sql": &fstest.MapFile{
			Data: []byte("CREATE TABLE users (id TEXT PRIMARY KEY, mutated INTEGER);"),
		},
	}

	err := RunWithFS(ctx, db, mutatedFS)
	if err == nil {
		t.Fatalf("expected checksum mismatch error, got nil")
	}
}
