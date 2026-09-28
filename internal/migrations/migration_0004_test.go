package migrations

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestMigration0004_RealSchema(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file::memory:?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatalf("failed to enable foreign keys: %v", err)
	}

	// Run all migrations up through 0004
	if err := Run(ctx, db); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}

	// Verify all T3/T4 tables exist
	tables := []string{
		"project_votes",
		"project_comments",
		"vote_audit_events",
		"webhook_subscriptions",
		"event_community_settings",
	}
	for _, table := range tables {
		var name string
		err := db.QueryRowContext(ctx, `
			SELECT name FROM sqlite_master WHERE type='table' AND name=?;
		`, table).Scan(&name)
		if err != nil || name != table {
			t.Fatalf("expected table %s to exist, err: %v", table, err)
		}
	}

	// Verify indices exist
	indices := []string{
		"idx_project_votes_proj",
		"idx_project_votes_user",
		"idx_project_comments_proj",
		"idx_vote_audit_proj",
		"idx_webhook_subs_event",
	}
	for _, idx := range indices {
		var name string
		err := db.QueryRowContext(ctx, `
			SELECT name FROM sqlite_master WHERE type='index' AND name=?;
		`, idx).Scan(&name)
		if err != nil || name != idx {
			t.Fatalf("expected index %s to exist, err: %v", idx, err)
		}
	}

	// Setup valid base T1 entities
	setupBaseT1Entities(t, db, ctx)

	now := time.Now().UTC().Format(time.RFC3339)

	// Test 1: Insert valid vote
	_, err = db.ExecContext(ctx, `
		INSERT INTO project_votes (id, event_id, project_id, user_id, created_at)
		VALUES ('vote_01', 'evt_01', 'prj_01', 'usr_part', ?);
	`, now)
	if err != nil {
		t.Fatalf("failed to insert valid project vote: %v", err)
	}

	// Test 2: Duplicate vote constraint (same event, project, user) must fail
	_, err = db.ExecContext(ctx, `
		INSERT INTO project_votes (id, event_id, project_id, user_id, created_at)
		VALUES ('vote_02', 'evt_01', 'prj_01', 'usr_part', ?);
	`, now)
	if err == nil {
		t.Fatalf("expected unique constraint violation on duplicate vote, got nil")
	}

	// Test 3: Insert valid comment
	_, err = db.ExecContext(ctx, `
		INSERT INTO project_comments (id, event_id, project_id, user_id, author_name, content, created_at)
		VALUES ('cmt_01', 'evt_01', 'prj_01', 'usr_part', 'Participant Bob', 'Great project!', ?);
	`, now)
	if err != nil {
		t.Fatalf("failed to insert valid project comment: %v", err)
	}

	// Test 4: Foreign key violation on non-existent project_id
	_, err = db.ExecContext(ctx, `
		INSERT INTO project_votes (id, event_id, project_id, user_id, created_at)
		VALUES ('vote_03', 'evt_01', 'prj_nonexistent', 'usr_part', ?);
	`, now)
	if err == nil {
		t.Fatalf("expected foreign key violation on invalid project_id, got nil")
	}
}
