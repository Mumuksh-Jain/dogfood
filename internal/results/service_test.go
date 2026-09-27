package results

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"dogfood/internal/migrations"
	_ "modernc.org/sqlite"
)

func setupTestDBWithJudgedProjects(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file::memory:?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatalf("failed to enable foreign keys: %v", err)
	}

	if err := migrations.Run(ctx, db); err != nil {
		t.Fatalf("migrations failed: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	// Create event
	_, err = db.ExecContext(ctx, `
		INSERT INTO events (id, name, submissions_open_at, submissions_close_at, judging_opens_at, judging_closes_at, created_at, updated_at)
		VALUES ('evt_01', 'Dogfood 2026', '2026-01-01T00:00:00Z', '2026-03-01T18:00:00Z', '2026-03-01T18:00:00Z', '2026-03-05T18:00:00Z', ?, ?);
	`, now, now)
	if err != nil {
		t.Fatalf("insert event failed: %v", err)
	}

	// Create users
	_, err = db.ExecContext(ctx, `
		INSERT INTO users (id, email_normalized, display_name, created_at)
		VALUES 
			('usr_org', 'org@example.org', 'Organizer Ada', ?),
			('usr_j1', 'j1@example.org', 'Judge One', ?),
			('usr_j2', 'j2@example.org', 'Judge Two', ?),
			('usr_p1', 'p1@example.org', 'Participant Alice', ?),
			('usr_p2', 'p2@example.org', 'Participant Bob', ?);
	`, now, now, now, now, now)
	if err != nil {
		t.Fatalf("insert users failed: %v", err)
	}

	// Create track
	_, err = db.ExecContext(ctx, `INSERT INTO tracks (id, event_id, name, created_at) VALUES ('trk_01', 'evt_01', 'AI & Infrastructure', ?);`, now)
	if err != nil {
		t.Fatalf("insert track failed: %v", err)
	}

	// Create teams
	_, err = db.ExecContext(ctx, `
		INSERT INTO teams (id, event_id, name, created_by, created_at)
		VALUES 
			('tm_01', 'evt_01', 'Team Alpha', 'usr_p1', ?),
			('tm_02', 'evt_01', 'Team Beta', 'usr_p2', ?);
	`, now, now)
	if err != nil {
		t.Fatalf("insert teams failed: %v", err)
	}

	// Create projects
	_, err = db.ExecContext(ctx, `
		INSERT INTO projects (id, event_id, track_id, team_id, created_by, created_at)
		VALUES 
			('prj_01', 'evt_01', 'trk_01', 'tm_01', 'usr_p1', ?),
			('prj_02', 'evt_01', 'trk_01', 'tm_02', 'usr_p2', ?);
	`, now, now)
	if err != nil {
		t.Fatalf("insert projects failed: %v", err)
	}

	// Create submissions
	_, err = db.ExecContext(ctx, `
		INSERT INTO submissions (id, event_id, project_id, version_no, state, title, summary, created_by, created_at)
		VALUES 
			('sub_01', 'evt_01', 'prj_01', 1, 'SUBMITTED', 'Alpha Vision', 'Computer vision model', 'usr_p1', ?),
			('sub_02', 'evt_01', 'prj_02', 1, 'SUBMITTED', 'Beta Kernel', 'High performance kernel', 'usr_p2', ?);
	`, now, now)
	if err != nil {
		t.Fatalf("insert submissions failed: %v", err)
	}

	// Create rubric
	_, err = db.ExecContext(ctx, `
		INSERT INTO rubric_versions (id, event_id, rubric_id, version_no, state, criteria_json, created_at)
		VALUES ('rub_01', 'evt_01', 'default', 1, 'PUBLISHED', '[]', ?);
	`, now)
	if err != nil {
		t.Fatalf("insert rubric failed: %v", err)
	}

	// Create assignments
	_, err = db.ExecContext(ctx, `
		INSERT INTO assignments (id, event_id, judge_user_id, project_id, rubric_version_id, status, assigned_at)
		VALUES 
			('asg_01', 'evt_01', 'usr_j1', 'prj_01', 'rub_01', 'COMPLETED', ?),
			('asg_02', 'evt_01', 'usr_j1', 'prj_02', 'rub_01', 'COMPLETED', ?),
			('asg_03', 'evt_01', 'usr_j2', 'prj_01', 'rub_01', 'COMPLETED', ?);
	`, now, now, now)
	if err != nil {
		t.Fatalf("insert assignments failed: %v", err)
	}

	// Create submitted ballots
	// j1 scores prj_01 with 5.0 and prj_02 with 3.0 (mean=4.0, stddev=1.414)
	// j2 scores prj_01 with 4.5 (n=1 fallback)
	_, err = db.ExecContext(ctx, `
		INSERT INTO ballot_versions (
			id, assignment_id, judge_user_id, project_id, rubric_version_id,
			version_no, save_kind, scores_json, comment, created_by, created_at
		) VALUES 
			('bv_01', 'asg_01', 'usr_j1', 'prj_01', 'rub_01', 1, 'SUBMISSION', '{"weighted_score":5.0,"total_score":5.0,"criteria":{"c1":5.0}}', 'Great', 'usr_j1', ?),
			('bv_02', 'asg_02', 'usr_j1', 'prj_02', 'rub_01', 1, 'SUBMISSION', '{"weighted_score":3.0,"total_score":3.0,"criteria":{"c1":3.0}}', 'Good', 'usr_j1', ?),
			('bv_03', 'asg_03', 'usr_j2', 'prj_01', 'rub_01', 1, 'SUBMISSION', '{"weighted_score":4.5,"total_score":4.5,"criteria":{"c1":4.5}}', 'Nice', 'usr_j2', ?);
	`, now, now, now)
	if err != nil {
		t.Fatalf("insert ballots failed: %v", err)
	}

	return db, ctx
}

func TestService_ComputePublishAndReplay(t *testing.T) {
	db, ctx := setupTestDBWithJudgedProjects(t)
	defer db.Close()

	svc := NewService(db)

	// 1. Compute initial DRAFT results
	draftRun, err := svc.ComputeResults(ctx, "evt_01", "usr_org")
	if err != nil {
		t.Fatalf("compute results failed: %v", err)
	}
	if draftRun.Status != StatusDraft {
		t.Errorf("expected status DRAFT, got %s", draftRun.Status)
	}
	if draftRun.InputDigest == "" {
		t.Errorf("expected non-empty input digest")
	}

	// Verify entries exist for both projects
	entries, err := svc.ListEntriesForRun(ctx, draftRun.ID)
	if err != nil {
		t.Fatalf("list entries failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// 2. Publish results
	pubRun, err := svc.PublishResults(ctx, draftRun.ID, "usr_org")
	if err != nil {
		t.Fatalf("publish results failed: %v", err)
	}
	if pubRun.Status != StatusPublished {
		t.Errorf("expected status PUBLISHED, got %s", pubRun.Status)
	}
	if pubRun.PublishedAt == nil {
		t.Errorf("expected non-nil published_at")
	}

	// Verify active results can be queried publicly
	activeRun, activeEntries, err := svc.GetActiveResults(ctx, "evt_01")
	if err != nil {
		t.Fatalf("get active results failed: %v", err)
	}
	if activeRun == nil || activeRun.ID != pubRun.ID {
		t.Fatalf("expected active run %s, got %+v", pubRun.ID, activeRun)
	}
	if len(activeEntries) != 2 {
		t.Errorf("expected 2 active entries, got %d", len(activeEntries))
	}

	// 3. Test "Explain This Rank" receipt
	_, p1Entry, p1Exp, err := svc.GetProjectExplanation(ctx, pubRun.ID, "prj_01")
	if err != nil {
		t.Fatalf("get project explanation failed: %v", err)
	}
	if p1Entry.ProjectID != "prj_01" || p1Exp == nil {
		t.Fatalf("invalid project explanation payload")
	}
	if len(p1Exp.BallotAudits) != 2 {
		t.Errorf("prj_01 should have 2 ballot audits, got %d", len(p1Exp.BallotAudits))
	}
	if p1Exp.FormulaBreakdown.ApprovedLanguage != ApprovedFairnessStatement {
		t.Errorf("missing approved fairness statement in explanation breakdown")
	}

	// 4. Test Independent Replay Verification (Happy Path)
	replayReport, err := svc.ReplayRun(ctx, pubRun.ID, "")
	if err != nil {
		t.Fatalf("replay run failed: %v", err)
	}
	if !replayReport.Passed {
		t.Errorf("expected replay to PASS, got FAIL. mismatches: %+v", replayReport.Mismatches)
	}
	if !replayReport.DigestMatches {
		t.Errorf("expected digest match in replay")
	}
	if replayReport.EntriesEvaluated != 2 {
		t.Errorf("expected 2 entries evaluated, got %d", replayReport.EntriesEvaluated)
	}

	// 5. Test Controlled Tampering Failure Detection
	// Mutate a final_score directly in result_entries to simulate tampering
	_, err = db.ExecContext(ctx, `
		UPDATE result_entries SET final_score = 9.99 WHERE result_run_id = ? AND project_id = 'prj_01';
	`, pubRun.ID)
	if err != nil {
		t.Fatalf("tampering update failed: %v", err)
	}

	tamperedReport, err := svc.ReplayRun(ctx, pubRun.ID, "prj_01")
	if err != nil {
		t.Fatalf("replay after tampering failed: %v", err)
	}
	if tamperedReport.Passed {
		t.Errorf("replay MUST fail on tampered data, but reported PASS")
	}
	if len(tamperedReport.Mismatches) == 0 {
		t.Errorf("expected at least 1 mismatch reported for tampered score")
	}
}

func TestService_SupersedingResultsRun(t *testing.T) {
	db, ctx := setupTestDBWithJudgedProjects(t)
	defer db.Close()

	svc := NewService(db)

	// Compute & publish Run 1
	run1, err := svc.ComputeResults(ctx, "evt_01", "usr_org")
	if err != nil {
		t.Fatalf("compute run 1 failed: %v", err)
	}
	_, err = svc.PublishResults(ctx, run1.ID, "usr_org")
	if err != nil {
		t.Fatalf("publish run 1 failed: %v", err)
	}

	// Compute Run 2 (correction or re-run)
	run2, err := svc.ComputeResults(ctx, "evt_01", "usr_org")
	if err != nil {
		t.Fatalf("compute run 2 failed: %v", err)
	}
	if run2.SupersedesResultRunID == nil || *run2.SupersedesResultRunID != run1.ID {
		t.Errorf("run 2 should record supersession of run 1, got %v", run2.SupersedesResultRunID)
	}

	// Publish Run 2: Run 1 should be RETIRED
	_, err = svc.PublishResults(ctx, run2.ID, "usr_org")
	if err != nil {
		t.Fatalf("publish run 2 failed: %v", err)
	}

	r1After, err := svc.GetResultRunByID(ctx, run1.ID)
	if err != nil {
		t.Fatalf("query run 1 failed: %v", err)
	}
	if r1After.Status != StatusRetired {
		t.Errorf("run 1 must be RETIRED after run 2 is published, got %s", r1After.Status)
	}
}
