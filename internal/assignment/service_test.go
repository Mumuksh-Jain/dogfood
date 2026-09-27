package assignment

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"dogfood/internal/migrations"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) (*sql.DB, *Service) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatalf("failed to enable foreign_keys: %v", err)
	}

	if err := migrations.Run(ctx, db); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	return db, NewService(db)
}

func createTestEvent(t *testing.T, db *sql.DB, ctx context.Context, eventID string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.ExecContext(ctx, `
		INSERT INTO events (id, name, submissions_open_at, submissions_close_at, judging_opens_at, judging_closes_at, created_at, updated_at)
		VALUES (?, 'Test Event', '2026-01-01T00:00:00Z', '2026-03-01T18:00:00Z', '2026-03-01T18:00:00Z', '2026-03-05T18:00:00Z', ?, ?);
	`, eventID, now, now)
	if err != nil {
		t.Fatalf("createTestEvent failed: %v", err)
	}
}

func createTestTrack(t *testing.T, db *sql.DB, ctx context.Context, trackID, eventID, name string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := db.ExecContext(ctx, `
		INSERT INTO tracks (id, event_id, name, created_at)
		VALUES (?, ?, ?, ?);
	`, trackID, eventID, name, now)
	if err != nil {
		t.Fatalf("createTestTrack failed: %v", err)
	}
}

func createTestJudge(t *testing.T, db *sql.DB, ctx context.Context, judgeID, eventID string, capacity int, trackIDs []string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	// Create user
	_, err := db.ExecContext(ctx, `
		INSERT INTO users (id, email_normalized, display_name, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING;
	`, judgeID, judgeID+"@example.org", "Judge "+judgeID, now)
	if err != nil {
		t.Fatalf("create user for judge failed: %v", err)
	}

	// Create event_roles
	_, err = db.ExecContext(ctx, `
		INSERT INTO event_roles (event_id, user_id, role, granted_at)
		VALUES (?, ?, 'judge', ?)
		ON CONFLICT(event_id, user_id, role) DO NOTHING;
	`, eventID, judgeID, now)
	if err != nil {
		t.Fatalf("create event_role failed: %v", err)
	}

	// Create judge_profiles
	_, err = db.ExecContext(ctx, `
		INSERT INTO judge_profiles (event_id, user_id, capacity, active, invited_at)
		VALUES (?, ?, ?, 1, ?)
		ON CONFLICT(event_id, user_id) DO UPDATE SET capacity = excluded.capacity, active = 1;
	`, eventID, judgeID, capacity, now)
	if err != nil {
		t.Fatalf("create judge_profiles failed: %v", err)
	}

	// Create track eligibility
	for _, trk := range trackIDs {
		_, err = db.ExecContext(ctx, `
			INSERT INTO judge_track_eligibility (event_id, judge_user_id, track_id, eligible, source, updated_at)
			VALUES (?, ?, ?, 1, 'rule', ?)
			ON CONFLICT(event_id, judge_user_id, track_id) DO UPDATE SET eligible = 1;
		`, eventID, judgeID, trk, now)
		if err != nil {
			t.Fatalf("create judge_track_eligibility failed: %v", err)
		}
	}
}

func createTestProject(t *testing.T, db *sql.DB, ctx context.Context, projectID, eventID, trackID string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	teamID := "tm_" + projectID
	userID := "usr_" + projectID

	// Create user
	_, _ = db.ExecContext(ctx, `
		INSERT INTO users (id, email_normalized, display_name, created_at)
		VALUES (?, ?, 'Participant', ?)
		ON CONFLICT(id) DO NOTHING;
	`, userID, userID+"@example.org", now)

	// Create team
	_, err := db.ExecContext(ctx, `
		INSERT INTO teams (id, event_id, name, created_at)
		VALUES (?, ?, 'Team', ?)
		ON CONFLICT(id) DO NOTHING;
	`, teamID, eventID, now)
	if err != nil {
		t.Fatalf("create team failed: %v", err)
	}

	// Create project
	_, err = db.ExecContext(ctx, `
		INSERT INTO projects (id, event_id, team_id, track_id, eligibility_status, created_at)
		VALUES (?, ?, ?, ?, 'ELIGIBLE', ?)
		ON CONFLICT(id) DO NOTHING;
	`, projectID, eventID, teamID, trackID, now)
	if err != nil {
		t.Fatalf("create project failed: %v", err)
	}

	// Create submitted submission
	_, err = db.ExecContext(ctx, `
		INSERT INTO submissions (id, event_id, project_id, version_no, state, title, created_at, submitted_at)
		VALUES (?, ?, ?, 1, 'SUBMITTED', 'Project Title', ?, ?);
	`, "sub_"+projectID, eventID, projectID, now, now)
	if err != nil {
		t.Fatalf("create submission failed: %v", err)
	}
}

