package migrations

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestMigration0003_RealSchema(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file::memory:?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatalf("failed to enable foreign keys: %v", err)
	}

	// Run all migrations up through 0003
	if err := Run(ctx, db); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}

	// Verify result_runs and result_entries tables exist
	tables := []string{"result_runs", "result_entries"}
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
		"idx_result_runs_event_status",
		"idx_result_entries_run_rank",
		"idx_result_entries_project",
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

	// Verify Foreign Key Constraints on result_runs and result_entries
	now := time.Now().UTC().Format(time.RFC3339)

	// Inserting a result_run with non-existent event_id must fail under foreign_keys = ON
	_, err = db.ExecContext(ctx, `
		INSERT INTO result_runs (
			id, event_id, algorithm_key, algorithm_version, status, created_at
		) VALUES ('run_invalid', 'evt_nonexistent', 'z_score', 'v1', 'DRAFT', ?);
	`, now)
	if err == nil {
		t.Fatalf("expected foreign key violation when inserting result_run with non-existent event_id")
	}

	// Set up valid T1 entities (creates evt_01, usr_org, trk_01, tm_01, prj_01)
	setupBaseT1Entities(t, db, ctx)

	// Insert valid result_run
	_, err = db.ExecContext(ctx, `
		INSERT INTO result_runs (
			id, event_id, algorithm_key, algorithm_version, config_json, cohort_policy, tie_policy,
			status, input_manifest_json, input_digest, created_by, created_at
		) VALUES (
			'run_01', 'evt_01', 'z_score_standardization', 'v1.0.0', '{}', 'all_completed',
			'deterministic_fallback', 'DRAFT', '{}', 'hash123', 'usr_org', ?
		);
	`, now)
	if err != nil {
		t.Fatalf("failed to insert valid result_run: %v", err)
	}

	// Insert valid result_entry
	_, err = db.ExecContext(ctx, `
		INSERT INTO result_entries (
			result_run_id, project_id, raw_score, normalized_score, final_score,
			rank, tie_group, expected_reviews, completed_reviews, effective_reviews, fallback_count, explanation_json
		) VALUES (
			'run_01', 'prj_01', 4.5, 4.3, 4.3, 1, 1, 3, 3, 3, 0, '{"note":"test"}'
		);
	`)
	if err != nil {
		t.Fatalf("failed to insert valid result_entry: %v", err)
	}

	// Primary Key constraint on result_entries: duplicate (run_01, prj_01) must fail
	_, err = db.ExecContext(ctx, `
		INSERT INTO result_entries (
			result_run_id, project_id, raw_score, normalized_score, final_score,
			rank, tie_group, expected_reviews, completed_reviews, effective_reviews, fallback_count, explanation_json
		) VALUES (
			'run_01', 'prj_01', 4.0, 4.0, 4.0, 2, 2, 3, 3, 3, 0, '{}'
		);
	`)
	if err == nil {
		t.Fatalf("expected primary key violation on duplicate result_entries (run_01, prj_01)")
	}

	// Foreign key cascade: deleting result_run should cascade delete result_entries
	_, err = db.ExecContext(ctx, `DELETE FROM result_runs WHERE id = 'run_01';`)
	if err != nil {
		t.Fatalf("failed to delete result_run: %v", err)
	}

	var entryCount int
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM result_entries WHERE result_run_id = 'run_01';`).Scan(&entryCount)
	if entryCount != 0 {
		t.Fatalf("expected result_entries to be cascade deleted, got count %d", entryCount)
	}
}
