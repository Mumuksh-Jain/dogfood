package httpapp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dogfood/internal/judging"
)

// TestOrganizerJudging_ProgressAPI_Authorization tests endpoint authentication and role isolation.
func TestOrganizerJudging_ProgressAPI_Authorization(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	// 1. Unauthenticated request -> 401
	reqUnauth := httptest.NewRequest(http.MethodGet, "/api/v1/organizer/progress", nil)
	rrUnauth := httptest.NewRecorder()
	handler.ServeHTTP(rrUnauth, reqUnauth)
	if rrUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", rrUnauth.Code)
	}

	// 2. Participant request -> 403 Forbidden
	reqPart := httptest.NewRequest(http.MethodGet, "/api/v1/organizer/progress", nil)
	reqPart.Header.Set("Authorization", "Bearer prt_2e88")
	rrPart := httptest.NewRecorder()
	handler.ServeHTTP(rrPart, reqPart)
	if rrPart.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for participant, got %d", rrPart.Code)
	}

	// 3. Judge request -> 403 Forbidden
	reqJudge := httptest.NewRequest(http.MethodGet, "/api/v1/organizer/progress", nil)
	reqJudge.Header.Set("Authorization", "Bearer jdg_a_91bc")
	rrJudge := httptest.NewRecorder()
	handler.ServeHTTP(rrJudge, reqJudge)
	if rrJudge.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for judge, got %d", rrJudge.Code)
	}

	// 4. Organizer request -> 200 OK
	reqOrg := httptest.NewRequest(http.MethodGet, "/api/v1/organizer/progress", nil)
	reqOrg.Header.Set("Authorization", "Bearer org_7f2a")
	rrOrg := httptest.NewRecorder()
	handler.ServeHTTP(rrOrg, reqOrg)
	if rrOrg.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for organizer, got %d: %s", rrOrg.Code, rrOrg.Body.String())
	}

	var resp struct {
		EventID  string                     `json:"event_id"`
		Progress *OrganizerJudgingProgress  `json:"progress"`
		Judges   []OrganizerJudgeProgressView `json:"judges"`
		Projects []OrganizerProjectCoverageView `json:"projects"`
		Tracks   []OrganizerTrackProgressView   `json:"tracks"`
	}
	if err := json.NewDecoder(rrOrg.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode progress response: %v", err)
	}

	if resp.Progress == nil {
		t.Fatal("expected non-nil progress in response")
	}
	if resp.Progress.TotalJudges == 0 {
		t.Errorf("expected > 0 total judges, got %d", resp.Progress.TotalJudges)
	}
	if len(resp.Judges) == 0 {
		t.Errorf("expected judge progress entries, got 0")
	}
}

