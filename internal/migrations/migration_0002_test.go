package migrations

import (
	"context"
	"database/sql"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	_ "modernc.org/sqlite"
)

// Helper to create a seeded base fixture in test DB
func setupBaseT1Entities(t *testing.T, db *sql.DB, ctx context.Context) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)

	// Create event
	_, err := db.ExecContext(ctx, `
		INSERT INTO events (id, name, submissions_open_at, submissions_close_at, judging_opens_at, judging_closes_at, created_at, updated_at)
		VALUES ('evt_01', 'Sample Hack 2026', '2026-01-01T00:00:00Z', '2026-03-01T18:00:00Z', '2026-03-01T18:00:00Z', '2026-03-05T18:00:00Z', ?, ?);
	`, now, now)
	if err != nil {
		t.Fatalf("failed to insert event: %v", err)
	}

	// Create users
	_, err = db.ExecContext(ctx, `
		INSERT INTO users (id, email_normalized, display_name, created_at)
		VALUES 
			('usr_org', 'org@example.org', 'Organizer Ada', ?),
			('usr_jdg1', 'judge1@example.org', 'Judge One', ?),
			('usr_jdg2', 'judge2@example.org', 'Judge Two', ?),
			('usr_part', 'part@example.org', 'Participant Bob', ?);
	`, now, now, now, now)
	if err != nil {
		t.Fatalf("failed to insert users: %v", err)
	}

	// Create event roles
	_, err = db.ExecContext(ctx, `
		INSERT INTO event_roles (event_id, user_id, role, granted_at)
		VALUES
			('evt_01', 'usr_org', 'organizer', ?),
			('evt_01', 'usr_jdg1', 'judge', ?),
			('evt_01', 'usr_jdg2', 'judge', ?),
			('evt_01', 'usr_part', 'participant', ?);
	`, now, now, now, now)
	if err != nil {
		t.Fatalf("failed to insert event_roles: %v", err)
	}

	// Create track
	_, err = db.ExecContext(ctx, `
		INSERT INTO tracks (id, event_id, name, created_at)
		VALUES ('trk_01', 'evt_01', 'Developer Tools', ?);
	`, now)
	if err != nil {
		t.Fatalf("failed to insert track: %v", err)
	}

	// Create team
	_, err = db.ExecContext(ctx, `
		INSERT INTO teams (id, event_id, name, created_by, created_at)
		VALUES ('tm_01', 'evt_01', 'Nightshift', 'usr_part', ?);
	`, now)
	if err != nil {
		t.Fatalf("failed to insert team: %v", err)
	}

	// Create project
	_, err = db.ExecContext(ctx, `
		INSERT INTO projects (id, event_id, team_id, track_id, created_by, created_at)
		VALUES ('prj_01', 'evt_01', 'tm_01', 'trk_01', 'usr_part', ?);
	`, now)
	if err != nil {
		t.Fatalf("failed to insert project: %v", err)
	}
}