// Test 1 — Basic feasible assignment
func TestService_BasicFeasible(t *testing.T) {
	db, svc := setupTestDB(t)
	ctx := context.Background()

	createTestEvent(t, db, ctx, "evt_01")
	createTestTrack(t, db, ctx, "trk_01", "evt_01", "Track 1")

	// 4 projects
	for i := 1; i <= 4; i++ {
		createTestProject(t, db, ctx, fmt.Sprintf("prj_%02d", i), "evt_01", "trk_01")
	}

	// 4 judges, capacity 3 each
	for i := 1; i <= 4; i++ {
		createTestJudge(t, db, ctx, fmt.Sprintf("jdg_%02d", i), "evt_01", 3, []string{"trk_01"})
	}

	res, err := svc.GenerateAssignments(ctx, Config{
		EventID:       "evt_01",
		TargetReviews: 2,
	})
	if err != nil {
		t.Fatalf("GenerateAssignments failed: %v", err)
	}

	if !res.Feasible || res.Status != "COMPLETED" {
		t.Fatalf("expected feasible COMPLETED assignment, got status=%s code=%s", res.Status, res.InfeasibilityCode)
	}

	// 4 projects * 2 reviews = 8 assignments
	if res.AssignmentsCount != 8 {
		t.Fatalf("expected 8 assignments, got %d", res.AssignmentsCount)
	}

	// Verify database persistence
	var count int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM assignments WHERE event_id = 'evt_01' AND status = 'ASSIGNED';").Scan(&count)
	if err != nil || count != 8 {
		t.Fatalf("expected 8 persisted assignments in db, got %d (err: %v)", count, err)
	}

	// Verify all projects receive 2 distinct judges & no duplicates
	projectJudges := make(map[string]map[string]bool)
	judgeLoads := make(map[string]int)
	for _, asg := range res.Assignments {
		if projectJudges[asg.ProjectID] == nil {
			projectJudges[asg.ProjectID] = make(map[string]bool)
		}
		if projectJudges[asg.ProjectID][asg.JudgeUserID] {
			t.Errorf("duplicate judge %s on project %s", asg.JudgeUserID, asg.ProjectID)
		}
		projectJudges[asg.ProjectID][asg.JudgeUserID] = true
		judgeLoads[asg.JudgeUserID]++
	}

	for i := 1; i <= 4; i++ {
		pid := fmt.Sprintf("prj_%02d", i)
		if len(projectJudges[pid]) != 2 {
			t.Errorf("project %s expected 2 distinct judges, got %d", pid, len(projectJudges[pid]))
		}
	}

	for i := 1; i <= 4; i++ {
		jid := fmt.Sprintf("jdg_%02d", i)
		if judgeLoads[jid] > 3 {
			t.Errorf("judge %s exceeded capacity (%d > 3)", jid, judgeLoads[jid])
		}
	}
}