// TestOrganizerJudging_JudgeAdministration tests inviting judges and updating their eligibility.
func TestOrganizerJudging_JudgeAdministration(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	// 1. Create / invite a new judge via JSON API
	newJudgePayload := map[string]any{
		"email":        "dr.watson@example.com",
		"display_name": "Dr. John Watson",
		"capacity":     7,
		"track_ids":    []string{"trk_01", "trk_02"},
	}
	payloadBytes, _ := json.Marshal(newJudgePayload)
	reqAdd := httptest.NewRequest(http.MethodPost, "/api/v1/organizer/judges", bytes.NewReader(payloadBytes))
	reqAdd.Header.Set("Authorization", "Bearer org_7f2a")
	reqAdd.Header.Set("Content-Type", "application/json")
	rrAdd := httptest.NewRecorder()
	handler.ServeHTTP(rrAdd, reqAdd)

	if rrAdd.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", rrAdd.Code, rrAdd.Body.String())
	}

	var createdJudge struct {
		Status      string   `json:"status"`
		JudgeUserID string   `json:"judge_user_id"`
		Email       string   `json:"email"`
		Capacity    int      `json:"capacity"`
		Tracks      []string `json:"eligible_tracks"`
	}
	if err := json.NewDecoder(rrAdd.Body).Decode(&createdJudge); err != nil {
		t.Fatalf("failed to decode created judge: %v", err)
	}
	if createdJudge.JudgeUserID == "" {
		t.Fatal("expected judge_user_id in created response")
	}
	if createdJudge.Capacity != 7 {
		t.Errorf("expected capacity 7, got %d", createdJudge.Capacity)
	}

	// Verify judge in DB
	var roleCount int
	_ = db.QueryRow(`SELECT COUNT(*) FROM event_roles WHERE user_id = ? AND role = 'judge';`, createdJudge.JudgeUserID).Scan(&roleCount)
	if roleCount != 1 {
		t.Errorf("expected 1 judge role for user %s, got %d", createdJudge.JudgeUserID, roleCount)
	}

	var eligCount int
	_ = db.QueryRow(`SELECT COUNT(*) FROM judge_track_eligibility WHERE judge_user_id = ? AND eligible = 1;`, createdJudge.JudgeUserID).Scan(&eligCount)
	if eligCount != 2 {
		t.Errorf("expected 2 eligible tracks, got %d", eligCount)
	}

	// 2. Update judge capacity & eligibility
	updatePayload := map[string]any{
		"capacity": 10,
		"tracks":   []string{"trk_01"},
	}
	updateBytes, _ := json.Marshal(updatePayload)
	reqUpdate := httptest.NewRequest(http.MethodPost, "/api/v1/organizer/judges/"+createdJudge.JudgeUserID+"/eligibility", bytes.NewReader(updateBytes))
	reqUpdate.Header.Set("Authorization", "Bearer org_7f2a")
	reqUpdate.Header.Set("Content-Type", "application/json")
	rrUpdate := httptest.NewRecorder()
	handler.ServeHTTP(rrUpdate, reqUpdate)

	if rrUpdate.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from eligibility update, got %d: %s", rrUpdate.Code, rrUpdate.Body.String())
	}

	var newCap int
	_ = db.QueryRow(`SELECT capacity FROM judge_profiles WHERE user_id = ?;`, createdJudge.JudgeUserID).Scan(&newCap)
	if newCap != 10 {
		t.Errorf("expected updated capacity 10, got %d", newCap)
	}

	_ = db.QueryRow(`SELECT COUNT(*) FROM judge_track_eligibility WHERE judge_user_id = ? AND eligible = 1;`, createdJudge.JudgeUserID).Scan(&eligCount)
	if eligCount != 1 {
		t.Errorf("expected 1 eligible track after update, got %d", eligCount)
	}
}