// 1. Fresh database: 0001 applies, 0002 applies, all six expected T2 tables exist.
// 9. Migration ordering: 0002 must work on a fresh database starting from 0001.
func TestMigration0002_FreshDatabaseAndOrdering(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatalf("failed to enable foreign_keys: %v", err)
	}

	// Read 0001 and 0002 from EmbeddedFS
	c0001, err := EmbeddedFS.ReadFile("0001_t1_core.sql")
	if err != nil {
		t.Fatalf("read 0001 failed: %v", err)
	}
	c0002, err := EmbeddedFS.ReadFile("0002_t2_judging.sql")
	if err != nil {
		t.Fatalf("read 0002 failed: %v", err)
	}

	// Step 1: Run 0001 alone on empty database
	fs0001Only := fstest.MapFS{
		"0001_t1_core.sql": &fstest.MapFile{Data: c0001},
	}
	if err := RunWithFS(ctx, db, fs0001Only); err != nil {
		t.Fatalf("applying 0001 failed: %v", err)
	}

	// Step 2: Apply 0002 on top of 0001
	fsUpTo0002 := fstest.MapFS{
		"0001_t1_core.sql":    &fstest.MapFile{Data: c0001},
		"0002_t2_judging.sql": &fstest.MapFile{Data: c0002},
	}
	if err := RunWithFS(ctx, db, fsUpTo0002); err != nil {
		t.Fatalf("applying 0002 on top of 0001 failed: %v", err)
	}

	// Verify all six T2 tables exist
	t2Tables := []string{
		"judge_profiles",
		"judge_track_eligibility",
		"assignment_runs",
		"assignments",
		"rubric_versions",
		"ballot_versions",
	}

	for _, tbl := range t2Tables {
		var count int
		err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?;", tbl).Scan(&count)
		if err != nil {
			t.Fatalf("query for table %s failed: %v", tbl, err)
		}
		if count != 1 {
			t.Errorf("expected T2 table %q to exist after 0002 migration, got count %d", tbl, count)
		}
	}
}

// 2. Migration metadata: schema_migrations contains 0001 and 0002, and 0002 checksum is recorded.
func TestMigration0002_MetadataAndChecksum(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatalf("failed to enable foreign_keys: %v", err)
	}

	if err := Run(ctx, db); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	applied, err := GetApplied(ctx, db)
	if err != nil {
		t.Fatalf("GetApplied failed: %v", err)
	}

	var found0001, found0002 bool
	for _, rec := range applied {
		if rec.Version == 1 && rec.Name == "0001_t1_core" {
			found0001 = true
			if len(rec.ChecksumSHA256) != 64 {
				t.Errorf("expected 64-char sha256 for 0001, got %q", rec.ChecksumSHA256)
			}
		}
		if rec.Version == 2 && rec.Name == "0002_t2_judging" {
			found0002 = true
			if len(rec.ChecksumSHA256) != 64 {
				t.Errorf("expected 64-char sha256 for 0002, got %q", rec.ChecksumSHA256)
			}
			if rec.AppliedAtUTC == "" {
				t.Errorf("expected non-empty applied_at_utc for 0002")
			}
		}
	}

	if !found0001 {
		t.Errorf("migration 0001 not found in schema_migrations")
	}
	if !found0002 {
		t.Errorf("migration 0002 not found in schema_migrations")
	}
}

// 3. Restart: reopening/rerunning migration runner does not duplicate or reapply 0002.
func TestMigration0002_RestartIdempotency(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatalf("failed to enable foreign_keys: %v", err)
	}

	if err := Run(ctx, db); err != nil {
		t.Fatalf("first Run failed: %v", err)
	}

	appliedFirst, err := GetApplied(ctx, db)
	if err != nil {
		t.Fatalf("GetApplied first failed: %v", err)
	}

	// Re-run 1 (simulate restart)
	if err := Run(ctx, db); err != nil {
		t.Fatalf("second Run failed: %v", err)
	}

	// Re-run 2 (simulate another restart)
	if err := Run(ctx, db); err != nil {
		t.Fatalf("third Run failed: %v", err)
	}

	appliedAfter, err := GetApplied(ctx, db)
	if err != nil {
		t.Fatalf("GetApplied after restarts failed: %v", err)
	}

	if len(appliedFirst) != len(appliedAfter) {
		t.Fatalf("applied count changed after restarts: before=%d after=%d", len(appliedFirst), len(appliedAfter))
	}

	for i := range appliedFirst {
		if appliedFirst[i] != appliedAfter[i] {
			t.Errorf("migration record [%d] drifted across restarts: %+v != %+v", i, appliedFirst[i], appliedAfter[i])
		}
	}
}

