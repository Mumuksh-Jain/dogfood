package httpapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"dogfood/internal/auth"
	"dogfood/internal/migrations"
	"dogfood/internal/seed"
	_ "modernc.org/sqlite"
)

func setupSeededDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	if err := migrations.Run(ctx, db); err != nil {
		t.Fatalf("migrations.Run: %v", err)
	}

	data, err := os.ReadFile("../../official/fixtures.json")
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	if _, err := seed.Load(ctx, db, "official/fixtures.json", data); err != nil {
		t.Fatalf("seed.Load: %v", err)
	}
	return db
}

func TestServer_VerticalSliceAcceptance(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	handler := server.Handler()

	// 1. T1: Public Gallery returns 200 and includes fixture project titles
	reqGallery := httptest.NewRequest(http.MethodGet, "/projects", nil)
	rrGallery := httptest.NewRecorder()
	handler.ServeHTTP(rrGallery, reqGallery)

	if rrGallery.Code != http.StatusOK {
		t.Errorf("gallery: expected 200, got %d", rrGallery.Code)
	}
	if !strings.Contains(rrGallery.Body.String(), "Glass Signal") {
		t.Errorf("gallery: missing fixture title 'Glass Signal' in body")
	}

	// 2. T1: Closed event refuses submissions with 4xx
	reqSubmit := httptest.NewRequest(http.MethodPost, "/projects/new", strings.NewReader(`{"title":"probe","summary":"probe"}`))
	reqSubmit.Header.Set("Cookie", "session=prt_2e88")
	reqSubmit.Header.Set("Content-Type", "application/json")
	rrSubmit := httptest.NewRecorder()
	handler.ServeHTTP(rrSubmit, reqSubmit)

	if rrSubmit.Code < 400 || rrSubmit.Code >= 500 {
		t.Errorf("submit: expected 4xx status, got %d", rrSubmit.Code)
	}

	// 3. T2: Judge A sees own scores (200 OK)
	reqJudgeA := httptest.NewRequest(http.MethodGet, "/api/judge/scores", nil)
	reqJudgeA.Header.Set("Cookie", "session=jdg_a_91bc")
	rrJudgeA := httptest.NewRecorder()
	handler.ServeHTTP(rrJudgeA, reqJudgeA)

	if rrJudgeA.Code != http.StatusOK {
		t.Errorf("judge_a own scores: expected 200, got %d", rrJudgeA.Code)
	}

	// 4. T2: Judge B cannot see peer scores (401 or 403)
	reqJudgeBPeer := httptest.NewRequest(http.MethodGet, "/api/judge/scores?judge=judge_a", nil)
	reqJudgeBPeer.Header.Set("Cookie", "session=jdg_b_44de")
	rrJudgeBPeer := httptest.NewRecorder()
	handler.ServeHTTP(rrJudgeBPeer, reqJudgeBPeer)

	if rrJudgeBPeer.Code != http.StatusUnauthorized && rrJudgeBPeer.Code != http.StatusForbidden {
		t.Errorf("judge_b peer access: expected 401/403, got %d", rrJudgeBPeer.Code)
	}

	// 5. T2: Participant blocked from judge scores (401 or 403)
	reqPart := httptest.NewRequest(http.MethodGet, "/api/judge/scores", nil)
	reqPart.Header.Set("Cookie", "session=prt_2e88")
	rrPart := httptest.NewRecorder()
	handler.ServeHTTP(rrPart, reqPart)

	if rrPart.Code != http.StatusUnauthorized && rrPart.Code != http.StatusForbidden {
		t.Errorf("participant blocked: expected 401/403, got %d", rrPart.Code)
	}

	// 6. T2: CSV export works for organizer (200 OK, comma in first line)
	reqCSV := httptest.NewRequest(http.MethodGet, "/api/export.csv", nil)
	reqCSV.Header.Set("Cookie", "session=org_7f2a")
	rrCSV := httptest.NewRecorder()
	handler.ServeHTTP(rrCSV, reqCSV)

	if rrCSV.Code != http.StatusOK {
		t.Errorf("csv export: expected 200, got %d", rrCSV.Code)
	}
	lines := strings.Split(rrCSV.Body.String(), "\n")
	if len(lines) == 0 || !strings.Contains(lines[0], ",") {
		t.Errorf("csv export: expected comma in first line, got: %q", rrCSV.Body.String())
	}
}

func TestHealthz(t *testing.T) {
	server, err := NewServer(Config{Port: 8080})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	server.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var res map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to decode json response: %v", err)
	}

	if res["status"] != "ok" {
		t.Errorf("expected status 'ok', got '%s'", res["status"])
	}
}

