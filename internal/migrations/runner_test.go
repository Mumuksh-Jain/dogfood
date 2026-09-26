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

func TestMigration0001_RealSchema(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	// Ensure foreign keys pragma is active
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatalf("failed to enable foreign_keys: %v", err)
	}

	// 1. Run migrations using default EmbeddedFS containing 0001_t1_core.sql
	if err := Run(ctx, db); err != nil {
		t.Fatalf("Run failed on fresh empty database: %v", err)
	}

	// 2. Check schema_migrations records 0001 and 0010
	applied, err := GetApplied(ctx, db)
	if err != nil {
		t.Fatalf("GetApplied failed: %v", err)
	}
	if len(applied) != 2 {
		t.Fatalf("expected 2 applied migrations, got %d", len(applied))
	}
	if applied[0].Version != 1 || applied[0].Name != "0001_t1_core" {
		t.Errorf("unexpected migration 1: version=%d name=%s", applied[0].Version, applied[0].Name)
	}
	if applied[1].Version != 10 || applied[1].Name != "0010_t1_prizes" {
		t.Errorf("unexpected migration 10: version=%d name=%s", applied[1].Version, applied[1].Name)
	}

	// 3. Re-run (simulate restart) and verify idempotency
	if err := Run(ctx, db); err != nil {
		t.Fatalf("second Run (restart) failed: %v", err)
	}
	appliedAfterRestart, err := GetApplied(ctx, db)
	if err != nil {
		t.Fatalf("GetApplied after restart failed: %v", err)
	}
	if len(appliedAfterRestart) != 2 {
		t.Fatalf("expected still 2 migrations after restart, got %d", len(appliedAfterRestart))
	}

	// 4. Verify all 12 T1 tables exist (including prizes)
	t1Tables := []string{
		"users",
		"sessions",
		"events",
		"event_roles",
		"tracks",
		"teams",
		"team_memberships",
		"team_invites",
		"projects",
		"submissions",
		"seed_imports",
		"prizes",
	}

	for _, tbl := range t1Tables {
		var count int
		err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?;", tbl).Scan(&count)
		if err != nil {
			t.Fatalf("query for table %s failed: %v", tbl, err)
		}
		if count != 1 {
			t.Errorf("expected T1 table %q to exist, but count was %d", tbl, count)
		}
	}

	// 5. Verify NO T2 tables exist
	t2Tables := []string{
		"judge_profiles",
		"judge_track_eligibility",
		"judge_conflicts",
		"assignment_runs",
		"assignments",
		"rubrics",
		"rubric_versions",
		"ballot_versions",
		"result_runs",
		"result_entries",
	}

	for _, tbl := range t2Tables {
		var count int
		err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?;", tbl).Scan(&count)
		if err != nil {
			t.Fatalf("query for table %s failed: %v", tbl, err)
		}
		if count != 0 {
			t.Errorf("T2 table %q should NOT exist in migration 0001, but found %d", tbl, count)
		}
	}

	// 6. Verify foreign key enforcement is active and working
	// Inserting a session with non-existent user_id should fail with foreign key violation
	_, err = db.ExecContext(ctx, "INSERT INTO sessions (id, user_id, token_hash, created_at, expires_at, last_seen_at) VALUES ('s1', 'nonexistent_u', 'thash', '2026-01-01', '2026-01-02', '2026-01-01');")
	if err == nil {
		t.Errorf("expected foreign key constraint failure on invalid user_id, got nil")
	}
}