// 4. Checksum: deliberately mutate the recorded checksum; migration runner must fail; restore valid state.
func TestMigration0002_ChecksumMismatchHardFails(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatalf("failed to enable foreign_keys: %v", err)
	}

	if err := Run(ctx, db); err != nil {
		t.Fatalf("initial Run failed: %v", err)
	}

	// Retrieve original checksum for version 2
	var origChecksum string
	err := db.QueryRowContext(ctx, "SELECT checksum_sha256 FROM schema_migrations WHERE version = 2;").Scan(&origChecksum)
	if err != nil {
		t.Fatalf("failed to get checksum for version 2: %v", err)
	}

	// Mutate recorded checksum
	tampered := "0000000000000000000000000000000000000000000000000000000000000000"
	_, err = db.ExecContext(ctx, "UPDATE schema_migrations SET checksum_sha256 = ? WHERE version = 2;", tampered)
	if err != nil {
		t.Fatalf("failed to update checksum: %v", err)
	}

	// Running migration must now fail hard with checksum mismatch
	err = Run(ctx, db)
	if err == nil {
		t.Fatalf("expected Run to fail with checksum mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "checksum mismatch for migration 0002") {
		t.Errorf("unexpected error message: %v", err)
	}

	// Restore original valid checksum
	_, err = db.ExecContext(ctx, "UPDATE schema_migrations SET checksum_sha256 = ? WHERE version = 2;", origChecksum)
	if err != nil {
		t.Fatalf("failed to restore checksum: %v", err)
	}

	// Running migration must now succeed
	if err := Run(ctx, db); err != nil {
		t.Fatalf("Run after restoring valid checksum failed: %v", err)
	}
}