// TestOrganizerJudging_RubricWeightAuthoring tests creating and publishing customized rubric versions.
func TestOrganizerJudging_RubricWeightAuthoring(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	// 1. Author and publish new rubric version with custom weights
	customCriteria := []judging.Criterion{
		{
			ID:          "functionality",
			Name:        "Functionality & Feature Completeness",
			Description: "Robust and complete implementation",
			MinScore:    0,
			MaxScore:    5,
			Weight:      2.5, // High priority
			Required:    true,
		},
		{
			ID:          "quality",
			Name:        "Code Quality & Technical Design",
			Description: "Architecture and test suite",
			MinScore:    0,
			MaxScore:    5,
			Weight:      1.5,
			Required:    true,
		},
		{
			ID:          "innovation",
			Name:        "Innovation & Creativity",
			Description: "Unique approach and novelty",
			MinScore:    0,
			MaxScore:    5,
			Weight:      1.0,
			Required:    true,
		},
		{
			ID:          "impact",
			Name:        "Impact & Practical Usability",
			Description: "Market value and usability",
			MinScore:    0,
			MaxScore:    5,
			Weight:      1.0,
			Required:    true,
		},
	}

	rubricPayload := map[string]any{
		"rubric_id": "default",
		"track_id":  "",
		"criteria":  customCriteria,
	}
	payloadBytes, _ := json.Marshal(rubricPayload)
	reqRubric := httptest.NewRequest(http.MethodPost, "/api/v1/organizer/rubrics", bytes.NewReader(payloadBytes))
	reqRubric.Header.Set("Authorization", "Bearer org_7f2a")
	reqRubric.Header.Set("Content-Type", "application/json")
	rrRubric := httptest.NewRecorder()
	handler.ServeHTTP(rrRubric, reqRubric)

	if rrRubric.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created from rubric creation, got %d: %s", rrRubric.Code, rrRubric.Body.String())
	}

	var createdRubric judging.RubricVersion
	if err := json.NewDecoder(rrRubric.Body).Decode(&createdRubric); err != nil {
		t.Fatalf("failed to decode created rubric: %v", err)
	}

	if createdRubric.VersionNo < 2 {
		t.Errorf("expected version >= 2, got %d", createdRubric.VersionNo)
	}
	if createdRubric.State != "PUBLISHED" {
		t.Errorf("expected state PUBLISHED, got %s", createdRubric.State)
	}
	if createdRubric.ConfigurationHash == "" {
		t.Error("expected non-empty configuration_hash")
	}

	// 2. Reject rubric with invalid weight (negative weight)
	invalidCriteria := []judging.Criterion{
		{
			ID:       "functionality",
			Name:     "Functionality",
			MinScore: 0,
			MaxScore: 5,
			Weight:   -1.0, // Negative weight is invalid
			Required: true,
		},
	}
	invalidPayload := map[string]any{
		"rubric_id": "default",
		"criteria":  invalidCriteria,
	}
	invBytes, _ := json.Marshal(invalidPayload)
	reqInv := httptest.NewRequest(http.MethodPost, "/api/v1/organizer/rubrics", bytes.NewReader(invBytes))
	reqInv.Header.Set("Authorization", "Bearer org_7f2a")
	reqInv.Header.Set("Content-Type", "application/json")
	rrInv := httptest.NewRecorder()
	handler.ServeHTTP(rrInv, reqInv)

	if rrInv.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for negative weight, got %d: %s", rrInv.Code, rrInv.Body.String())
	}
}

// TestOrganizerJudging_DashboardUI tests dashboard HTML rendering of judging operations.
func TestOrganizerJudging_DashboardUI(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	// 1. Organizer Dashboard contains judging progress control room
	reqOrg := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	reqOrg.Header.Set("Authorization", "Bearer org_7f2a")
	rrOrg := httptest.NewRecorder()
	handler.ServeHTTP(rrOrg, reqOrg)

	if rrOrg.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for organizer dashboard, got %d", rrOrg.Code)
	}
	orgHTML := rrOrg.Body.String()

	expectedSnippets := []string{
		"Judging Operations &amp; Live Progress",
		"Judge Roster &amp; Reviewer Progress",
		"Project Review Coverage",
		"Track Completion Breakdown",
		"Author New Rubric Version",
		"Publish New Rubric Version",
	}
	for _, snip := range expectedSnippets {
		if !strings.Contains(orgHTML, snip) {
			t.Errorf("organizer dashboard HTML missing snippet: %q", snip)
		}
	}

	// 2. Participant Dashboard does NOT contain organizer judging controls
	reqPart := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	reqPart.Header.Set("Authorization", "Bearer prt_2e88")
	rrPart := httptest.NewRecorder()
	handler.ServeHTTP(rrPart, reqPart)

	if rrPart.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for participant dashboard, got %d", rrPart.Code)
	}
	partHTML := rrPart.Body.String()

	forbiddenForParticipant := []string{
		"Judging Operations &amp; Live Progress",
		"Judge Roster &amp; Reviewer Progress",
		"Author New Rubric Version",
	}
	for _, snip := range forbiddenForParticipant {
		if strings.Contains(partHTML, snip) {
			t.Errorf("participant dashboard should not contain organizer snippet: %q", snip)
		}
	}
}