// Test 2 — Determinism: Run exact same input twice, verify identical assignment pairs & ordering
func TestService_Determinism(t *testing.T) {
	ctx := context.Background()

	setupRun := func() []Assignment {
		db, svc := setupTestDB(t)
		createTestEvent(t, db, ctx, "evt_det")
		createTestTrack(t, db, ctx, "trk_01", "evt_det", "Track 1")
		createTestTrack(t, db, ctx, "trk_02", "evt_det", "Track 2")

		for i := 1; i <= 6; i++ {
			trk := "trk_01"
			if i > 3 {
				trk = "trk_02"
			}
			createTestProject(t, db, ctx, fmt.Sprintf("prj_%02d", i), "evt_det", trk)
		}

		for i := 1; i <= 5; i++ {
			createTestJudge(t, db, ctx, fmt.Sprintf("jdg_%02d", i), "evt_det", 4, []string{"trk_01", "trk_02"})
		}

		res, err := svc.GenerateAssignments(ctx, Config{
			EventID:       "evt_det",
			TargetReviews: 3,
			RunID:         "run_fixed",
		})
		if err != nil {
			t.Fatalf("run failed: %v", err)
		}
		if !res.Feasible {
			t.Fatalf("expected feasible run")
		}
		return res.Assignments
	}

	run1 := setupRun()
	run2 := setupRun()

	if len(run1) != len(run2) {
		t.Fatalf("assignments count mismatch: %d != %d", len(run1), len(run2))
	}

	for i := range run1 {
		if run1[i].JudgeUserID != run2[i].JudgeUserID || run1[i].ProjectID != run2[i].ProjectID {
			t.Fatalf("mismatch at [%d]:\nRun1: %s -> %s\nRun2: %s -> %s",
				i, run1[i].JudgeUserID, run1[i].ProjectID, run2[i].JudgeUserID, run2[i].ProjectID)
		}
	}
}

// Test 3 — Track eligibility: Judges only receive projects from eligible tracks
func TestService_TrackEligibility(t *testing.T) {
	db, svc := setupTestDB(t)
	ctx := context.Background()

	createTestEvent(t, db, ctx, "evt_trk")
	createTestTrack(t, db, ctx, "trk_a", "evt_trk", "Track A")
	createTestTrack(t, db, ctx, "trk_b", "evt_trk", "Track B")

	// 2 projects on Track A, 2 projects on Track B
	createTestProject(t, db, ctx, "prj_a1", "evt_trk", "trk_a")
	createTestProject(t, db, ctx, "prj_a2", "evt_trk", "trk_a")
	createTestProject(t, db, ctx, "prj_b1", "evt_trk", "trk_b")
	createTestProject(t, db, ctx, "prj_b2", "evt_trk", "trk_b")

	// Judges 1 & 2 only for Track A
	createTestJudge(t, db, ctx, "jdg_a1", "evt_trk", 4, []string{"trk_a"})
	createTestJudge(t, db, ctx, "jdg_a2", "evt_trk", 4, []string{"trk_a"})

	// Judges 3 & 4 only for Track B
	createTestJudge(t, db, ctx, "jdg_b1", "evt_trk", 4, []string{"trk_b"})
	createTestJudge(t, db, ctx, "jdg_b2", "evt_trk", 4, []string{"trk_b"})

	res, err := svc.GenerateAssignments(ctx, Config{
		EventID:       "evt_trk",
		TargetReviews: 2,
	})
	if err != nil {
		t.Fatalf("GenerateAssignments failed: %v", err)
	}
	if !res.Feasible {
		t.Fatalf("expected feasible run, got %s", res.InfeasibilityCode)
	}

	for _, asg := range res.Assignments {
		if asg.JudgeUserID == "jdg_a1" || asg.JudgeUserID == "jdg_a2" {
			if asg.ProjectID != "prj_a1" && asg.ProjectID != "prj_a2" {
				t.Errorf("Track A judge %s assigned to non-Track A project %s", asg.JudgeUserID, asg.ProjectID)
			}
		}
		if asg.JudgeUserID == "jdg_b1" || asg.JudgeUserID == "jdg_b2" {
			if asg.ProjectID != "prj_b1" && asg.ProjectID != "prj_b2" {
				t.Errorf("Track B judge %s assigned to non-Track B project %s", asg.JudgeUserID, asg.ProjectID)
			}
		}
	}
}