func TestT1_AuthAndSessionFlow(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	// 1. Unauthenticated dashboard redirects to /login
	reqDash := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rrDash := httptest.NewRecorder()
	handler.ServeHTTP(rrDash, reqDash)
	if rrDash.Code != http.StatusSeeOther {
		t.Errorf("unauthenticated dashboard: expected 303, got %d", rrDash.Code)
	}

	// 2. Login as existing participant
	reqLogin := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email=priya1@example.org"))
	reqLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrLogin := httptest.NewRecorder()
	handler.ServeHTTP(rrLogin, reqLogin)
	if rrLogin.Code != http.StatusSeeOther {
		t.Fatalf("login failed: expected 303, got %d", rrLogin.Code)
	}

	// Extract session cookie
	cookies := rrLogin.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatalf("expected session cookie on successful login")
	}

	// 3. Authenticated dashboard succeeds
	reqDashAuth := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	reqDashAuth.AddCookie(sessionCookie)
	rrDashAuth := httptest.NewRecorder()
	handler.ServeHTTP(rrDashAuth, reqDashAuth)
	if rrDashAuth.Code != http.StatusOK {
		t.Errorf("authenticated dashboard: expected 200, got %d", rrDashAuth.Code)
	}
	if !strings.Contains(rrDashAuth.Body.String(), "priya1@example.org") {
		t.Errorf("expected user email in dashboard body")
	}

	// 4. Logout clears session
	reqLogout := httptest.NewRequest(http.MethodGet, "/logout", nil)
	reqLogout.AddCookie(sessionCookie)
	rrLogout := httptest.NewRecorder()
	handler.ServeHTTP(rrLogout, reqLogout)
	if rrLogout.Code != http.StatusSeeOther {
		t.Errorf("logout: expected 303, got %d", rrLogout.Code)
	}

	// 5. Subsequent dashboard request fails
	reqDashAfter := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	reqDashAfter.AddCookie(sessionCookie)
	rrDashAfter := httptest.NewRecorder()
	handler.ServeHTTP(rrDashAfter, reqDashAfter)
	if rrDashAfter.Code != http.StatusSeeOther {
		t.Errorf("dashboard after logout: expected 303, got %d", rrDashAfter.Code)
	}
}

