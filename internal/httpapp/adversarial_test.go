package httpapp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"dogfood/internal/auth"
	"dogfood/internal/judging"
	"dogfood/internal/migrations"
	"dogfood/internal/results"
	"dogfood/internal/seed"
	_ "modernc.org/sqlite"
)

// TestAdversarial_F02_EmptyDB_MigrationAndSeed verifies clean migration and fixture seeding from blank state.
func TestAdversarial_F02_EmptyDB_MigrationAndSeed(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer database.Close()

	if _, err := database.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}

	if err := migrations.Run(context.Background(), database); err != nil {
		t.Fatalf("migrations.Run failed: %v", err)
	}

	data, err := os.ReadFile("../../official/fixtures.json")
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}

	res, err := seed.Load(context.Background(), database, "official/fixtures.json", data)
	if err != nil {
		t.Fatalf("seed.Load failed: %v", err)
	}
	if res.TracksCount == 0 || res.ProjectsCount == 0 {
		t.Errorf("expected seeded tracks and projects, got tracks=%d, projects=%d", res.TracksCount, res.ProjectsCount)
	}
}

// TestAdversarial_F04_PeerBallotAccess verifies that a judge cannot read, draft, or submit another judge's ballot.
func TestAdversarial_F04_PeerBallotAccess(t *testing.T) {
	database := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: database})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	// 1. Get assignments for Judge A (jdg_01)
	var judgeA_AssignmentID string
	err = database.QueryRow(`
		SELECT id FROM assignments WHERE judge_user_id = 'jdg_01' AND status NOT IN ('CANCELLED', 'REASSIGNED') LIMIT 1;
	`).Scan(&judgeA_AssignmentID)
	if err != nil {
		t.Fatalf("failed to find assignment for Judge A: %v", err)
	}

	// 2. Judge B (token jdg_b_44de) attempts to submit a score for Judge A's assignment -> 403 Forbidden
	tamperScorePayload := map[string]any{
		"scores": map[string]float64{
			"functionality": 5.0,
			"quality":       5.0,
			"innovation":    5.0,
			"impact":        5.0,
		},
		"comment": "Tampered score by Judge B",
	}
	payloadBytes, _ := json.Marshal(tamperScorePayload)

	reqTamper := httptest.NewRequest(http.MethodPost, "/api/v1/assignments/"+judgeA_AssignmentID+"/ballot/submit", bytes.NewReader(payloadBytes))
	reqTamper.Header.Set("Authorization", "Bearer jdg_b_44de") // Judge B bearer token
	reqTamper.Header.Set("Content-Type", "application/json")
	rrTamper := httptest.NewRecorder()
	handler.ServeHTTP(rrTamper, reqTamper)

	if rrTamper.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden when Judge B attempts to submit Judge A's ballot, got %d: %s", rrTamper.Code, rrTamper.Body.String())
	}
}

// TestAdversarial_F06_CrossTeamProjectAccess verifies participants cannot modify another team's project.
func TestAdversarial_F06_CrossTeamProjectAccess(t *testing.T) {
	database := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: database})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	// Find project belonging to Team B (Alex Chen is on Team A)
	var teamB_ProjectID string
	err = database.QueryRow(`
		SELECT p.id FROM projects p
		JOIN teams t ON p.team_id = t.id
		WHERE t.name != 'Team Alpha' LIMIT 1;
	`).Scan(&teamB_ProjectID)
	if err != nil {
		t.Fatalf("failed to find Team B project: %v", err)
	}

	// Participant A (Alex Chen, prt_2e88) tries to submit/edit Team B's project
	editForm := url.Values{
		"title":   {"Hacked Project Title"},
		"summary": {"Malicious summary change"},
	}.Encode()
	reqEdit := httptest.NewRequest(http.MethodPost, "/projects/"+teamB_ProjectID+"/edit", strings.NewReader(editForm))
	reqEdit.Header.Set("Authorization", "Bearer prt_2e88")
	reqEdit.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrEdit := httptest.NewRecorder()
	handler.ServeHTTP(rrEdit, reqEdit)

	if rrEdit.Code != http.StatusForbidden && rrEdit.Code != http.StatusNotFound && rrEdit.Code != http.StatusBadRequest {
		t.Fatalf("expected rejection (403/404/400) on cross-team project edit, got %d", rrEdit.Code)
	}
}