// 5. Foreign keys: verify PRAGMA foreign_keys remains ON, test representative invalid references are rejected.
func TestMigration0002_ForeignKeysEnforced(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatalf("failed to enable foreign_keys: %v", err)
	}

	if err := Run(ctx, db); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Verify PRAGMA foreign_keys remains ON
	var fkStatus int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys;").Scan(&fkStatus); err != nil {
		t.Fatalf("query PRAGMA foreign_keys failed: %v", err)
	}
	if fkStatus != 1 {
		t.Fatalf("expected PRAGMA foreign_keys = 1, got %d", fkStatus)
	}

	setupBaseT1Entities(t, db, ctx)
	now := time.Now().UTC().Format(time.RFC3339)

	// A. judge_profiles: invalid event_id rejected
	_, err := db.ExecContext(ctx, `
		INSERT INTO judge_profiles (event_id, user_id, capacity, active, invited_at)
		VALUES ('nonexistent_evt', 'usr_jdg1', 5, 1, ?);
	`, now)
	if err == nil {
		t.Errorf("judge_profiles: expected foreign key violation on invalid event_id")
	}

	// B. judge_profiles: invalid user_id rejected
	_, err = db.ExecContext(ctx, `
		INSERT INTO judge_profiles (event_id, user_id, capacity, active, invited_at)
		VALUES ('evt_01', 'nonexistent_usr', 5, 1, ?);
	`, now)
	if err == nil {
		t.Errorf("judge_profiles: expected foreign key violation on invalid user_id")
	}

	// C. judge_profiles: negative capacity rejected by CHECK constraint
	_, err = db.ExecContext(ctx, `
		INSERT INTO judge_profiles (event_id, user_id, capacity, active, invited_at)
		VALUES ('evt_01', 'usr_jdg1', -1, 1, ?);
	`, now)
	if err == nil {
		t.Errorf("judge_profiles: expected check constraint violation on negative capacity")
	}

	// D. judge_track_eligibility: invalid track_id rejected
	_, err = db.ExecContext(ctx, `
		INSERT INTO judge_track_eligibility (event_id, judge_user_id, track_id, eligible, source, updated_at)
		VALUES ('evt_01', 'usr_jdg1', 'nonexistent_trk', 1, 'rule', ?);
	`, now)
	if err == nil {
		t.Errorf("judge_track_eligibility: expected foreign key violation on invalid track_id")
	}

	// E. assignment_runs: invalid event_id rejected
	_, err = db.ExecContext(ctx, `
		INSERT INTO assignment_runs (id, event_id, algorithm_key, algorithm_version, status, created_at)
		VALUES ('run_01', 'nonexistent_evt', 'bipartite_max_flow', '1.0', 'COMPLETED', ?);
	`, now)
	if err == nil {
		t.Errorf("assignment_runs: expected foreign key violation on invalid event_id")
	}

	// F. rubric_versions: invalid track_id rejected
	_, err = db.ExecContext(ctx, `
		INSERT INTO rubric_versions (id, event_id, rubric_id, track_id, version_no, state, created_at)
		VALUES ('rub_01', 'evt_01', 'default', 'nonexistent_trk', 1, 'PUBLISHED', ?);
	`, now)
	if err == nil {
		t.Errorf("rubric_versions: expected foreign key violation on invalid track_id")
	}

	// Seed valid rubric_version
	_, err = db.ExecContext(ctx, `
		INSERT INTO rubric_versions (id, event_id, rubric_id, version_no, state, criteria_json, created_at)
		VALUES ('rub_v1', 'evt_01', 'default', 1, 'PUBLISHED', '[]', ?);
	`, now)
	if err != nil {
		t.Fatalf("failed to insert valid rubric_version: %v", err)
	}

	// G. assignments: invalid rubric_version_id rejected
	_, err = db.ExecContext(ctx, `
		INSERT INTO assignments (id, event_id, judge_user_id, project_id, rubric_version_id, assigned_at)
		VALUES ('asg_01', 'evt_01', 'usr_jdg1', 'prj_01', 'nonexistent_rub', ?);
	`, now)
	if err == nil {
		t.Errorf("assignments: expected foreign key violation on invalid rubric_version_id")
	}

	// Seed valid assignment
	_, err = db.ExecContext(ctx, `
		INSERT INTO assignments (id, event_id, judge_user_id, project_id, rubric_version_id, assigned_at)
		VALUES ('asg_01', 'evt_01', 'usr_jdg1', 'prj_01', 'rub_v1', ?);
	`, now)
	if err != nil {
		t.Fatalf("failed to insert valid assignment: %v", err)
	}

	// H. ballot_versions: invalid assignment_id rejected
	_, err = db.ExecContext(ctx, `
		INSERT INTO ballot_versions (id, assignment_id, judge_user_id, project_id, rubric_version_id, version_no, save_kind, created_at)
		VALUES ('bv_01', 'nonexistent_asg', 'usr_jdg1', 'prj_01', 'rub_v1', 1, 'DRAFT_SAVE', ?);
	`, now)
	if err == nil {
		t.Errorf("ballot_versions: expected foreign key violation on invalid assignment_id")
	}
}