func TestT1_TeamCapacity_And_InviteReplayProtection(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	// 1. Priya creates a new team "CapacityGuard"
	reqTeam := httptest.NewRequest(http.MethodPost, "/api/teams", strings.NewReader(`{"name":"CapacityGuard"}`))
	reqTeam.Header.Set("Cookie", "session=prt_2e88")
	reqTeam.Header.Set("Content-Type", "application/json")
	reqTeam.Header.Set("Accept", "application/json")
	rrTeam := httptest.NewRecorder()
	handler.ServeHTTP(rrTeam, reqTeam)

	if rrTeam.Code != http.StatusOK {
		t.Fatalf("create team failed: %d: %s", rrTeam.Code, rrTeam.Body.String())
	}
	var teamRes struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	_ = json.Unmarshal(rrTeam.Body.Bytes(), &teamRes)
	newTeamID := teamRes.ID

	// Team now has 1 member (Priya).
	// Query 4 distinct fixture users who are not yet on this team.
	rows, err := db.Query("SELECT email_normalized FROM users WHERE email_normalized LIKE 'member%' LIMIT 4;")
	if err != nil {
		t.Fatalf("query test users: %v", err)
	}
	var testEmails []string
	for rows.Next() {
		var em string
		_ = rows.Scan(&em)
		testEmails = append(testEmails, em)
	}
	rows.Close()

	// Priya generates an invite
	reqInvite1 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/teams/%s/invites", newTeamID), nil)
	reqInvite1.Header.Set("Cookie", "session=prt_2e88")
	reqInvite1.Header.Set("Accept", "application/json")
	rrInvite1 := httptest.NewRecorder()
	handler.ServeHTTP(rrInvite1, reqInvite1)
	var invRes1 struct {
		InviteToken string `json:"invite_token"`
	}
	_ = json.Unmarshal(rrInvite1.Body.Bytes(), &invRes1)

	// Member 2 joins using invite 1
	reqLog2 := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email="+testEmails[0]))
	reqLog2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrLog2 := httptest.NewRecorder()
	handler.ServeHTTP(rrLog2, reqLog2)
	cookie2 := rrLog2.Result().Cookies()[0]

	reqJoin2 := httptest.NewRequest(http.MethodPost, "/api/teams/join?token="+invRes1.InviteToken, nil)
	reqJoin2.AddCookie(cookie2)
	reqJoin2.Header.Set("Accept", "application/json")
	rrJoin2 := httptest.NewRecorder()
	handler.ServeHTTP(rrJoin2, reqJoin2)
	if rrJoin2.Code != http.StatusOK {
		t.Fatalf("member 2 join failed: %d: %s", rrJoin2.Code, rrJoin2.Body.String())
	}

	// Invariant: Replay protection — attempting to reuse invite 1 must fail with 400
	reqLog3 := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email="+testEmails[1]))
	reqLog3.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrLog3 := httptest.NewRecorder()
	handler.ServeHTTP(rrLog3, reqLog3)
	cookie3 := rrLog3.Result().Cookies()[0]

	reqReplay := httptest.NewRequest(http.MethodPost, "/api/teams/join?token="+invRes1.InviteToken, nil)
	reqReplay.AddCookie(cookie3)
	reqReplay.Header.Set("Accept", "application/json")
	rrReplay := httptest.NewRecorder()
	handler.ServeHTTP(rrReplay, reqReplay)
	if rrReplay.Code != http.StatusBadRequest {
		t.Errorf("expected invite replay to fail with 400, got %d", rrReplay.Code)
	}

	// Generate invite 2 for member 3
	reqInvite2 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/teams/%s/invites", newTeamID), nil)
	reqInvite2.Header.Set("Cookie", "session=prt_2e88")
	reqInvite2.Header.Set("Accept", "application/json")
	rrInvite2 := httptest.NewRecorder()
	handler.ServeHTTP(rrInvite2, reqInvite2)
	var invRes2 struct {
		InviteToken string `json:"invite_token"`
	}
	_ = json.Unmarshal(rrInvite2.Body.Bytes(), &invRes2)

	// Member 3 joins with invite 2
	reqJoin3 := httptest.NewRequest(http.MethodPost, "/api/teams/join?token="+invRes2.InviteToken, nil)
	reqJoin3.AddCookie(cookie3)
	reqJoin3.Header.Set("Accept", "application/json")
	rrJoin3 := httptest.NewRecorder()
	handler.ServeHTTP(rrJoin3, reqJoin3)
	if rrJoin3.Code != http.StatusOK {
		t.Fatalf("member 3 join failed: %d: %s", rrJoin3.Code, rrJoin3.Body.String())
	}

	// Generate invite 3 for member 4
	reqInvite3 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/teams/%s/invites", newTeamID), nil)
	reqInvite3.Header.Set("Cookie", "session=prt_2e88")
	reqInvite3.Header.Set("Accept", "application/json")
	rrInvite3 := httptest.NewRecorder()
	handler.ServeHTTP(rrInvite3, reqInvite3)
	var invRes3 struct {
		InviteToken string `json:"invite_token"`
	}
	_ = json.Unmarshal(rrInvite3.Body.Bytes(), &invRes3)

	// Member 4 joins with invite 3
	reqLog4 := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email="+testEmails[2]))
	reqLog4.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrLog4 := httptest.NewRecorder()
	handler.ServeHTTP(rrLog4, reqLog4)
	cookie4 := rrLog4.Result().Cookies()[0]

	reqJoin4 := httptest.NewRequest(http.MethodPost, "/api/teams/join?token="+invRes3.InviteToken, nil)
	reqJoin4.AddCookie(cookie4)
	reqJoin4.Header.Set("Accept", "application/json")
	rrJoin4 := httptest.NewRecorder()
	handler.ServeHTTP(rrJoin4, reqJoin4)
	if rrJoin4.Code != http.StatusOK {
		t.Fatalf("member 4 join failed: %d: %s", rrJoin4.Code, rrJoin4.Body.String())
	}

	// Verify team now has exactly 4 members
	var activeMembers int
	_ = db.QueryRow("SELECT COUNT(*) FROM team_memberships WHERE team_id = ? AND left_at IS NULL;", newTeamID).Scan(&activeMembers)
	if activeMembers != 4 {
		t.Fatalf("expected 4 active members, got %d", activeMembers)
	}

	// Attempting to generate a new invite now fails because team is full (4/4)
	reqInviteFull := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/teams/%s/invites", newTeamID), nil)
	reqInviteFull.Header.Set("Cookie", "session=prt_2e88")
	rrInviteFull := httptest.NewRecorder()
	handler.ServeHTTP(rrInviteFull, reqInviteFull)
	if rrInviteFull.Code != http.StatusBadRequest {
		t.Errorf("expected invite creation on full team to fail with 400, got %d", rrInviteFull.Code)
	}

	// Invariant: 5th member joining MUST fail with 409 Conflict
	// Manually insert an unexpired invite token in DB to test the join transaction boundary directly
	fakeToken := "valid_unexpired_token_for_full_team_test"
	fakeHash := auth.HashToken(fakeToken)
	var priyaID string
	_ = db.QueryRow("SELECT user_id FROM sessions WHERE token_hash = ?;", auth.HashToken("prt_2e88")).Scan(&priyaID)
	_, err = db.Exec("INSERT INTO team_invites (id, event_id, team_id, token_hash, created_by, created_at, expires_at) VALUES ('inv_test_full', 'evt_01', ?, ?, ?, '2026-01-01T00:00:00Z', '2030-01-01T00:00:00Z');", newTeamID, fakeHash, priyaID)
	if err != nil {
		t.Fatalf("insert test invite: %v", err)
	}

	reqLog5 := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email="+testEmails[3]))
	reqLog5.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrLog5 := httptest.NewRecorder()
	handler.ServeHTTP(rrLog5, reqLog5)
	cookie5 := rrLog5.Result().Cookies()[0]

	reqJoin5 := httptest.NewRequest(http.MethodPost, "/api/teams/join?token="+fakeToken, nil)
	reqJoin5.AddCookie(cookie5)
	reqJoin5.Header.Set("Accept", "application/json")
	rrJoin5 := httptest.NewRecorder()
	handler.ServeHTTP(rrJoin5, reqJoin5)

	if rrJoin5.Code != http.StatusConflict {
		t.Errorf("5th member join on 4-member team: expected 409 Conflict, got %d", rrJoin5.Code)
	}
}