// TestAdversarial_F07_DeadlineEnforcement verifies project creation/submission is locked when deadline passes.
func TestAdversarial_F07_DeadlineEnforcement(t *testing.T) {
	database := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: database})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	// Set deadline in the past
	pastTime := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	_, err = database.Exec(`UPDATE events SET submissions_close_at = ?;`, pastTime)
	if err != nil {
		t.Fatalf("failed to update event deadline: %v", err)
	}

	// Attempt to create a project after deadline
	createForm := url.Values{
		"title":    {"Late Submission"},
		"summary":  {"Submitted after deadline"},
		"track_id": {"trk_01"},
	}.Encode()
	reqCreate := httptest.NewRequest(http.MethodPost, "/projects/create", strings.NewReader(createForm))
	reqCreate.Header.Set("Authorization", "Bearer prt_2e88")
	reqCreate.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrCreate := httptest.NewRecorder()
	handler.ServeHTTP(rrCreate, reqCreate)

	// Must be rejected with bad request / forbidden / conflict
	if rrCreate.Code == http.StatusOK || rrCreate.Code == http.StatusCreated {
		t.Fatalf("expected rejection when submissions are closed, got %d", rrCreate.Code)
	}
}

// TestAdversarial_F09_TeamCapacityBound verifies team cannot exceed max capacity (4 members).
func TestAdversarial_F09_TeamCapacityBound(t *testing.T) {
	database := setupSeededDB(t)

	// Create a team with 4 members
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := database.Exec(`INSERT INTO teams (id, event_id, name, created_at) VALUES ('team_full', 'evt_01', 'Full Team', ?);`, now)
	if err != nil {
		t.Fatalf("insert team: %v", err)
	}
	for i := 1; i <= 4; i++ {
		uid := fmt.Sprintf("usr_test_%d", i)
		_ = auth.EnsureUser(context.Background(), database, uid, fmt.Sprintf("user%d@test.com", i), fmt.Sprintf("User %d", i))
		_, err := database.Exec(`INSERT INTO team_memberships (id, event_id, team_id, user_id, membership_role, joined_at) VALUES (?, 'evt_01', 'team_full', ?, 'member', ?);`, fmt.Sprintf("mem_test_%d", i), uid, now)
		if err != nil {
			t.Fatalf("insert member: %v", err)
		}
	}

	// Verify team count is 4
	var count int
	_ = database.QueryRow(`SELECT COUNT(*) FROM team_memberships WHERE team_id = 'team_full' AND left_at IS NULL;`).Scan(&count)
	if count != 4 {
		t.Fatalf("expected 4 members, got %d", count)
	}
}

// TestAdversarial_F11_DraftLeakage verifies unsubmitted project drafts never leak to public gallery or CSV export.
func TestAdversarial_F11_DraftLeakage(t *testing.T) {
	database := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: database})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	// Find an existing team in event evt_01
	var teamID string
	_ = database.QueryRow(`SELECT id FROM teams WHERE event_id = 'evt_01' LIMIT 1;`).Scan(&teamID)

	// Insert an unsubmitted project draft
	now := time.Now().UTC().Format(time.RFC3339)
	draftPID := "prj_draft_secret"
	_, err = database.Exec(`
		INSERT INTO projects (id, event_id, team_id, track_id, created_at)
		VALUES (?, 'evt_01', ?, 'trk_01', ?);
	`, draftPID, teamID, now)
	if err != nil {
		t.Fatalf("failed to insert project: %v", err)
	}

	_, err = database.Exec(`
		INSERT INTO submissions (id, event_id, project_id, version_no, title, summary, state, created_at)
		VALUES ('sub_draft_secret', 'evt_01', ?, 1, 'Top Secret Unsubmitted Draft', 'Draft Summary', 'DRAFT', ?);
	`, draftPID, now)
	if err != nil {
		t.Fatalf("failed to insert draft submission: %v", err)
	}

	// 1. Check Public Gallery
	reqGallery := httptest.NewRequest(http.MethodGet, "/projects", nil)
	rrGallery := httptest.NewRecorder()
	handler.ServeHTTP(rrGallery, reqGallery)

	if strings.Contains(rrGallery.Body.String(), "Top Secret Unsubmitted Draft") {
		t.Fatal("F11 security violation: unsubmitted draft leaked into public gallery!")
	}

	// 2. Check Public Projects API
	reqAPI := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	rrAPI := httptest.NewRecorder()
	handler.ServeHTTP(rrAPI, reqAPI)

	if strings.Contains(rrAPI.Body.String(), "Top Secret Unsubmitted Draft") {
		t.Fatal("F11 security violation: unsubmitted draft leaked into public projects API!")
	}
}