// 6. Schema scope: verify the six T2 tables exist, verify no accidental T3/T4 tables were introduced in 0002.
func TestMigration0002_SchemaScope(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatalf("failed to enable foreign_keys: %v", err)
	}

	c0001, err := fs.ReadFile(ActiveFS, "0001_t1_core.sql")
	if err != nil {
		t.Fatalf("failed to read 0001_t1_core.sql: %v", err)
	}
	c0002, err := fs.ReadFile(ActiveFS, "0002_t2_judging.sql")
	if err != nil {
		t.Fatalf("failed to read 0002_t2_judging.sql: %v", err)
	}
	c0010, err := fs.ReadFile(ActiveFS, "0010_t1_prizes.sql")
	if err != nil {
		t.Fatalf("failed to read 0010_t1_prizes.sql: %v", err)
	}
	fsUpTo0002 := fstest.MapFS{
		"0001_t1_core.sql":    &fstest.MapFile{Data: c0001},
		"0002_t2_judging.sql": &fstest.MapFile{Data: c0002},
		"0010_t1_prizes.sql":  &fstest.MapFile{Data: c0010},
	}

	if err := RunWithFS(ctx, db, fsUpTo0002); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// 1. Verify exact six T2 tables exist
	expectedT2Tables := []string{
		"judge_profiles",
		"judge_track_eligibility",
		"assignment_runs",
		"assignments",
		"rubric_versions",
		"ballot_versions",
	}
	for _, tbl := range expectedT2Tables {
		var count int
		err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?;", tbl).Scan(&count)
		if err != nil {
			t.Fatalf("query for table %s failed: %v", tbl, err)
		}
		if count != 1 {
			t.Errorf("expected T2 table %q to exist, got count %d", tbl, count)
		}
	}

	// 2. Verify NO accidental T3/T4 or extra tables exist
	forbiddenTables := []string{
		"result_runs",
		"result_entries",
		"judge_conflicts",
		"ballots",
		"ballot_items",
		"rubrics",
		"rubric_criteria",
		"votes",
		"comments",
		"webhooks",
		"certificates",
		"ranking_snapshots",
		"replay_manifests",
	}
	for _, tbl := range forbiddenTables {
		var count int
		err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?;", tbl).Scan(&count)
		if err != nil {
			t.Fatalf("query for table %s failed: %v", tbl, err)
		}
		if count != 0 {
			t.Errorf("unexpected table %q should NOT exist in T2 schema checkpoint, found %d", tbl, count)
		}
	}

	// 3. Verify core T1 tables still intact
	expectedT1Tables := []string{
		"users", "sessions", "events", "event_roles", "tracks",
		"teams", "team_memberships", "team_invites", "projects", "submissions", "seed_imports", "prizes",
	}
	for _, tbl := range expectedT1Tables {
		var count int
		err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?;", tbl).Scan(&count)
		if err != nil {
			t.Fatalf("query for table %s failed: %v", tbl, err)
		}
		if count != 1 {
			t.Errorf("expected T1 table %q to remain intact, got count %d", tbl, count)
		}
	}
}