func TestT1_ProjectDraft_And_SecurityInvariants(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	// Extend event deadline into future so draft & edit can be tested
	futureTime := "2030-01-01T00:00:00Z"
	_, err = db.Exec("UPDATE events SET submissions_close_at = ?;", futureTime)
	if err != nil {
		t.Fatalf("update deadline: %v", err)
	}

	// 1. Participant on tm_01 creates a project draft
	reqCreate := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(`{
		"team_id": "tm_01",
		"track_id": "trk_04",
		"title": "Quantum Shield",
		"summary": "AI intrusion prevention",
		"repo_url": "https://github.com/dogfood/shield",
		"action": "draft"
	}`))
	reqCreate.Header.Set("Cookie", "session=prt_2e88")
	reqCreate.Header.Set("Content-Type", "application/json")
	reqCreate.Header.Set("Accept", "application/json")
	rrCreate := httptest.NewRecorder()
	handler.ServeHTTP(rrCreate, reqCreate)

	if rrCreate.Code != http.StatusOK {
		t.Fatalf("create project draft: expected 200, got %d: %s", rrCreate.Code, rrCreate.Body.String())
	}
	var prjRes struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	_ = json.Unmarshal(rrCreate.Body.Bytes(), &prjRes)
	if prjRes.State != "DRAFT" {
		t.Errorf("expected state DRAFT, got %s", prjRes.State)
	}

	// 2. Security Invariant: Unauthenticated user CANNOT view private draft
	reqUnauth := httptest.NewRequest(http.MethodGet, "/api/projects/"+prjRes.ID, nil)
	rrUnauth := httptest.NewRecorder()
	handler.ServeHTTP(rrUnauth, reqUnauth)
	if rrUnauth.Code != http.StatusUnauthorized && rrUnauth.Code != http.StatusForbidden {
		t.Errorf("unauthenticated private draft view: expected 401/403, got %d", rrUnauth.Code)
	}

	// 3. Security Invariant: Participant from other team CANNOT edit or view private draft
	// Login user from tm_02
	reqLogOther := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email=member32_1@example.org"))
	reqLogOther.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrLogOther := httptest.NewRecorder()
	handler.ServeHTTP(rrLogOther, reqLogOther)
	var otherCookie *http.Cookie
	for _, c := range rrLogOther.Result().Cookies() {
		if c.Name == "session" {
			otherCookie = c
			break
		}
	}

	reqOtherEdit := httptest.NewRequest(http.MethodPost, "/api/projects/"+prjRes.ID+"/edit", strings.NewReader(`{"title":"Hacked"}`))
	reqOtherEdit.AddCookie(otherCookie)
	reqOtherEdit.Header.Set("Content-Type", "application/json")
	rrOtherEdit := httptest.NewRecorder()
	handler.ServeHTTP(rrOtherEdit, reqOtherEdit)
	if rrOtherEdit.Code != http.StatusForbidden {
		t.Errorf("cross-team project edit: expected 403 Forbidden, got %d", rrOtherEdit.Code)
	}

	// 4. Team member edits draft successfully
	reqEdit := httptest.NewRequest(http.MethodPost, "/api/projects/"+prjRes.ID+"/edit", strings.NewReader(`{
		"title": "Quantum Shield V2",
		"summary": "Updated AI intrusion defense",
		"repo_url": "https://github.com/dogfood/shield"
	}`))
	reqEdit.Header.Set("Cookie", "session=prt_2e88")
	reqEdit.Header.Set("Content-Type", "application/json")
	reqEdit.Header.Set("Accept", "application/json")
	rrEdit := httptest.NewRecorder()
	handler.ServeHTTP(rrEdit, reqEdit)
	if rrEdit.Code != http.StatusOK {
		t.Fatalf("edit project draft: expected 200, got %d: %s", rrEdit.Code, rrEdit.Body.String())
	}

	// 5. Team member submits project
	reqSub := httptest.NewRequest(http.MethodPost, "/api/projects/"+prjRes.ID+"/submit", nil)
	reqSub.Header.Set("Cookie", "session=prt_2e88")
	reqSub.Header.Set("Accept", "application/json")
	rrSub := httptest.NewRecorder()
	handler.ServeHTTP(rrSub, reqSub)
	if rrSub.Code != http.StatusOK {
		t.Fatalf("submit project: expected 200, got %d: %s", rrSub.Code, rrSub.Body.String())
	}

	// 6. Now that it is SUBMITTED, public can view it
	reqPub := httptest.NewRequest(http.MethodGet, "/api/projects/"+prjRes.ID, nil)
	rrPub := httptest.NewRecorder()
	handler.ServeHTTP(rrPub, reqPub)
	if rrPub.Code != http.StatusOK {
		t.Errorf("public submitted project view: expected 200, got %d", rrPub.Code)
	}

	// 7. Security Invariant: Once closed, new submission or edit rejected with 403
	_, err = db.Exec("UPDATE events SET submissions_open_at = '2020-01-01T00:00:00Z', submissions_close_at = '2020-01-02T00:00:00Z';")
	if err != nil {
		t.Fatalf("update deadline to past failed: %v", err)
	}
	reqLate := httptest.NewRequest(http.MethodPost, "/api/projects/"+prjRes.ID+"/edit", strings.NewReader(`{"title":"Late Edit"}`))
	reqLate.Header.Set("Cookie", "session=prt_2e88")
	reqLate.Header.Set("Content-Type", "application/json")
	rrLate := httptest.NewRecorder()
	handler.ServeHTTP(rrLate, reqLate)
	if rrLate.Code != http.StatusForbidden {
		t.Errorf("post-deadline edit: expected 403 Forbidden, got %d", rrLate.Code)
	}
}