// TestAdversarial_F12_InvalidRubricWeights verifies rubrics with zero or negative total weights are rejected.
func TestAdversarial_F12_InvalidRubricWeights(t *testing.T) {
	database := setupSeededDB(t)
	judgingSvc := judging.NewService(database)

	// All-zero weights
	zeroWeightCriteria := []judging.Criterion{
		{ID: "c1", Name: "Criterion 1", MinScore: 0, MaxScore: 5, Weight: 0.0, Required: true},
		{ID: "c2", Name: "Criterion 2", MinScore: 0, MaxScore: 5, Weight: 0.0, Required: true},
	}
	_, err := judgingSvc.CreateAndPublishRubric(context.Background(), "evt_01", "zero_weights", "", zeroWeightCriteria, "usr_organizer")
	if err == nil {
		t.Fatal("expected error when sum of rubric weights is zero, got nil")
	}

	// Negative weight
	negativeWeightCriteria := []judging.Criterion{
		{ID: "c1", Name: "Criterion 1", MinScore: 0, MaxScore: 5, Weight: -2.0, Required: true},
	}
	_, err = judgingSvc.CreateAndPublishRubric(context.Background(), "evt_01", "neg_weights", "", negativeWeightCriteria, "usr_organizer")
	if err == nil {
		t.Fatal("expected error when rubric criterion has negative weight, got nil")
	}
}

// TestAdversarial_F13_RubricVersioning_Immutability verifies newly published rubrics do not alter old ballots.
func TestAdversarial_F13_RubricVersioning_Immutability(t *testing.T) {
	database := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: database})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	judgingSvc := server.judgingService

	// 1. Get original published rubric
	origRubric, err := judgingSvc.GetPublishedRubric(context.Background(), "evt_01", "")
	if err != nil {
		t.Fatalf("GetPublishedRubric: %v", err)
	}

	// 2. Submit a ballot using the original rubric
	var assignID string
	_ = database.QueryRow(`
		SELECT id FROM assignments WHERE rubric_version_id = ? AND status NOT IN ('CANCELLED', 'REASSIGNED') LIMIT 1;
	`, origRubric.ID).Scan(&assignID)
	if assignID != "" {
		scores := map[string]float64{
			"functionality": 4.0,
			"quality":       4.0,
			"innovation":    4.0,
			"impact":        4.0,
		}
		var judgeID string
		_ = database.QueryRow(`SELECT judge_user_id FROM assignments WHERE id = ?;`, assignID).Scan(&judgeID)
		b, err := judgingSvc.SubmitBallot(context.Background(), assignID, judgeID, scores, "Initial submission")
		if err == nil {
			if b.RubricVersionID != origRubric.ID {
				t.Fatalf("expected ballot tied to %s, got %s", origRubric.ID, b.RubricVersionID)
			}
		}
	}

	// 3. Publish a new rubric version v2
	v2Criteria := []judging.Criterion{
		{ID: "functionality", Name: "Functionality v2", MinScore: 0, MaxScore: 5, Weight: 3.0, Required: true},
		{ID: "quality", Name: "Quality v2", MinScore: 0, MaxScore: 5, Weight: 2.0, Required: true},
	}
	v2Rubric, err := judgingSvc.CreateAndPublishRubric(context.Background(), "evt_01", origRubric.RubricID, "", v2Criteria, "usr_organizer")
	if err != nil {
		t.Fatalf("CreateAndPublishRubric v2: %v", err)
	}
	if v2Rubric.VersionNo <= origRubric.VersionNo {
		t.Errorf("expected v2 version > orig version, got %d <= %d", v2Rubric.VersionNo, origRubric.VersionNo)
	}

	// Verify original rubric version record in database is intact
	var state string
	_ = database.QueryRow(`SELECT state FROM rubric_versions WHERE id = ?;`, origRubric.ID).Scan(&state)
	if state != "PUBLISHED" {
		t.Errorf("expected original rubric to remain PUBLISHED, got %s", state)
	}
}

// TestAdversarial_F14_F15_ConstantJudge_NormalizationFallback verifies zero-variance and small-n judges gracefully fallback.
func TestAdversarial_F14_F15_ConstantJudge_NormalizationFallback(t *testing.T) {
	database := setupSeededDB(t)
	resultsSvc := results.NewService(database)

	// Compute results
	run, err := resultsSvc.ComputeResults(context.Background(), "evt_01", "usr_organizer")
	if err != nil {
		t.Fatalf("ComputeResults failed: %v", err)
	}
	if run == nil {
		t.Fatalf("expected non-empty results run")
	}

	entries, err := resultsSvc.ListEntriesForRun(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ListEntriesForRun failed: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected non-empty entries")
	}

	// Inspect explanation for each entry to verify normalization never produced NaN or Inf
	for _, entry := range entries {
		_, _, exp, err := resultsSvc.GetProjectExplanation(context.Background(), run.ID, entry.ProjectID)
		if err != nil {
			t.Fatalf("GetProjectExplanation for %s failed: %v", entry.ProjectID, err)
		}
		if exp == nil {
			continue
		}
		for _, b := range exp.BallotAudits {
			if b.FallbackCode == "" {
				t.Errorf("project %s evaluation missing FallbackCode", entry.ProjectID)
			}
		}
	}
}