// 7. Version semantics: ballot_versions can represent multiple versions for one assignment,
// enforce uniqueness on assignment_id + version_no, no mutable current-version flag,
// later drafts must not replace committed score, corrections append an immutable version.
func TestMigration0002_VersionSemantics_BallotVersions(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatalf("failed to enable foreign_keys: %v", err)
	}

	if err := Run(ctx, db); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Verify absence of mutable current version column
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(ballot_versions);")
	if err != nil {
		t.Fatalf("table_info query failed: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan table_info failed: %v", err)
		}
		if name == "is_current" || name == "current_version_id" || name == "is_latest" {
			t.Fatalf("found forbidden mutable current flag column %q in ballot_versions", name)
		}
	}

	setupBaseT1Entities(t, db, ctx)
	now := time.Now().UTC().Format(time.RFC3339)

	// Setup rubric version and assignment
	_, err = db.ExecContext(ctx, `
		INSERT INTO rubric_versions (id, event_id, rubric_id, version_no, state, criteria_json, created_at)
		VALUES ('rub_v1', 'evt_01', 'default', 1, 'PUBLISHED', '[]', ?);
	`, now)
	if err != nil {
		t.Fatalf("insert rubric_versions failed: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO assignments (id, event_id, judge_user_id, project_id, rubric_version_id, assigned_at)
		VALUES ('asg_01', 'evt_01', 'usr_jdg1', 'prj_01', 'rub_v1', ?);
	`, now)
	if err != nil {
		t.Fatalf("insert assignments failed: %v", err)
	}

	// 1. Insert version 1: DRAFT_SAVE
	_, err = db.ExecContext(ctx, `
		INSERT INTO ballot_versions (id, assignment_id, judge_user_id, project_id, rubric_version_id, version_no, save_kind, scores_json, comment, created_at)
		VALUES ('bv_01', 'asg_01', 'usr_jdg1', 'prj_01', 'rub_v1', 1, 'DRAFT_SAVE', '{"functionality": 3}', 'Work in progress', ?);
	`, now)
	if err != nil {
		t.Fatalf("insert bv_01 draft failed: %v", err)
	}

	// Latest working version = 1
	var workingVer int
	err = db.QueryRowContext(ctx, "SELECT version_no FROM ballot_versions WHERE assignment_id = 'asg_01' ORDER BY version_no DESC LIMIT 1;").Scan(&workingVer)
	if err != nil || workingVer != 1 {
		t.Fatalf("expected latest working version 1, got %d (err: %v)", workingVer, err)
	}

	// Committed ballot query = 0 rows (no SUBMISSION or CORRECTION yet)
	var committedVer int
	err = db.QueryRowContext(ctx, "SELECT version_no FROM ballot_versions WHERE assignment_id = 'asg_01' AND save_kind IN ('SUBMISSION', 'CORRECTION') ORDER BY version_no DESC LIMIT 1;").Scan(&committedVer)
	if err != sql.ErrNoRows {
		t.Fatalf("expected no committed ballot yet, got ver %d (err: %v)", committedVer, err)
	}

	// 2. Insert version 2: SUBMISSION (official committed ballot)
	_, err = db.ExecContext(ctx, `
		INSERT INTO ballot_versions (id, assignment_id, judge_user_id, project_id, rubric_version_id, version_no, save_kind, scores_json, comment, created_at)
		VALUES ('bv_02', 'asg_01', 'usr_jdg1', 'prj_01', 'rub_v1', 2, 'SUBMISSION', '{"functionality": 4}', 'Solid submission', ?);
	`, now)
	if err != nil {
		t.Fatalf("insert bv_02 submission failed: %v", err)
	}

	// Committed ballot = 2
	err = db.QueryRowContext(ctx, "SELECT version_no FROM ballot_versions WHERE assignment_id = 'asg_01' AND save_kind IN ('SUBMISSION', 'CORRECTION') ORDER BY version_no DESC LIMIT 1;").Scan(&committedVer)
	if err != nil || committedVer != 2 {
		t.Fatalf("expected committed ballot version 2, got %d (err: %v)", committedVer, err)
	}

	// 3. Insert version 3: DRAFT_SAVE (judge starts draft post-submission)
	_, err = db.ExecContext(ctx, `
		INSERT INTO ballot_versions (id, assignment_id, judge_user_id, project_id, rubric_version_id, version_no, save_kind, scores_json, comment, created_at)
		VALUES ('bv_03', 'asg_01', 'usr_jdg1', 'prj_01', 'rub_v1', 3, 'DRAFT_SAVE', '{"functionality": 5}', 'Thinking about higher score', ?);
	`, now)
	if err != nil {
		t.Fatalf("insert bv_03 draft failed: %v", err)
	}

	// Latest working version is 3
	err = db.QueryRowContext(ctx, "SELECT version_no FROM ballot_versions WHERE assignment_id = 'asg_01' ORDER BY version_no DESC LIMIT 1;").Scan(&workingVer)
	if err != nil || workingVer != 3 {
		t.Fatalf("expected working version 3, got %d (err: %v)", workingVer, err)
	}

	// CRITICAL SEMANTIC: Later draft must NOT replace committed score!
	var committedScores string
	err = db.QueryRowContext(ctx, "SELECT version_no, scores_json FROM ballot_versions WHERE assignment_id = 'asg_01' AND save_kind IN ('SUBMISSION', 'CORRECTION') ORDER BY version_no DESC LIMIT 1;").Scan(&committedVer, &committedScores)
	if err != nil || committedVer != 2 || !strings.Contains(committedScores, `"functionality": 4`) {
		t.Fatalf("committed score was overwritten by later draft! committedVer=%d scores=%s", committedVer, committedScores)
	}

	// 4. Insert version 4: CORRECTION (appends immutable corrected version)
	_, err = db.ExecContext(ctx, `
		INSERT INTO ballot_versions (id, assignment_id, judge_user_id, project_id, rubric_version_id, version_no, save_kind, scores_json, comment, created_at, supersedes_version_id)
		VALUES ('bv_04', 'asg_01', 'usr_jdg1', 'prj_01', 'rub_v1', 4, 'CORRECTION', '{"functionality": 5}', 'Official correction approved', ?, 'bv_02');
	`, now)
	if err != nil {
		t.Fatalf("insert bv_04 correction failed: %v", err)
	}

	// Now committed ballot is version 4 with score 5
	err = db.QueryRowContext(ctx, "SELECT version_no, scores_json FROM ballot_versions WHERE assignment_id = 'asg_01' AND save_kind IN ('SUBMISSION', 'CORRECTION') ORDER BY version_no DESC LIMIT 1;").Scan(&committedVer, &committedScores)
	if err != nil || committedVer != 4 || !strings.Contains(committedScores, `"functionality": 5`) {
		t.Fatalf("expected committed correction version 4, got %d scores=%s (err: %v)", committedVer, committedScores, err)
	}

	// 5. Enforce uniqueness: duplicate (assignment_id, version_no) must be rejected
	_, err = db.ExecContext(ctx, `
		INSERT INTO ballot_versions (id, assignment_id, judge_user_id, project_id, rubric_version_id, version_no, save_kind, scores_json, created_at)
		VALUES ('bv_dup', 'asg_01', 'usr_jdg1', 'prj_01', 'rub_v1', 2, 'DRAFT_SAVE', '{}', ?);
	`, now)
	if err == nil {
		t.Errorf("expected duplicate (assignment_id, version_no) to be rejected by unique constraint")
	}
}

// 8. Assignment integrity: prevent duplicate judge/project assignment identities where required by frozen model,
// preserve event/project/judge relationships through foreign keys, support infeasible run with zero assignments.
func TestMigration0002_AssignmentIntegrity(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatalf("failed to enable foreign_keys: %v", err)
	}

	if err := Run(ctx, db); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	setupBaseT1Entities(t, db, ctx)
	now := time.Now().UTC().Format(time.RFC3339)

	// Setup rubric version
	_, err := db.ExecContext(ctx, `
		INSERT INTO rubric_versions (id, event_id, rubric_id, version_no, state, criteria_json, created_at)
		VALUES ('rub_v1', 'evt_01', 'default', 1, 'PUBLISHED', '[]', ?);
	`, now)
	if err != nil {
		t.Fatalf("insert rubric_versions failed: %v", err)
	}

	// 1. Infeasible run with zero assignments is representable
	_, err = db.ExecContext(ctx, `
		INSERT INTO assignment_runs (id, event_id, algorithm_key, algorithm_version, status, requested_reviews_total, infeasibility_code, infeasibility_details_json, created_at)
		VALUES ('run_inf', 'evt_01', 'bipartite_max_flow', '1.0', 'INFEASIBLE', 10, 'CAPACITY_INSUFFICIENT', '{"reason": "track capacity exceeded"}', ?);
	`, now)
	if err != nil {
		t.Fatalf("insert infeasible assignment run failed: %v", err)
	}

	var runCount int
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM assignment_runs WHERE status = 'INFEASIBLE';").Scan(&runCount)
	if runCount != 1 {
		t.Fatalf("expected 1 infeasible assignment run, got %d", runCount)
	}

	var asgCount int
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM assignments WHERE assignment_run_id = 'run_inf';").Scan(&asgCount)
	if asgCount != 0 {
		t.Fatalf("expected 0 assignments for infeasible run, got %d", asgCount)
	}

	// 2. Active assignment creation
	_, err = db.ExecContext(ctx, `
		INSERT INTO assignments (id, event_id, judge_user_id, project_id, rubric_version_id, status, assigned_at)
		VALUES ('asg_active1', 'evt_01', 'usr_jdg1', 'prj_01', 'rub_v1', 'ASSIGNED', ?);
	`, now)
	if err != nil {
		t.Fatalf("insert active assignment failed: %v", err)
	}

	// 3. Duplicate active assignment for same judge and project MUST fail
	_, err = db.ExecContext(ctx, `
		INSERT INTO assignments (id, event_id, judge_user_id, project_id, rubric_version_id, status, assigned_at)
		VALUES ('asg_active_dup', 'evt_01', 'usr_jdg1', 'prj_01', 'rub_v1', 'ASSIGNED', ?);
	`, now)
	if err == nil {
		t.Errorf("expected duplicate active assignment to be rejected by idx_assignments_active_judge_project")
	}

	// Attempt with status = STARTED must also fail
	_, err = db.ExecContext(ctx, `
		INSERT INTO assignments (id, event_id, judge_user_id, project_id, rubric_version_id, status, assigned_at)
		VALUES ('asg_started_dup', 'evt_01', 'usr_jdg1', 'prj_01', 'rub_v1', 'STARTED', ?);
	`, now)
	if err == nil {
		t.Errorf("expected duplicate STARTED assignment to be rejected")
	}

	// Attempt with status = COMPLETED must also fail
	_, err = db.ExecContext(ctx, `
		INSERT INTO assignments (id, event_id, judge_user_id, project_id, rubric_version_id, status, assigned_at)
		VALUES ('asg_completed_dup', 'evt_01', 'usr_jdg1', 'prj_01', 'rub_v1', 'COMPLETED', ?);
	`, now)
	if err == nil {
		t.Errorf("expected duplicate COMPLETED assignment to be rejected")
	}

	// 4. Reassignment lifecycle: when previous assignment is CANCELLED, a new assignment is permitted
	_, err = db.ExecContext(ctx, "UPDATE assignments SET status = 'CANCELLED' WHERE id = 'asg_active1';")
	if err != nil {
		t.Fatalf("failed to cancel assignment: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO assignments (id, event_id, judge_user_id, project_id, rubric_version_id, status, assigned_at, supersedes_assignment_id, reason)
		VALUES ('asg_reassigned', 'evt_01', 'usr_jdg1', 'prj_01', 'rub_v1', 'ASSIGNED', ?, 'asg_active1', 'Reassigned after judge availability update');
	`, now)
	if err != nil {
		t.Fatalf("reassignment after cancellation failed: %v", err)
	}
}