func createTestUserSession(t *testing.T, db *sql.DB, userID, email, role string) string {
	t.Helper()
	nowUTC := "2026-01-01T00:00:00Z"
	expUTC := "2030-01-01T00:00:00Z"
	_, _ = db.Exec("INSERT OR IGNORE INTO users (id, email_normalized, display_name, created_at) VALUES (?, ?, ?, ?);",
		userID, email, "Test "+role, nowUTC)
	var eventID string
	_ = db.QueryRow("SELECT id FROM events ORDER BY created_at ASC LIMIT 1;").Scan(&eventID)
	_, _ = db.Exec("INSERT OR REPLACE INTO event_roles (event_id, user_id, role, granted_at) VALUES (?, ?, ?, ?);",
		eventID, userID, role, nowUTC)
	sessionToken := "token_" + userID
	tokenHash := auth.HashToken(sessionToken)
	sessionID := "sess_" + userID
	_, err := db.Exec("INSERT OR REPLACE INTO sessions (id, user_id, token_hash, created_at, expires_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?);",
		sessionID, userID, tokenHash, nowUTC, expUTC, nowUTC)
	if err != nil {
		t.Fatalf("createTestUserSession: %v", err)
	}
	return sessionToken
}

func TestT1_AdminRole_Authorization(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	handler := server.Handler()

	// Create user with SOLELY 'admin' role (no organizer role)
	adminToken := createTestUserSession(t, db, "usr_pure_admin", "pure_admin@example.org", "admin")

	// 1. Pure Admin can update event windows
	reqEvent := httptest.NewRequest(http.MethodPost, "/api/organizer/event", strings.NewReader(`{"name":"Admin Updated Hackathon"}`))
	reqEvent.Header.Set("Cookie", "session="+adminToken)
	reqEvent.Header.Set("Content-Type", "application/json")
	reqEvent.Header.Set("Accept", "application/json")
	rrEvent := httptest.NewRecorder()
	handler.ServeHTTP(rrEvent, reqEvent)
	if rrEvent.Code != http.StatusOK {
		t.Fatalf("pure admin update event: expected 200, got %d: %s", rrEvent.Code, rrEvent.Body.String())
	}

	// 2. Pure Admin can create track
	reqTrack := httptest.NewRequest(http.MethodPost, "/api/organizer/tracks", strings.NewReader(`{"name":"Robotics"}`))
	reqTrack.Header.Set("Cookie", "session="+adminToken)
	reqTrack.Header.Set("Content-Type", "application/json")
	reqTrack.Header.Set("Accept", "application/json")
	rrTrack := httptest.NewRecorder()
	handler.ServeHTTP(rrTrack, reqTrack)
	if rrTrack.Code != http.StatusCreated {
		t.Fatalf("pure admin create track: expected 201, got %d: %s", rrTrack.Code, rrTrack.Body.String())
	}

	// 3. Pure Admin can create prize
	reqPrize := httptest.NewRequest(http.MethodPost, "/api/organizer/prizes", strings.NewReader(`{"name":"Best Hardware","amount_text":"$5,000"}`))
	reqPrize.Header.Set("Cookie", "session="+adminToken)
	reqPrize.Header.Set("Content-Type", "application/json")
	reqPrize.Header.Set("Accept", "application/json")
	rrPrize := httptest.NewRecorder()
	handler.ServeHTTP(rrPrize, reqPrize)
	if rrPrize.Code != http.StatusCreated {
		t.Fatalf("pure admin create prize: expected 201, got %d: %s", rrPrize.Code, rrPrize.Body.String())
	}

	// 4. Pure Admin can export CSV
	reqCSV := httptest.NewRequest(http.MethodGet, "/api/export.csv", nil)
	reqCSV.Header.Set("Cookie", "session="+adminToken)
	rrCSV := httptest.NewRecorder()
	handler.ServeHTTP(rrCSV, reqCSV)
	if rrCSV.Code != http.StatusOK {
		t.Fatalf("pure admin export csv: expected 200, got %d: %s", rrCSV.Code, rrCSV.Body.String())
	}

	// 5. Participant blocked from administrative actions (403 Forbidden)
	adminEndpoints := []struct {
		method string
		url    string
		body   string
	}{
		{http.MethodPost, "/api/organizer/events", `{"name":"Illegal Event"}`},
		{http.MethodPost, "/api/organizer/event", `{"name":"Illegal Update"}`},
		{http.MethodPost, "/api/organizer/tracks", `{"name":"Illegal Track"}`},
		{http.MethodPost, "/api/organizer/prizes", `{"name":"Illegal Prize"}`},
		{http.MethodGet, "/api/export.csv", ""},
	}
	for _, ep := range adminEndpoints {
		var req *http.Request
		if ep.body != "" {
			req = httptest.NewRequest(ep.method, ep.url, strings.NewReader(ep.body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(ep.method, ep.url, nil)
		}
		req.Header.Set("Cookie", "session=prt_2e88")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("participant on %s %s: expected 403 Forbidden, got %d", ep.method, ep.url, rr.Code)
		}
	}

	// 6. Judge blocked from administrative actions (403 Forbidden)
	for _, ep := range adminEndpoints {
		var req *http.Request
		if ep.body != "" {
			req = httptest.NewRequest(ep.method, ep.url, strings.NewReader(ep.body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(ep.method, ep.url, nil)
		}
		req.Header.Set("Cookie", "session=jdg_a_91bc")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Errorf("judge on %s %s: expected 403 Forbidden, got %d", ep.method, ep.url, rr.Code)
		}
	}
}

func TestT1_EventCreation_And_DateValidation(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	handler := server.Handler()

	adminToken := createTestUserSession(t, db, "usr_event_admin", "event_admin@example.org", "admin")

	// 1. Valid event creation
	reqCreate := httptest.NewRequest(http.MethodPost, "/api/organizer/events", strings.NewReader(`{
		"name": "Spring Hack 2026",
		"registration_opens_at": "2026-03-01T00:00:00Z",
		"registration_closes_at": "2026-03-10T00:00:00Z",
		"submissions_open_at": "2026-03-11T00:00:00Z",
		"submissions_close_at": "2026-03-15T00:00:00Z",
		"judging_opens_at": "2026-03-16T00:00:00Z",
		"judging_closes_at": "2026-03-18T00:00:00Z"
	}`))
	reqCreate.Header.Set("Cookie", "session="+adminToken)
	reqCreate.Header.Set("Content-Type", "application/json")
	reqCreate.Header.Set("Accept", "application/json")
	rrCreate := httptest.NewRecorder()
	handler.ServeHTTP(rrCreate, reqCreate)
	if rrCreate.Code != http.StatusCreated {
		t.Fatalf("create event: expected 201, got %d: %s", rrCreate.Code, rrCreate.Body.String())
	}

	// 2. Invalid date ordering in creation (open > close) -> 400 Bad Request
	reqInvalid := httptest.NewRequest(http.MethodPost, "/api/organizer/events", strings.NewReader(`{
		"name": "Bad Dates Hack",
		"submissions_open_at": "2026-05-10T00:00:00Z",
		"submissions_close_at": "2026-05-01T00:00:00Z"
	}`))
	reqInvalid.Header.Set("Cookie", "session="+adminToken)
	reqInvalid.Header.Set("Content-Type", "application/json")
	rrInvalid := httptest.NewRecorder()
	handler.ServeHTTP(rrInvalid, reqInvalid)
	if rrInvalid.Code != http.StatusBadRequest {
		t.Errorf("invalid date order creation: expected 400 Bad Request, got %d", rrInvalid.Code)
	}

	// 3. Invalid date ordering in update (open > close) -> 400 Bad Request
	reqInvalidUpdate := httptest.NewRequest(http.MethodPost, "/api/organizer/event", strings.NewReader(`{
		"submissions_open_at": "2026-06-15T00:00:00Z",
		"submissions_close_at": "2026-06-01T00:00:00Z"
	}`))
	reqInvalidUpdate.Header.Set("Cookie", "session="+adminToken)
	reqInvalidUpdate.Header.Set("Content-Type", "application/json")
	rrInvalidUpdate := httptest.NewRecorder()
	handler.ServeHTTP(rrInvalidUpdate, reqInvalidUpdate)
	if rrInvalidUpdate.Code != http.StatusBadRequest {
		t.Errorf("invalid date order update: expected 400 Bad Request, got %d", rrInvalidUpdate.Code)
	}
}

func TestT1_TrackAndPrize_Configuration(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	handler := server.Handler()

	adminToken := createTestUserSession(t, db, "usr_cfg_admin", "cfg_admin@example.org", "admin")

	// 1. Admin creates track
	reqTrack := httptest.NewRequest(http.MethodPost, "/api/organizer/tracks", strings.NewReader(`{"name":"Quantum Computing"}`))
	reqTrack.Header.Set("Cookie", "session="+adminToken)
	reqTrack.Header.Set("Content-Type", "application/json")
	reqTrack.Header.Set("Accept", "application/json")
	rrTrack := httptest.NewRecorder()
	handler.ServeHTTP(rrTrack, reqTrack)
	if rrTrack.Code != http.StatusCreated {
		t.Fatalf("create track: expected 201, got %d: %s", rrTrack.Code, rrTrack.Body.String())
	}
	var trackRes struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	_ = json.Unmarshal(rrTrack.Body.Bytes(), &trackRes)

	// 2. Public query for tracks
	reqGetTracks := httptest.NewRequest(http.MethodGet, "/api/tracks", nil)
	rrGetTracks := httptest.NewRecorder()
	handler.ServeHTTP(rrGetTracks, reqGetTracks)
	if rrGetTracks.Code != http.StatusOK {
		t.Fatalf("get tracks: expected 200, got %d", rrGetTracks.Code)
	}
	if !strings.Contains(rrGetTracks.Body.String(), "Quantum Computing") {
		t.Errorf("get tracks response missing Quantum Computing: %s", rrGetTracks.Body.String())
	}

	// 3. Admin creates prize linked to track
	reqPrize := httptest.NewRequest(http.MethodPost, "/api/organizer/prizes", strings.NewReader(fmt.Sprintf(`{
		"name": "Grand Quantum Prize",
		"description": "Best algorithmic advance",
		"amount_text": "$25,000",
		"track_id": "%s"
	}`, trackRes.ID)))
	reqPrize.Header.Set("Cookie", "session="+adminToken)
	reqPrize.Header.Set("Content-Type", "application/json")
	reqPrize.Header.Set("Accept", "application/json")
	rrPrize := httptest.NewRecorder()
	handler.ServeHTTP(rrPrize, reqPrize)
	if rrPrize.Code != http.StatusCreated {
		t.Fatalf("create prize: expected 201, got %d: %s", rrPrize.Code, rrPrize.Body.String())
	}

	// 4. Public query for prizes
	reqGetPrizes := httptest.NewRequest(http.MethodGet, "/api/prizes", nil)
	rrGetPrizes := httptest.NewRecorder()
	handler.ServeHTTP(rrGetPrizes, reqGetPrizes)
	if rrGetPrizes.Code != http.StatusOK {
		t.Fatalf("get prizes: expected 200, got %d", rrGetPrizes.Code)
	}
	if !strings.Contains(rrGetPrizes.Body.String(), "Grand Quantum Prize") || !strings.Contains(rrGetPrizes.Body.String(), "$25,000") {
		t.Errorf("get prizes response missing prize info: %s", rrGetPrizes.Body.String())
	}
}

func TestT1_FullSubmissionLifecycle_HappyPath(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	handler := server.Handler()

	// Ensure submissions are open
	_, _ = db.Exec("UPDATE events SET submissions_open_at = '2020-01-01T00:00:00Z', submissions_close_at = '2030-01-01T00:00:00Z';")

	// 1. Participant creates draft
	reqDraft := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(`{
		"team_id": "tm_01",
		"track_id": "trk_01",
		"title": "Solaris Kernel",
		"summary": "Distributed kernel operating system",
		"repo_url": "https://github.com/solaris/kernel",
		"demo_url": "https://solaris.dev",
		"action": "draft"
	}`))
	reqDraft.Header.Set("Cookie", "session=prt_2e88")
	reqDraft.Header.Set("Content-Type", "application/json")
	reqDraft.Header.Set("Accept", "application/json")
	rrDraft := httptest.NewRecorder()
	handler.ServeHTTP(rrDraft, reqDraft)
	if rrDraft.Code != http.StatusOK {
		t.Fatalf("create draft: expected 200, got %d: %s", rrDraft.Code, rrDraft.Body.String())
	}
	var draftRes struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	_ = json.Unmarshal(rrDraft.Body.Bytes(), &draftRes)
	if draftRes.State != "DRAFT" {
		t.Errorf("expected DRAFT, got %s", draftRes.State)
	}

	// 2. Draft is NOT in public gallery
	reqGal1 := httptest.NewRequest(http.MethodGet, "/projects", nil)
	rrGal1 := httptest.NewRecorder()
	handler.ServeHTTP(rrGal1, reqGal1)
	if strings.Contains(rrGal1.Body.String(), "Solaris Kernel") {
		t.Errorf("draft project should not appear in public gallery before submission")
	}

	// 3. Edit draft to update title and details
	reqEdit := httptest.NewRequest(http.MethodPost, "/api/projects/"+draftRes.ID+"/edit", strings.NewReader(`{
		"title": "Solaris Microkernel Pro",
		"summary": "Zero-trust distributed microkernel",
		"repo_url": "https://github.com/solaris/microkernel",
		"demo_url": "https://pro.solaris.dev"
	}`))
	reqEdit.Header.Set("Cookie", "session=prt_2e88")
	reqEdit.Header.Set("Content-Type", "application/json")
	reqEdit.Header.Set("Accept", "application/json")
	rrEdit := httptest.NewRecorder()
	handler.ServeHTTP(rrEdit, reqEdit)
	if rrEdit.Code != http.StatusOK {
		t.Fatalf("edit draft: expected 200, got %d: %s", rrEdit.Code, rrEdit.Body.String())
	}

	// 4. Submit draft before deadline
	reqSubmit := httptest.NewRequest(http.MethodPost, "/api/projects/"+draftRes.ID+"/submit", nil)
	reqSubmit.Header.Set("Cookie", "session=prt_2e88")
	reqSubmit.Header.Set("Accept", "application/json")
	rrSubmit := httptest.NewRecorder()
	handler.ServeHTTP(rrSubmit, reqSubmit)
	if rrSubmit.Code != http.StatusOK {
		t.Fatalf("submit project: expected 200, got %d: %s", rrSubmit.Code, rrSubmit.Body.String())
	}

	// 5. Unauthenticated visitor sees submitted project in gallery
	reqGal2 := httptest.NewRequest(http.MethodGet, "/projects", nil)
	rrGal2 := httptest.NewRecorder()
	handler.ServeHTTP(rrGal2, reqGal2)
	if rrGal2.Code != http.StatusOK {
		t.Fatalf("public gallery: expected 200, got %d", rrGal2.Code)
	}
	if !strings.Contains(rrGal2.Body.String(), "Solaris Microkernel Pro") {
		t.Errorf("public gallery missing submitted project 'Solaris Microkernel Pro'")
	}
	// Verify official fixture project still visible
	if !strings.Contains(rrGal2.Body.String(), "Glass Signal") {
		t.Errorf("public gallery missing official fixture 'Glass Signal'")
	}

	// 6. Unauthenticated visitor can view project detail API
	reqAPI := httptest.NewRequest(http.MethodGet, "/api/projects/"+draftRes.ID, nil)
	rrAPI := httptest.NewRecorder()
	handler.ServeHTTP(rrAPI, reqAPI)
	if rrAPI.Code != http.StatusOK {
		t.Fatalf("public get project API: expected 200, got %d: %s", rrAPI.Code, rrAPI.Body.String())
	}
	var pubRes ProjectView
	_ = json.Unmarshal(rrAPI.Body.Bytes(), &pubRes)
	if pubRes.Title != "Solaris Microkernel Pro" || pubRes.State != "SUBMITTED" || pubRes.SubmittedAt == "" {
		t.Errorf("unexpected project API response: %+v", pubRes)
	}
}
