package judging

import (
	"context"
	"database/sql"
	"errors"
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
	db.SetMaxOpenConns(1)
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

func seedBaseEntities(t *testing.T, db *sql.DB, ctx context.Context, eventID string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := db.ExecContext(ctx, `
		INSERT INTO events (id, name, submissions_open_at, submissions_close_at, judging_opens_at, judging_closes_at, created_at, updated_at)
		VALUES (?, 'Judging Event', '2026-01-01T00:00:00Z', '2026-03-01T18:00:00Z', '2026-03-01T18:00:00Z', '2026-03-05T18:00:00Z', ?, ?)
		ON CONFLICT(id) DO NOTHING;
	`, eventID, now, now)
	if err != nil {
		t.Fatalf("failed to insert event: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO users (id, email_normalized, display_name, created_at)
		VALUES 
			('usr_org', 'org@example.org', 'Organizer', ?),
			('jdg_01', 'judge1@example.org', 'Judge One', ?),
			('jdg_02', 'judge2@example.org', 'Judge Two', ?),
			('usr_part', 'part@example.org', 'Participant', ?)
		ON CONFLICT(id) DO NOTHING;
	`, now, now, now, now)
	if err != nil {
		t.Fatalf("failed to insert users: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO tracks (id, event_id, name, created_at)
		VALUES ('trk_01', ?, 'Main Track', ?)
		ON CONFLICT(id) DO NOTHING;
	`, eventID, now)
	if err != nil {
		t.Fatalf("failed to insert track: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO teams (id, event_id, name, created_at)
		VALUES ('tm_01', ?, 'Team 1', ?)
		ON CONFLICT(id) DO NOTHING;
	`, eventID, now)
	if err != nil {
		t.Fatalf("failed to insert team: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO projects (id, event_id, team_id, track_id, eligibility_status, created_at)
		VALUES ('prj_01', ?, 'tm_01', 'trk_01', 'ELIGIBLE', ?)
		ON CONFLICT(id) DO NOTHING;
	`, eventID, now)
	if err != nil {
		t.Fatalf("failed to insert project: %v", err)
	}

	_, err = db.ExecContext(ctx, `
		INSERT INTO submissions (id, event_id, project_id, version_no, state, title, summary, created_at, submitted_at)
		VALUES ('sub_01', ?, 'prj_01', 1, 'SUBMITTED', 'Project Quiet Hours', 'A great tool.', ?, ?)
		ON CONFLICT(id) DO NOTHING;
	`, eventID, now, now)
	if err != nil {
		t.Fatalf("failed to insert submission: %v", err)
	}
}

// 1. Create rubric version
func TestRubric_CreateVersion(t *testing.T) {
	db, svc := setupTestDB(t)
	ctx := context.Background()
	seedBaseEntities(t, db, ctx, "evt_01")

	criteria := []Criterion{
		{ID: "c1", Name: "Tech", MinScore: 0, MaxScore: 10, Weight: 1.0, Required: true},
		{ID: "c2", Name: "Design", MinScore: 0, MaxScore: 5, Weight: 0.5, Required: false},
	}

	rv, err := svc.CreateRubricDraft(ctx, "evt_01", "default", "", criteria, "usr_org")
	if err != nil {
		t.Fatalf("CreateRubricDraft failed: %v", err)
	}

	if rv.VersionNo != 1 || rv.State != "DRAFT" {
		t.Errorf("unexpected rubric state: version=%d state=%s", rv.VersionNo, rv.State)
	}
	if len(rv.Criteria) != 2 {
		t.Errorf("expected 2 criteria, got %d", len(rv.Criteria))
	}
}

// 2. Publish rubric & 4. Published rubric is retrievable
func TestRubric_PublishAndRetrieve(t *testing.T) {
	db, svc := setupTestDB(t)
	ctx := context.Background()
	seedBaseEntities(t, db, ctx, "evt_01")

	criteria := []Criterion{
		{ID: "functionality", Name: "Functionality", MinScore: 0, MaxScore: 5, Weight: 1.0, Required: true},
		{ID: "quality", Name: "Quality", MinScore: 0, MaxScore: 5, Weight: 1.0, Required: true},
	}

	draft, err := svc.CreateRubricDraft(ctx, "evt_01", "default", "", criteria, "usr_org")
	if err != nil {
		t.Fatalf("create draft failed: %v", err)
	}

	published, err := svc.PublishRubricVersion(ctx, draft.ID)
	if err != nil {
		t.Fatalf("publish failed: %v", err)
	}

	if published.State != "PUBLISHED" || published.PublishedAt == "" {
		t.Errorf("expected published rubric with timestamp: %+v", published)
	}

	retrieved, err := svc.GetPublishedRubric(ctx, "evt_01", "")
	if err != nil {
		t.Fatalf("GetPublishedRubric failed: %v", err)
	}

	if retrieved.ID != published.ID {
		t.Errorf("retrieved ID %q != published ID %q", retrieved.ID, published.ID)
	}
}

// 3. Cannot publish invalid rubric
func TestRubric_CannotPublishInvalid(t *testing.T) {
	_, svc := setupTestDB(t)
	ctx := context.Background()

	// Missing criteria
	_, err := svc.CreateAndPublishRubric(ctx, "evt_01", "default", "", []Criterion{}, "usr_org")
	if !errors.Is(err, ErrInvalidCriterion) {
		t.Errorf("expected ErrInvalidCriterion on empty criteria, got %v", err)
	}

	// Negative weight
	badWeights := []Criterion{
		{ID: "c1", Name: "Criterion 1", MinScore: 0, MaxScore: 10, Weight: -1.0, Required: true},
	}
	_, err = svc.CreateAndPublishRubric(ctx, "evt_01", "default", "", badWeights, "usr_org")
	if !errors.Is(err, ErrInvalidCriterion) {
		t.Errorf("expected ErrInvalidCriterion on negative weight, got %v", err)
	}

	// Max <= Min
	badRange := []Criterion{
		{ID: "c1", Name: "Criterion 1", MinScore: 10, MaxScore: 5, Weight: 1.0, Required: true},
	}
	_, err = svc.CreateAndPublishRubric(ctx, "evt_01", "default", "", badRange, "usr_org")
	if !errors.Is(err, ErrInvalidCriterion) {
		t.Errorf("expected ErrInvalidCriterion on bad score range, got %v", err)
	}

	// Duplicate IDs
	duplicateIDs := []Criterion{
		{ID: "c1", Name: "Criterion 1", MinScore: 0, MaxScore: 5, Weight: 1.0, Required: true},
		{ID: "c1", Name: "Criterion 1 Duplicate", MinScore: 0, MaxScore: 5, Weight: 1.0, Required: true},
	}
	_, err = svc.CreateAndPublishRubric(ctx, "evt_01", "default", "", duplicateIDs, "usr_org")
	if !errors.Is(err, ErrInvalidCriterion) {
		t.Errorf("expected ErrInvalidCriterion on duplicate IDs, got %v", err)
	}
}

// 5. Version remains immutable after being used & retiring prior versions on new publish
func TestRubric_ImmutabilityAndVersioning(t *testing.T) {
	db, svc := setupTestDB(t)
	ctx := context.Background()
	seedBaseEntities(t, db, ctx, "evt_01")

	c1 := []Criterion{{ID: "c1", Name: "Crit 1", MinScore: 0, MaxScore: 5, Weight: 1.0, Required: true}}
	v1, err := svc.CreateAndPublishRubric(ctx, "evt_01", "default", "", c1, "usr_org")
	if err != nil {
		t.Fatalf("v1 publish failed: %v", err)
	}

	// Publish v2
	c2 := []Criterion{
		{ID: "c1", Name: "Crit 1", MinScore: 0, MaxScore: 5, Weight: 1.0, Required: true},
		{ID: "c2", Name: "Crit 2", MinScore: 0, MaxScore: 5, Weight: 2.0, Required: true},
	}
	v2Draft, err := svc.CreateRubricDraft(ctx, "evt_01", "default", "", c2, "usr_org")
	if err != nil {
		t.Fatalf("v2 draft failed: %v", err)
	}
	v2, err := svc.PublishRubricVersion(ctx, v2Draft.ID)
	if err != nil {
		t.Fatalf("v2 publish failed: %v", err)
	}

	// v1 should now be RETIRED
	v1Reloaded, err := svc.GetRubricVersionByID(ctx, v1.ID)
	if err != nil {
		t.Fatalf("v1 reload failed: %v", err)
	}
	if v1Reloaded.State != "RETIRED" {
		t.Errorf("expected v1 to be RETIRED after v2 published, got %s", v1Reloaded.State)
	}

	// Active rubric should now be v2
	active, err := svc.GetPublishedRubric(ctx, "evt_01", "")
	if err != nil || active.ID != v2.ID {
		t.Errorf("expected active rubric to be v2, got %+v", active)
	}
}

// 6. Event isolation for rubrics
func TestRubric_EventIsolation(t *testing.T) {
	db, svc := setupTestDB(t)
	ctx := context.Background()
	seedBaseEntities(t, db, ctx, "evt_a")
	seedBaseEntities(t, db, ctx, "evt_b")

	c := []Criterion{{ID: "c1", Name: "Crit", MinScore: 0, MaxScore: 5, Weight: 1.0, Required: true}}
	rvA, err := svc.CreateAndPublishRubric(ctx, "evt_a", "default", "", c, "usr_org")
	if err != nil {
		t.Fatalf("publish A failed: %v", err)
	}

	// evt_b should not find rvA
	_, err = svc.GetPublishedRubric(ctx, "evt_b", "")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for evt_b, got %v", err)
	}

	listA, _ := svc.ListRubrics(ctx, "evt_a")
	if len(listA) != 1 || listA[0].ID != rvA.ID {
		t.Errorf("unexpected list for evt_a: %+v", listA)
	}

	listB, _ := svc.ListRubrics(ctx, "evt_b")
	if len(listB) != 0 {
		t.Errorf("expected 0 rubrics for evt_b, got %d", len(listB))
	}
}

// --- Ballot Tests ---

func setupAssignmentForTest(t *testing.T, db *sql.DB, ctx context.Context, svc *Service, asgID, eventID, judgeID, projectID string) *RubricVersion {
	t.Helper()
	seedBaseEntities(t, db, ctx, eventID)

	rubric, err := svc.EnsureDefaultRubric(ctx, eventID, "usr_org")
	if err != nil {
		t.Fatalf("ensure rubric failed: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err = db.ExecContext(ctx, `
		INSERT INTO assignments (id, event_id, judge_user_id, project_id, rubric_version_id, status, assigned_at)
		VALUES (?, ?, ?, ?, ?, 'ASSIGNED', ?)
		ON CONFLICT(id) DO NOTHING;
	`, asgID, eventID, judgeID, projectID, rubric.ID, now)
	if err != nil {
		t.Fatalf("create assignment failed: %v", err)
	}

	return rubric
}

// 7. Judge can create draft for own assignment
// 8. Judge can update own draft
// 11. Server calculates total
func TestBallot_DraftLifecycleAndCalculation(t *testing.T) {
	db, svc := setupTestDB(t)
	ctx := context.Background()
	setupAssignmentForTest(t, db, ctx, svc, "asg_01", "evt_01", "jdg_01", "prj_01")

	// 1. Create first draft
	scores1 := map[string]float64{
		"functionality": 4.0,
		"quality":       3.0,
	}
	draft1, err := svc.SaveDraftBallot(ctx, "asg_01", "jdg_01", scores1, "Draft note 1")
	if err != nil {
		t.Fatalf("SaveDraftBallot 1 failed: %v", err)
	}

	if draft1.VersionNo != 1 || draft1.SaveKind != "DRAFT" {
		t.Errorf("expected version 1 DRAFT, got ver=%d kind=%s", draft1.VersionNo, draft1.SaveKind)
	}
	if draft1.TotalScore != 7.0 {
		t.Errorf("expected total score 7.0, got %v", draft1.TotalScore)
	}

	// Verify assignment status transitioned to STARTED
	var status string
	_ = db.QueryRowContext(ctx, "SELECT status FROM assignments WHERE id = 'asg_01';").Scan(&status)
	if status != "STARTED" {
		t.Errorf("expected assignment status STARTED, got %s", status)
	}

	// 2. Update draft (appends immutable version 2)
	scores2 := map[string]float64{
		"functionality": 5.0,
		"quality":       4.0,
		"innovation":    4.0,
	}
	draft2, err := svc.SaveDraftBallot(ctx, "asg_01", "jdg_01", scores2, "Updated draft note")
	if err != nil {
		t.Fatalf("SaveDraftBallot 2 failed: %v", err)
	}

	if draft2.VersionNo != 2 {
		t.Errorf("expected version 2, got %d", draft2.VersionNo)
	}
	if draft2.TotalScore != 13.0 {
		t.Errorf("expected total score 13.0, got %v", draft2.TotalScore)
	}

	// Verify latest ballot returns version 2
	latest, err := svc.GetLatestBallot(ctx, "asg_01")
	if err != nil || latest == nil {
		t.Fatalf("GetLatestBallot failed: %v", err)
	}
	if latest.VersionNo != 2 || latest.TotalScore != 13.0 {
		t.Errorf("unexpected latest ballot: %+v", latest)
	}
}

// 9. Required scores are enforced & 10. Score bounds are enforced & 12. Submit valid ballot
func TestBallot_SubmissionValidationAndSuccess(t *testing.T) {
	db, svc := setupTestDB(t)
	ctx := context.Background()
	setupAssignmentForTest(t, db, ctx, svc, "asg_01", "evt_01", "jdg_01", "prj_01")

	// Missing required score
	incompleteScores := map[string]float64{
		"functionality": 4.0,
		"quality":       3.0,
		// missing innovation and impact
	}
	_, err := svc.SubmitBallot(ctx, "asg_01", "jdg_01", incompleteScores, "Incomplete")
	if !errors.Is(err, ErrMissingCriterion) {
		t.Errorf("expected ErrMissingCriterion, got %v", err)
	}

	// Out of bounds score (> 5.0)
	outOfBoundsScores := map[string]float64{
		"functionality": 6.0,
		"quality":       3.0,
		"innovation":    4.0,
		"impact":        3.0,
	}
	_, err = svc.SubmitBallot(ctx, "asg_01", "jdg_01", outOfBoundsScores, "Out of bounds")
	if !errors.Is(err, ErrScoreOutOfBounds) {
		t.Errorf("expected ErrScoreOutOfBounds, got %v", err)
	}

	// Negative score (< 0.0)
	negativeScores := map[string]float64{
		"functionality": -1.0,
		"quality":       3.0,
		"innovation":    4.0,
		"impact":        3.0,
	}
	_, err = svc.SubmitBallot(ctx, "asg_01", "jdg_01", negativeScores, "Negative score")
	if !errors.Is(err, ErrScoreOutOfBounds) {
		t.Errorf("expected ErrScoreOutOfBounds on negative score, got %v", err)
	}

	// Unknown criterion
	unknownScores := map[string]float64{
		"functionality": 4.0,
		"quality":       3.0,
		"innovation":    4.0,
		"impact":        3.0,
		"bogus_field":   5.0,
	}
	_, err = svc.SubmitBallot(ctx, "asg_01", "jdg_01", unknownScores, "Unknown field")
	if !errors.Is(err, ErrInvalidCriterion) {
		t.Errorf("expected ErrInvalidCriterion on unknown field, got %v", err)
	}

	// Valid complete submission
	validScores := map[string]float64{
		"functionality": 4.0,
		"quality":       3.0,
		"innovation":    5.0,
		"impact":        4.0,
	}
	sub, err := svc.SubmitBallot(ctx, "asg_01", "jdg_01", validScores, "Excellent submission!")
	if err != nil {
		t.Fatalf("valid SubmitBallot failed: %v", err)
	}

	if sub.SaveKind != "SUBMISSION" {
		t.Errorf("expected save_kind SUBMISSION, got %s", sub.SaveKind)
	}
	// Total = 4 + 3 + 5 + 4 = 16
	// Weighted average = 16 / 4 = 4.0
	if sub.TotalScore != 16.0 || sub.WeightedScore != 4.0 {
		t.Errorf("expected total=16.0 weighted=4.0, got total=%v weighted=%v", sub.TotalScore, sub.WeightedScore)
	}

	// Verify assignment status transitioned to COMPLETED
	var status string
	_ = db.QueryRowContext(ctx, "SELECT status FROM assignments WHERE id = 'asg_01';").Scan(&status)
	if status != "COMPLETED" {
		t.Errorf("expected assignment status COMPLETED, got %s", status)
	}
}

// 13. Submitted ballot cannot be edited
// 14. Duplicate submission is rejected safely
func TestBallot_ImmutabilityAfterSubmission(t *testing.T) {
	db, svc := setupTestDB(t)
	ctx := context.Background()
	setupAssignmentForTest(t, db, ctx, svc, "asg_01", "evt_01", "jdg_01", "prj_01")

	validScores := map[string]float64{
		"functionality": 4.0,
		"quality":       4.0,
		"innovation":    4.0,
		"impact":        4.0,
	}
	_, err := svc.SubmitBallot(ctx, "asg_01", "jdg_01", validScores, "Final")
	if err != nil {
		t.Fatalf("initial submit failed: %v", err)
	}

	// Attempt to save draft on submitted ballot must fail with ErrBallotSubmitted
	_, err = svc.SaveDraftBallot(ctx, "asg_01", "jdg_01", validScores, "Attempting draft edit")
	if !errors.Is(err, ErrBallotSubmitted) {
		t.Errorf("expected ErrBallotSubmitted on draft edit after submit, got %v", err)
	}

	// Attempt duplicate submission must also fail with ErrBallotSubmitted
	_, err = svc.SubmitBallot(ctx, "asg_01", "jdg_01", validScores, "Attempting duplicate submit")
	if !errors.Is(err, ErrBallotSubmitted) {
		t.Errorf("expected ErrBallotSubmitted on duplicate submit, got %v", err)
	}
}

// 15. Judge cannot access peer assignment ballot
// 16. Judge cannot modify peer ballot
func TestBallot_PeerIsolationEnforced(t *testing.T) {
	db, svc := setupTestDB(t)
	ctx := context.Background()
	setupAssignmentForTest(t, db, ctx, svc, "asg_judge1", "evt_01", "jdg_01", "prj_01")

	// Judge 2 attempts to read Judge 1's assignment details
	_, err := svc.GetAssignmentDetail(ctx, "asg_judge1", "jdg_02", false)
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("expected ErrForbidden for peer judge read, got %v", err)
	}

	// Judge 2 attempts to save draft on Judge 1's assignment
	scores := map[string]float64{"functionality": 3.0}
	_, err = svc.SaveDraftBallot(ctx, "asg_judge1", "jdg_02", scores, "Hacking peer ballot")
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("expected ErrForbidden for peer draft write, got %v", err)
	}

	// Judge 2 attempts to submit Judge 1's assignment
	validScores := map[string]float64{
		"functionality": 4.0, "quality": 4.0, "innovation": 4.0, "impact": 4.0,
	}
	_, err = svc.SubmitBallot(ctx, "asg_judge1", "jdg_02", validScores, "Hacking peer submit")
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("expected ErrForbidden for peer submit, got %v", err)
	}
}

// 19. Submitted ballot references exact rubric version
func TestBallot_ReferencesExactRubricVersion(t *testing.T) {
	db, svc := setupTestDB(t)
	ctx := context.Background()
	rubric := setupAssignmentForTest(t, db, ctx, svc, "asg_01", "evt_01", "jdg_01", "prj_01")

	scores := map[string]float64{
		"functionality": 5.0, "quality": 5.0, "innovation": 5.0, "impact": 5.0,
	}
	sub, err := svc.SubmitBallot(ctx, "asg_01", "jdg_01", scores, "Perfect score")
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	if sub.RubricVersionID != rubric.ID {
		t.Errorf("submitted rubric version %q != expected %q", sub.RubricVersionID, rubric.ID)
	}

	var storedRubricID string
	err = db.QueryRowContext(ctx, "SELECT rubric_version_id FROM ballot_versions WHERE id = ?;", sub.ID).Scan(&storedRubricID)
	if err != nil || storedRubricID != rubric.ID {
		t.Errorf("database ballot_versions rubric FK mismatch: %q != %q", storedRubricID, rubric.ID)
	}
}

// 20. Existing assignment history remains untouched
func TestBallot_AssignmentHistoryPreserved(t *testing.T) {
	db, svc := setupTestDB(t)
	ctx := context.Background()
	setupAssignmentForTest(t, db, ctx, svc, "asg_01", "evt_01", "jdg_01", "prj_01")

	// Save draft 1
	d1, _ := svc.SaveDraftBallot(ctx, "asg_01", "jdg_01", map[string]float64{"functionality": 2.0}, "draft 1")
	// Save draft 2
	d2, _ := svc.SaveDraftBallot(ctx, "asg_01", "jdg_01", map[string]float64{"functionality": 3.0}, "draft 2")
	// Submit
	sub, _ := svc.SubmitBallot(ctx, "asg_01", "jdg_01", map[string]float64{
		"functionality": 4.0, "quality": 4.0, "innovation": 4.0, "impact": 4.0,
	}, "final submission")

	// Count total rows in ballot_versions for asg_01
	var count int
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM ballot_versions WHERE assignment_id = 'asg_01';").Scan(&count)
	if count != 3 {
		t.Fatalf("expected all 3 versions preserved in history, found %d", count)
	}

	// Verify all versions still exist by ID
	for _, id := range []string{d1.ID, d2.ID, sub.ID} {
		var exists int
		_ = db.QueryRowContext(ctx, "SELECT 1 FROM ballot_versions WHERE id = ?;", id).Scan(&exists)
		if exists != 1 {
			t.Errorf("historical version %s was lost!", id)
		}
	}
}