// Logical rubric scope uniqueness
func TestMigration0002_RubricVersions_Scope(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatalf("failed to enable foreign_keys: %v", err)
	}

	if err := Run(ctx, db); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	setupBaseT1Entities(t, db, ctx)
	now := time.Now().UTC().Format(time.RFC3339)

	// Insert rubric version 1
	_, err := db.ExecContext(ctx, `
		INSERT INTO rubric_versions (id, event_id, rubric_id, version_no, state, criteria_json, created_at)
		VALUES ('rub_1', 'evt_01', 'default', 1, 'PUBLISHED', '[]', ?);
	`, now)
	if err != nil {
		t.Fatalf("failed to insert rubric v1: %v", err)
	}

	// Duplicate scope (event_id, rubric_id, version_no) must fail
	_, err = db.ExecContext(ctx, `
		INSERT INTO rubric_versions (id, event_id, rubric_id, version_no, state, criteria_json, created_at)
		VALUES ('rub_1_dup', 'evt_01', 'default', 1, 'DRAFT', '[]', ?);
	`, now)
	if err == nil {
		t.Errorf("expected duplicate rubric scope (event_id, rubric_id, version_no) to be rejected")
	}

	// New version_no succeeds
	_, err = db.ExecContext(ctx, `
		INSERT INTO rubric_versions (id, event_id, rubric_id, version_no, state, criteria_json, created_at)
		VALUES ('rub_2', 'evt_01', 'default', 2, 'DRAFT', '[]', ?);
	`, now)
	if err != nil {
		t.Fatalf("failed to insert rubric v2: %v", err)
	}
}