// Test 4 — Capacity constraint: Binding capacity is strictly respected
func TestService_CapacityConstraint(t *testing.T) {
	db, svc := setupTestDB(t)
	ctx := context.Background()

	createTestEvent(t, db, ctx, "evt_cap")
	createTestTrack(t, db, ctx, "trk_01", "evt_cap", "Track 1")

	// 3 projects, target = 2 reviews -> total 6 reviews needed
	createTestProject(t, db, ctx, "prj_01", "evt_cap", "trk_01")
	createTestProject(t, db, ctx, "prj_02", "evt_cap", "trk_01")
	createTestProject(t, db, ctx, "prj_03", "evt_cap", "trk_01")

	// 4 judges with binding capacities: 2, 2, 1, 1 (total capacity = 6 exactly)
	createTestJudge(t, db, ctx, "jdg_01", "evt_cap", 2, []string{"trk_01"})
	createTestJudge(t, db, ctx, "jdg_02", "evt_cap", 2, []string{"trk_01"})
	createTestJudge(t, db, ctx, "jdg_03", "evt_cap", 1, []string{"trk_01"})
	createTestJudge(t, db, ctx, "jdg_04", "evt_cap", 1, []string{"trk_01"})

	res, err := svc.GenerateAssignments(ctx, Config{
		EventID:       "evt_cap",
		TargetReviews: 2,
	})
	if err != nil {
		t.Fatalf("GenerateAssignments failed: %v", err)
	}
	if !res.Feasible {
		t.Fatalf("expected feasible run under exact binding capacity, got %s", res.InfeasibilityCode)
	}

	judgeCounts := make(map[string]int)
	for _, asg := range res.Assignments {
		judgeCounts[asg.JudgeUserID]++
	}

	if judgeCounts["jdg_01"] > 2 || judgeCounts["jdg_02"] > 2 || judgeCounts["jdg_03"] > 1 || judgeCounts["jdg_04"] > 1 {
		t.Errorf("capacity exceeded: %+v", judgeCounts)
	}
}

// Test 5 — Insufficient capacity: required review slots > total eligible capacity -> infeasible
func TestService_InsufficientCapacity(t *testing.T) {
	db, svc := setupTestDB(t)
	ctx := context.Background()

	createTestEvent(t, db, ctx, "evt_insuf")
	createTestTrack(t, db, ctx, "trk_01", "evt_insuf", "Track 1")

	// 5 projects, target = 3 reviews = 15 required review slots
	for i := 1; i <= 5; i++ {
		createTestProject(t, db, ctx, fmt.Sprintf("prj_%02d", i), "evt_insuf", "trk_01")
	}

	// 4 judges, capacity 2 each = 8 total capacity (< 15)
	for i := 1; i <= 4; i++ {
		createTestJudge(t, db, ctx, fmt.Sprintf("jdg_%02d", i), "evt_insuf", 2, []string{"trk_01"})
	}

	res, err := svc.GenerateAssignments(ctx, Config{
		EventID:       "evt_insuf",
		TargetReviews: 3,
		RunID:         "run_insuf_cap",
	})
	if err != nil {
		t.Fatalf("unexpected execution error: %v", err)
	}

	if res.Feasible || res.Status != "INFEASIBLE" {
		t.Fatalf("expected run to be INFEASIBLE, got status=%s", res.Status)
	}

	if res.InfeasibilityCode != CodeInsufficientCapacity {
		t.Errorf("expected code %s, got %s", CodeInsufficientCapacity, res.InfeasibilityCode)
	}

	if res.InfeasibilityDetails == nil || res.InfeasibilityDetails.RequestedTotal != 15 || res.InfeasibilityDetails.AvailableCapacity != 8 {
		t.Errorf("unexpected details: %+v", res.InfeasibilityDetails)
	}

	// Verify NO invalid assignments were persisted
	var asgCount int
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM assignments WHERE assignment_run_id = 'run_insuf_cap';").Scan(&asgCount)
	if asgCount != 0 {
		t.Fatalf("infeasible run must not create assignments, found %d", asgCount)
	}

	// Verify assignment_runs table contains the audit record
	var recordedCode string
	err = db.QueryRowContext(ctx, "SELECT infeasibility_code FROM assignment_runs WHERE id = 'run_insuf_cap';").Scan(&recordedCode)
	if err != nil || recordedCode != CodeInsufficientCapacity {
		t.Fatalf("assignment_runs audit record missing or invalid: %s (err: %v)", recordedCode, err)
	}
}

// Test 6 — No eligible judge: Project whose track has no eligible judges -> infeasible
func TestService_NoEligibleJudge(t *testing.T) {
	db, svc := setupTestDB(t)
	ctx := context.Background()

	createTestEvent(t, db, ctx, "evt_nojdg")
	createTestTrack(t, db, ctx, "trk_orphan", "evt_nojdg", "Orphan Track")
	createTestTrack(t, db, ctx, "trk_normal", "evt_nojdg", "Normal Track")

	createTestProject(t, db, ctx, "prj_orphan", "evt_nojdg", "trk_orphan")
	createTestProject(t, db, ctx, "prj_normal", "evt_nojdg", "trk_normal")

	// Judges only eligible for trk_normal
	createTestJudge(t, db, ctx, "jdg_01", "evt_nojdg", 5, []string{"trk_normal"})
	createTestJudge(t, db, ctx, "jdg_02", "evt_nojdg", 5, []string{"trk_normal"})

	res, err := svc.GenerateAssignments(ctx, Config{
		EventID:       "evt_nojdg",
		TargetReviews: 2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.Feasible || res.Status != "INFEASIBLE" {
		t.Fatalf("expected INFEASIBLE run, got %s", res.Status)
	}

	if res.InfeasibilityCode != CodeNoEligibleJudge {
		t.Errorf("expected %s, got %s", CodeNoEligibleJudge, res.InfeasibilityCode)
	}

	if res.InfeasibilityDetails == nil || len(res.InfeasibilityDetails.AffectedProjects) == 0 {
		t.Fatalf("expected details identifying affected project, got %+v", res.InfeasibilityDetails)
	}

	if res.InfeasibilityDetails.AffectedProjects[0] != "prj_orphan" {
		t.Errorf("expected affected project prj_orphan, got %s", res.InfeasibilityDetails.AffectedProjects[0])
	}
}

// Test 7 — Duplicate prevention: Active judge/project pair cannot receive duplicate assignment
func TestService_DuplicatePrevention(t *testing.T) {
	db, svc := setupTestDB(t)
	ctx := context.Background()

	createTestEvent(t, db, ctx, "evt_dup")
	createTestTrack(t, db, ctx, "trk_01", "evt_dup", "Track 1")
	createTestProject(t, db, ctx, "prj_01", "evt_dup", "trk_01")

	createTestJudge(t, db, ctx, "jdg_01", "evt_dup", 3, []string{"trk_01"})
	createTestJudge(t, db, ctx, "jdg_02", "evt_dup", 3, []string{"trk_01"})

	now := time.Now().UTC().Format(time.RFC3339)

	// Pre-create rubric version
	_, _ = db.ExecContext(ctx, `
		INSERT INTO rubric_versions (id, event_id, rubric_id, version_no, state, criteria_json, created_at)
		VALUES ('rub_v1', 'evt_dup', 'default', 1, 'PUBLISHED', '[]', ?);
	`, now)

	// Pre-insert an existing active assignment for jdg_01 -> prj_01
	_, err := db.ExecContext(ctx, `
		INSERT INTO assignments (id, event_id, judge_user_id, project_id, rubric_version_id, status, assigned_at)
		VALUES ('asg_existing_01', 'evt_dup', 'jdg_01', 'prj_01', 'rub_v1', 'ASSIGNED', ?);
	`, now)
	if err != nil {
		t.Fatalf("failed to insert pre-existing assignment: %v", err)
	}

	// Now run assignment targeting 2 reviews
	// Project already has 1 review (jdg_01). It only needs 1 more, and CANNOT pick jdg_01 again!
	res, err := svc.GenerateAssignments(ctx, Config{
		EventID:       "evt_dup",
		TargetReviews: 2,
	})
	if err != nil {
		t.Fatalf("GenerateAssignments failed: %v", err)
	}

	if !res.Feasible {
		t.Fatalf("expected feasible run, got %s", res.InfeasibilityCode)
	}

	if res.AssignmentsCount != 1 {
		t.Fatalf("expected exactly 1 new assignment, got %d", res.AssignmentsCount)
	}

	if res.Assignments[0].JudgeUserID != "jdg_02" {
		t.Errorf("expected jdg_02 to be assigned, but got %s (duplicate prevention failed!)", res.Assignments[0].JudgeUserID)
	}
}

// Test 8 — Completed assignment preservation: existing completed assignment is never erased or overwritten
func TestService_CompletedAssignmentPreservation(t *testing.T) {
	db, svc := setupTestDB(t)
	ctx := context.Background()

	createTestEvent(t, db, ctx, "evt_comp")
	createTestTrack(t, db, ctx, "trk_01", "evt_comp", "Track 1")
	createTestProject(t, db, ctx, "prj_01", "evt_comp", "trk_01")

	createTestJudge(t, db, ctx, "jdg_01", "evt_comp", 3, []string{"trk_01"})
	createTestJudge(t, db, ctx, "jdg_02", "evt_comp", 3, []string{"trk_01"})

	now := time.Now().UTC().Format(time.RFC3339)

	_, _ = db.ExecContext(ctx, `
		INSERT INTO rubric_versions (id, event_id, rubric_id, version_no, state, criteria_json, created_at)
		VALUES ('rub_v1', 'evt_comp', 'default', 1, 'PUBLISHED', '[]', ?);
	`, now)

	// Pre-create COMPLETED assignment with a ballot
	_, err := db.ExecContext(ctx, `
		INSERT INTO assignments (id, event_id, judge_user_id, project_id, rubric_version_id, status, assigned_at, completed_at)
		VALUES ('asg_completed', 'evt_comp', 'jdg_01', 'prj_01', 'rub_v1', 'COMPLETED', ?, ?);
	`, now, now)
	if err != nil {
		t.Fatalf("failed to insert completed assignment: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO ballot_versions (id, assignment_id, judge_user_id, project_id, rubric_version_id, version_no, save_kind, scores_json, created_at)
		VALUES ('bv_comp', 'asg_completed', 'jdg_01', 'prj_01', 'rub_v1', 1, 'SUBMISSION', '{"q": 5}', ?);
	`, now)
	if err != nil {
		t.Fatalf("failed to insert ballot version: %v", err)
	}

	// Generate assignments with target = 2 reviews
	res, err := svc.GenerateAssignments(ctx, Config{
		EventID:       "evt_comp",
		TargetReviews: 2,
	})
	if err != nil {
		t.Fatalf("GenerateAssignments failed: %v", err)
	}

	// Verify completed assignment is still present and COMPLETED
	var status string
	err = db.QueryRowContext(ctx, "SELECT status FROM assignments WHERE id = 'asg_completed';").Scan(&status)
	if err != nil || status != "COMPLETED" {
		t.Fatalf("completed assignment was overwritten or mutated! status=%s (err: %v)", status, err)
	}

	// Verify ballot version is still intact
	var bvCount int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM ballot_versions WHERE assignment_id = 'asg_completed';").Scan(&bvCount)
	if err != nil || bvCount != 1 {
		t.Fatalf("completed ballot was erased! count=%d", bvCount)
	}

	// Verify only 1 new assignment was added (for jdg_02)
	if res.AssignmentsCount != 1 || res.Assignments[0].JudgeUserID != "jdg_02" {
		t.Errorf("expected 1 new assignment to jdg_02, got %d assignments", res.AssignmentsCount)
	}
}

// Test 9 — Event isolation: Event A generation cannot touch Event B entities
func TestService_EventIsolation(t *testing.T) {
	db, svc := setupTestDB(t)
	ctx := context.Background()

	createTestEvent(t, db, ctx, "evt_a")
	createTestEvent(t, db, ctx, "evt_b")

	createTestTrack(t, db, ctx, "trk_a", "evt_a", "Track A")
	createTestTrack(t, db, ctx, "trk_b", "evt_b", "Track B")

	// Projects in Event A and Event B
	createTestProject(t, db, ctx, "prj_a1", "evt_a", "trk_a")
	createTestProject(t, db, ctx, "prj_b1", "evt_b", "trk_b")

	// Judges in Event A and Event B
	createTestJudge(t, db, ctx, "jdg_a1", "evt_a", 3, []string{"trk_a"})
	createTestJudge(t, db, ctx, "jdg_b1", "evt_b", 3, []string{"trk_b"})

	// Run assignment for Event A
	resA, err := svc.GenerateAssignments(ctx, Config{
		EventID:       "evt_a",
		TargetReviews: 1,
	})
	if err != nil {
		t.Fatalf("GenerateAssignments Event A failed: %v", err)
	}

	if !resA.Feasible || resA.AssignmentsCount != 1 {
		t.Fatalf("expected 1 assignment for Event A, got %d", resA.AssignmentsCount)
	}

	asg := resA.Assignments[0]
	if asg.EventID != "evt_a" || asg.JudgeUserID != "jdg_a1" || asg.ProjectID != "prj_a1" {
		t.Errorf("Event A assignment leaked entities: %+v", asg)
	}

	// Verify no assignments created in Event B
	var bCount int
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM assignments WHERE event_id = 'evt_b';").Scan(&bCount)
	if bCount != 0 {
		t.Errorf("expected 0 assignments in Event B, got %d", bCount)
	}
}

// Test 10 — Reproducible ordering: Assignment results do not depend on SQLite row-return order
func TestService_ReproducibleOrdering(t *testing.T) {
	ctx := context.Background()

	runWithInsertionOrder := func(reverseJudges, reverseProjects bool) []Assignment {
		db, svc := setupTestDB(t)
		createTestEvent(t, db, ctx, "evt_rep")
		createTestTrack(t, db, ctx, "trk_01", "evt_rep", "Track 1")

		pList := []string{"prj_01", "prj_02", "prj_03", "prj_04"}
		if reverseProjects {
			pList = []string{"prj_04", "prj_03", "prj_02", "prj_01"}
		}
		for _, pid := range pList {
			createTestProject(t, db, ctx, pid, "evt_rep", "trk_01")
		}

		jList := []string{"jdg_01", "jdg_02", "jdg_03", "jdg_04"}
		if reverseJudges {
			jList = []string{"jdg_04", "jdg_03", "jdg_02", "jdg_01"}
		}
		for _, jid := range jList {
			createTestJudge(t, db, ctx, jid, "evt_rep", 3, []string{"trk_01"})
		}

		res, err := svc.GenerateAssignments(ctx, Config{
			EventID:       "evt_rep",
			TargetReviews: 2,
			RunID:         "run_fixed",
		})
		if err != nil || !res.Feasible {
			t.Fatalf("failed to generate: %v", err)
		}
		return res.Assignments
	}

	resNormal := runWithInsertionOrder(false, false)
	resReversed := runWithInsertionOrder(true, true)

	if len(resNormal) != len(resReversed) {
		t.Fatalf("length mismatch: %d != %d", len(resNormal), len(resReversed))
	}

	for i := range resNormal {
		if resNormal[i].JudgeUserID != resReversed[i].JudgeUserID || resNormal[i].ProjectID != resReversed[i].ProjectID {
			t.Fatalf("order dependence detected at index %d: normal=(%s -> %s) reversed=(%s -> %s)",
				i, resNormal[i].JudgeUserID, resNormal[i].ProjectID, resReversed[i].JudgeUserID, resReversed[i].ProjectID)
		}
	}
}
