package httpapp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"dogfood/internal/auth"
)

// TestHumanAcceptance_FlowA_Auth tests Demo Persona sign in, session revocation, and acceptance token isolation.
func TestHumanAcceptance_FlowA_Auth(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	personas := []struct {
		key      string
		expected string
	}{
		{"organizer", "Organizer"},
		{"participant_a", "Alex Chen"},
		{"participant_b", "Blair Taylor"},
		{"judge_a", "Tomas Varga"},
		{"judge_b", "Wei Lindqvist"},
	}

	for _, p := range personas {
		t.Run(p.key, func(t *testing.T) {
			// 1. Log in via demo persona
			body := url.Values{"demo_user": {p.key}}.Encode()
			reqLogin := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
			reqLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rrLogin := httptest.NewRecorder()
			handler.ServeHTTP(rrLogin, reqLogin)

			if rrLogin.Code != http.StatusSeeOther {
				t.Fatalf("login %s: expected 303, got %d", p.key, rrLogin.Code)
			}
			cookies := rrLogin.Result().Cookies()
			var sessionCookie *http.Cookie
			for _, c := range cookies {
				if c.Name == "session" {
					sessionCookie = c
					break
				}
			}
			if sessionCookie == nil || sessionCookie.Value == "" {
				t.Fatalf("login %s: session cookie missing", p.key)
			}

			// Invariant: human browser session MUST NOT be one of the fixed official evaluator tokens
			for prot := range auth.ProtectedAcceptanceTokens {
				if sessionCookie.Value == prot {
					t.Fatalf("login %s: session cookie matched protected token %s", p.key, prot)
				}
			}

			// 2. Access dashboard with human session
			reqDash := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
			reqDash.AddCookie(sessionCookie)
			rrDash := httptest.NewRecorder()
			handler.ServeHTTP(rrDash, reqDash)
			if rrDash.Code != http.StatusOK {
				t.Fatalf("dashboard %s: expected 200, got %d", p.key, rrDash.Code)
			}
			if !strings.Contains(rrDash.Body.String(), p.expected) {
				t.Errorf("dashboard %s: missing expected persona name %q", p.key, p.expected)
			}

			// 3. Log out - revoking the human session
			reqLogout := httptest.NewRequest(http.MethodGet, "/logout", nil)
			reqLogout.AddCookie(sessionCookie)
			rrLogout := httptest.NewRecorder()
			handler.ServeHTTP(rrLogout, reqLogout)
			if rrLogout.Code != http.StatusSeeOther {
				t.Fatalf("logout %s: expected 303, got %d", p.key, rrLogout.Code)
			}

			// 4. Invariant: Accessing dashboard with revoked session cookie fails
			reqDashRevoked := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
			reqDashRevoked.AddCookie(sessionCookie)
			rrDashRevoked := httptest.NewRecorder()
			handler.ServeHTTP(rrDashRevoked, reqDashRevoked)
			if rrDashRevoked.Code != http.StatusSeeOther {
				t.Errorf("revoked dashboard %s: expected 303 redirect, got %d", p.key, rrDashRevoked.Code)
			}

			// 5. Re-login succeeds with brand new session token
			reqReLogin := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
			reqReLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rrReLogin := httptest.NewRecorder()
			handler.ServeHTTP(rrReLogin, reqReLogin)
			if rrReLogin.Code != http.StatusSeeOther {
				t.Fatalf("re-login %s: expected 303, got %d", p.key, rrReLogin.Code)
			}
			var newCookie *http.Cookie
			for _, c := range rrReLogin.Result().Cookies() {
				if c.Name == "session" {
					newCookie = c
					break
				}
			}
			if newCookie == nil || newCookie.Value == "" || newCookie.Value == sessionCookie.Value {
				t.Fatalf("re-login %s: expected fresh session cookie", p.key)
			}
		})
	}

	// 6. Invariant: Official evaluator tokens remain valid and cannot be revoked by logout
	for prot := range auth.ProtectedAcceptanceTokens {
		reqProt := httptest.NewRequest(http.MethodGet, "/logout", nil)
		reqProt.Header.Set("Cookie", "session="+prot)
		rrProt := httptest.NewRecorder()
		handler.ServeHTTP(rrProt, reqProt)

		// Verify session is still valid in database
		var count int
		_ = db.QueryRow("SELECT COUNT(*) FROM sessions WHERE token_hash = ? AND revoked_at IS NULL;", auth.HashToken(prot)).Scan(&count)
		if count != 1 {
			t.Errorf("protected acceptance token %s was improperly revoked", prot)
		}
	}
}

// TestHumanAcceptance_FlowB_Team tests complete browser team lifecycle, invites, and capacity guards.
func TestHumanAcceptance_FlowB_Team(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	// 1. Participant A logs in
	loginA := url.Values{"demo_user": {"participant_a"}}.Encode()
	reqLogA := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(loginA))
	reqLogA.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrLogA := httptest.NewRecorder()
	handler.ServeHTTP(rrLogA, reqLogA)
	cookieA := rrLogA.Result().Cookies()[0]

	// 2. Participant A creates team via browser form
	createTeamForm := url.Values{"name": {"Apex Innovators"}}.Encode()
	reqTeam := httptest.NewRequest(http.MethodPost, "/api/teams", strings.NewReader(createTeamForm))
	reqTeam.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqTeam.Header.Set("Accept", "text/html,application/xhtml+xml")
	reqTeam.AddCookie(cookieA)
	rrTeam := httptest.NewRecorder()
	handler.ServeHTTP(rrTeam, reqTeam)
	if rrTeam.Code != http.StatusSeeOther {
		t.Fatalf("create team: expected 303, got %d", rrTeam.Code)
	}

	var teamID string
	err = db.QueryRow("SELECT id FROM teams WHERE name = 'Apex Innovators';").Scan(&teamID)
	if err != nil {
		t.Fatalf("team not created in db: %v", err)
	}

	// 3. Participant A generates team invite link via browser form
	reqInv := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/teams/%s/invites", teamID), nil)
	reqInv.Header.Set("Accept", "text/html,application/xhtml+xml")
	reqInv.AddCookie(cookieA)
	rrInv := httptest.NewRecorder()
	handler.ServeHTTP(rrInv, reqInv)
	if rrInv.Code != http.StatusSeeOther {
		t.Fatalf("create invite: expected 303, got %d", rrInv.Code)
	}

	// Extract invite_token from redirect location
	loc := rrInv.Header().Get("Location")
	locURL, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse redirect location: %v", err)
	}
	inviteToken := locURL.Query().Get("invite_token")
	if inviteToken == "" {
		t.Fatalf("redirect URL missing invite_token parameter: %s", loc)
	}

	// 4. Participant A logs out
	reqLogoutA := httptest.NewRequest(http.MethodGet, "/logout", nil)
	reqLogoutA.AddCookie(cookieA)
	rrLogoutA := httptest.NewRecorder()
	handler.ServeHTTP(rrLogoutA, reqLogoutA)

	// 5. Unauthenticated user visits invite URL
	reqUnauthJoin := httptest.NewRequest(http.MethodGet, "/teams/join?token="+inviteToken, nil)
	reqUnauthJoin.Header.Set("Accept", "text/html")
	rrUnauthJoin := httptest.NewRecorder()
	handler.ServeHTTP(rrUnauthJoin, reqUnauthJoin)
	if rrUnauthJoin.Code != http.StatusOK {
		t.Fatalf("unauth join view: expected 200, got %d", rrUnauthJoin.Code)
	}
	unauthBody := rrUnauthJoin.Body.String()
	if !strings.Contains(unauthBody, "Apex Innovators") {
		t.Errorf("unauth join page missing team name")
	}
	if !strings.Contains(unauthBody, "Sign In to Accept Invitation") {
		t.Errorf("unauth join page missing sign in prompt")
	}

	// 6. Participant B logs in with return_to
	loginB := url.Values{
		"demo_user": {"participant_b"},
		"return_to": {"/teams/join?token=" + inviteToken},
	}.Encode()
	reqLogB := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(loginB))
	reqLogB.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrLogB := httptest.NewRecorder()
	handler.ServeHTTP(rrLogB, reqLogB)
	if rrLogB.Code != http.StatusSeeOther {
		t.Fatalf("login B: expected 303, got %d", rrLogB.Code)
	}
	if rrLogB.Header().Get("Location") != "/teams/join?token="+inviteToken {
		t.Errorf("login B did not redirect to return_to: got %s", rrLogB.Header().Get("Location"))
	}
	cookieB := rrLogB.Result().Cookies()[0]

	// 7. Participant B visits dedicated confirmation page
	reqAuthJoin := httptest.NewRequest(http.MethodGet, "/teams/join?token="+inviteToken, nil)
	reqAuthJoin.Header.Set("Accept", "text/html")
	reqAuthJoin.AddCookie(cookieB)
	rrAuthJoin := httptest.NewRecorder()
	handler.ServeHTTP(rrAuthJoin, reqAuthJoin)
	if rrAuthJoin.Code != http.StatusOK {
		t.Fatalf("auth join view: expected 200, got %d", rrAuthJoin.Code)
	}
	authJoinBody := rrAuthJoin.Body.String()
	if !strings.Contains(authJoinBody, "Accept Invitation &amp; Join Team") && !strings.Contains(authJoinBody, "Accept Invitation & Join Team") {
		t.Errorf("join page missing accept button")
	}
	if !strings.Contains(authJoinBody, "1 of 4 members") {
		t.Errorf("join page missing member count capacity info")
	}

	// 8. Participant B clicks "Accept Invitation & Join Team"
	joinForm := url.Values{"token": {inviteToken}}.Encode()
	reqJoinSubmit := httptest.NewRequest(http.MethodPost, "/teams/join", strings.NewReader(joinForm))
	reqJoinSubmit.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqJoinSubmit.Header.Set("Accept", "text/html,application/xhtml+xml")
	reqJoinSubmit.AddCookie(cookieB)
	rrJoinSubmit := httptest.NewRecorder()
	handler.ServeHTTP(rrJoinSubmit, reqJoinSubmit)
	if rrJoinSubmit.Code != http.StatusSeeOther {
		t.Fatalf("join submit: expected 303, got %d", rrJoinSubmit.Code)
	}

	// 9. Participant B dashboard shows both members
	reqDashB := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	reqDashB.AddCookie(cookieB)
	rrDashB := httptest.NewRecorder()
	handler.ServeHTTP(rrDashB, reqDashB)
	dashBBody := rrDashB.Body.String()
	if !strings.Contains(dashBBody, "Alex Chen (Participant A)") || !strings.Contains(dashBBody, "Blair Taylor (Participant B)") {
		t.Errorf("dashboard B does not display both team members")
	}

	// 10. Replay protection: Re-opening or re-submitting the same invite token produces human-readable error
	reqReplayGet := httptest.NewRequest(http.MethodGet, "/teams/join?token="+inviteToken, nil)
	reqReplayGet.Header.Set("Accept", "text/html")
	rrReplayGet := httptest.NewRecorder()
	handler.ServeHTTP(rrReplayGet, reqReplayGet)
	if !strings.Contains(rrReplayGet.Body.String(), "This invitation has already been used") {
		t.Errorf("replay GET did not show human-readable already used error: %s", rrReplayGet.Body.String())
	}

	reqReplayPost := httptest.NewRequest(http.MethodPost, "/teams/join", strings.NewReader(joinForm))
	reqReplayPost.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqReplayPost.Header.Set("Accept", "text/html")
	reqReplayPost.AddCookie(cookieB)
	rrReplayPost := httptest.NewRecorder()
	handler.ServeHTTP(rrReplayPost, reqReplayPost)
	if rrReplayPost.Code != http.StatusSeeOther || !strings.Contains(rrReplayPost.Header().Get("Location"), "already+been+used") {
		t.Errorf("replay POST did not redirect with already used error: %s", rrReplayPost.Header().Get("Location"))
	}

	// 11. Add members 3 and 4
	nowUTC := "2026-02-01T12:00:00Z"
	var u3, u4 string
	err = db.QueryRow("SELECT id FROM users WHERE id NOT IN (SELECT user_id FROM team_memberships WHERE team_id = ?) LIMIT 1 OFFSET 0;", teamID).Scan(&u3)
	if err != nil {
		t.Fatalf("query user 3: %v", err)
	}
	err = db.QueryRow("SELECT id FROM users WHERE id NOT IN (SELECT user_id FROM team_memberships WHERE team_id = ?) LIMIT 1 OFFSET 1;", teamID).Scan(&u4)
	if err != nil {
		t.Fatalf("query user 4: %v", err)
	}
	_, err = db.Exec("INSERT INTO team_memberships (id, event_id, team_id, user_id, membership_role, joined_at) VALUES ('mem_3', 'evt_01', ?, ?, 'member', ?);", teamID, u3, nowUTC)
	if err != nil {
		t.Fatalf("insert member 3: %v", err)
	}
	_, err = db.Exec("INSERT INTO team_memberships (id, event_id, team_id, user_id, membership_role, joined_at) VALUES ('mem_4', 'evt_01', ?, ?, 'member', ?);", teamID, u4, nowUTC)
	if err != nil {
		t.Fatalf("insert member 4: %v", err)
	}

	// Verify team has 4 members
	var count int
	_ = db.QueryRow("SELECT COUNT(*) FROM team_memberships WHERE team_id = ? AND left_at IS NULL;", teamID).Scan(&count)
	if count != 4 {
		t.Fatalf("expected 4 members, got %d", count)
	}

	// Attempting to generate a new invite now fails cleanly with human-readable error
	reqInvFull := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/teams/%s/invites", teamID), nil)
	reqInvFull.Header.Set("Accept", "text/html,application/xhtml+xml")
	reqInvFull.AddCookie(cookieB)
	rrInvFull := httptest.NewRecorder()
	handler.ServeHTTP(rrInvFull, reqInvFull)
	if rrInvFull.Code != http.StatusSeeOther || !strings.Contains(rrInvFull.Header().Get("Location"), "maximum+capacity") {
		t.Errorf("full team invite creation did not redirect with capacity error: %s", rrInvFull.Header().Get("Location"))
	}
}

// TestHumanAcceptance_FlowC_Project tests drafting, editing, submitting, and public gallery visibility.
func TestHumanAcceptance_FlowC_Project(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	// Ensure submissions window is open
	_, _ = db.Exec("UPDATE events SET submissions_open_at = '2026-01-01T00:00:00Z', submissions_close_at = '2030-01-01T00:00:00Z';")

	// 1. Participant A logs in
	loginA := url.Values{"demo_user": {"participant_a"}}.Encode()
	reqLogA := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(loginA))
	reqLogA.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrLogA := httptest.NewRecorder()
	handler.ServeHTTP(rrLogA, reqLogA)
	cookieA := rrLogA.Result().Cookies()[0]

	// 2. Create team
	createTeamForm := url.Values{"name": {"Project Builders"}}.Encode()
	reqTeam := httptest.NewRequest(http.MethodPost, "/api/teams", strings.NewReader(createTeamForm))
	reqTeam.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqTeam.Header.Set("Accept", "text/html")
	reqTeam.AddCookie(cookieA)
	rrTeam := httptest.NewRecorder()
	handler.ServeHTTP(rrTeam, reqTeam)

	var teamID string
	_ = db.QueryRow("SELECT id FROM teams WHERE name = 'Project Builders';").Scan(&teamID)

	// 3. Create Draft Project from Dashboard
	draftForm := url.Values{
		"team_id":  {teamID},
		"track_id": {"trk_01"},
		"title":    {"Project Nova"},
		"summary":  {"Autonomous exploration bot."},
		"repo_url": {"https://github.com/example/nova"},
		"demo_url": {"https://nova.example.org"},
		"action":   {"draft"},
	}.Encode()
	reqDraft := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(draftForm))
	reqDraft.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqDraft.Header.Set("Accept", "text/html")
	reqDraft.AddCookie(cookieA)
	rrDraft := httptest.NewRecorder()
	handler.ServeHTTP(rrDraft, reqDraft)
	if rrDraft.Code != http.StatusSeeOther {
		t.Fatalf("create draft: expected 303, got %d", rrDraft.Code)
	}

	var projectID string
	_ = db.QueryRow("SELECT id FROM projects WHERE team_id = ?;", teamID).Scan(&projectID)

	// 4. Verify draft appears in Participant Dashboard with DRAFT badge
	reqDash := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	reqDash.AddCookie(cookieA)
	rrDash := httptest.NewRecorder()
	handler.ServeHTTP(rrDash, reqDash)
	dashBody := rrDash.Body.String()
	if !strings.Contains(dashBody, "Project Nova") {
		t.Errorf("dashboard does not display draft project title")
	}
	if !strings.Contains(dashBody, "DRAFT") {
		t.Errorf("dashboard does not display DRAFT badge")
	}

	// 5. Invariant: Draft MUST NOT appear in the public gallery
	reqGal := httptest.NewRequest(http.MethodGet, "/projects", nil)
	rrGal := httptest.NewRecorder()
	handler.ServeHTTP(rrGal, reqGal)
	if strings.Contains(rrGal.Body.String(), "Project Nova") {
		t.Errorf("public gallery improperly displays private draft project")
	}

	// 6. Edit Draft
	editForm := url.Values{
		"title":    {"Project Nova Prime"},
		"summary":  {"Upgraded autonomous exploration bot."},
		"repo_url": {"https://github.com/example/nova-prime"},
		"demo_url": {"https://nova-prime.example.org"},
	}.Encode()
	reqEdit := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/projects/%s/edit", projectID), strings.NewReader(editForm))
	reqEdit.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqEdit.Header.Set("Accept", "text/html")
	reqEdit.AddCookie(cookieA)
	rrEdit := httptest.NewRecorder()
	handler.ServeHTTP(rrEdit, reqEdit)
	if rrEdit.Code != http.StatusSeeOther {
		t.Fatalf("edit project: expected 303, got %d", rrEdit.Code)
	}

	// 7. Submit Project
	reqSubmit := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/projects/%s/submit", projectID), nil)
	reqSubmit.Header.Set("Accept", "text/html")
	reqSubmit.AddCookie(cookieA)
	rrSubmit := httptest.NewRecorder()
	handler.ServeHTTP(rrSubmit, reqSubmit)
	if rrSubmit.Code != http.StatusSeeOther {
		t.Fatalf("submit project: expected 303, got %d", rrSubmit.Code)
	}

	// 8. Project is now SUBMITTED and visible in public gallery
	reqGalAfter := httptest.NewRequest(http.MethodGet, "/projects", nil)
	rrGalAfter := httptest.NewRecorder()
	handler.ServeHTTP(rrGalAfter, reqGalAfter)
	galBody := rrGalAfter.Body.String()
	if !strings.Contains(galBody, "Project Nova Prime") {
		t.Errorf("submitted project does not appear in public gallery")
	}
	if !strings.Contains(galBody, "Upgraded autonomous exploration bot") {
		t.Errorf("submitted project summary missing from gallery")
	}
}

// TestHumanAcceptance_FlowD_Organizer tests the organizer workspace and role guards.
func TestHumanAcceptance_FlowD_Organizer(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	// 1. Organizer logs in
	loginOrg := url.Values{"demo_user": {"organizer"}}.Encode()
	reqLogOrg := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(loginOrg))
	reqLogOrg.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrLogOrg := httptest.NewRecorder()
	handler.ServeHTTP(rrLogOrg, reqLogOrg)
	cookieOrg := rrLogOrg.Result().Cookies()[0]

	// 2. Organizer Dashboard renders all tabs/sections
	reqDash := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	reqDash.AddCookie(cookieOrg)
	rrDash := httptest.NewRecorder()
	handler.ServeHTTP(rrDash, reqDash)
	if rrDash.Code != http.StatusOK {
		t.Fatalf("organizer dashboard: expected 200, got %d", rrDash.Code)
	}
	dashBody := rrDash.Body.String()
	for _, sec := range []string{"Organizer Workspace", "Event Lifecycle Windows", "Tracks", "Prizes", "All Teams", "All Projects"} {
		if !strings.Contains(dashBody, sec) {
			t.Errorf("organizer dashboard missing section: %s", sec)
		}
	}

	// 3. Organizer creates a new Track via browser form
	trackForm := url.Values{"name": {"Robotics & Automation"}}.Encode()
	reqTrack := httptest.NewRequest(http.MethodPost, "/api/organizer/tracks", strings.NewReader(trackForm))
	reqTrack.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqTrack.Header.Set("Accept", "text/html")
	reqTrack.AddCookie(cookieOrg)
	rrTrack := httptest.NewRecorder()
	handler.ServeHTTP(rrTrack, reqTrack)
	if rrTrack.Code != http.StatusSeeOther {
		t.Fatalf("create track: expected 303, got %d", rrTrack.Code)
	}

	// Verify track appears in dashboard
	reqDash2 := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	reqDash2.AddCookie(cookieOrg)
	rrDash2 := httptest.NewRecorder()
	handler.ServeHTTP(rrDash2, reqDash2)
	if !strings.Contains(rrDash2.Body.String(), "Robotics &amp; Automation") && !strings.Contains(rrDash2.Body.String(), "Robotics & Automation") {
		t.Errorf("new track not displayed in organizer dashboard")
	}

	// 4. Organizer creates a new Prize via browser form
	prizeForm := url.Values{
		"name":        {"Grand Innovation Prize"},
		"amount_text": {"$10,000 USD"},
		"description": {"Most impressive technical execution"},
	}.Encode()
	reqPrize := httptest.NewRequest(http.MethodPost, "/api/organizer/prizes", strings.NewReader(prizeForm))
	reqPrize.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqPrize.Header.Set("Accept", "text/html")
	reqPrize.AddCookie(cookieOrg)
	rrPrize := httptest.NewRecorder()
	handler.ServeHTTP(rrPrize, reqPrize)
	if rrPrize.Code != http.StatusSeeOther {
		t.Fatalf("create prize: expected 303, got %d", rrPrize.Code)
	}

	// 5. Invariant: Participant attempting organizer actions receives 403 Forbidden
	loginPart := url.Values{"demo_user": {"participant_a"}}.Encode()
	reqLogPart := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(loginPart))
	reqLogPart.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrLogPart := httptest.NewRecorder()
	handler.ServeHTTP(rrLogPart, reqLogPart)
	cookiePart := rrLogPart.Result().Cookies()[0]

	reqForbiddenTrack := httptest.NewRequest(http.MethodPost, "/api/organizer/tracks", strings.NewReader(trackForm))
	reqForbiddenTrack.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqForbiddenTrack.AddCookie(cookiePart)
	rrForbiddenTrack := httptest.NewRecorder()
	handler.ServeHTTP(rrForbiddenTrack, reqForbiddenTrack)
	if rrForbiddenTrack.Code != http.StatusForbidden {
		t.Errorf("participant creating track: expected 403, got %d", rrForbiddenTrack.Code)
	}

	reqForbiddenExport := httptest.NewRequest(http.MethodGet, "/api/export.csv", nil)
	reqForbiddenExport.AddCookie(cookiePart)
	rrForbiddenExport := httptest.NewRecorder()
	handler.ServeHTTP(rrForbiddenExport, reqForbiddenExport)
	if rrForbiddenExport.Code != http.StatusForbidden {
		t.Errorf("participant exporting CSV: expected 403, got %d", rrForbiddenExport.Code)
	}
}

// TestHumanAcceptance_FlowE_Judge tests the Judge role surface and peer isolation.
func TestHumanAcceptance_FlowE_Judge(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	// 1. Judge A logs in
	loginJudge := url.Values{"demo_user": {"judge_a"}}.Encode()
	reqLogJ := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(loginJudge))
	reqLogJ.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrLogJ := httptest.NewRecorder()
	handler.ServeHTTP(rrLogJ, reqLogJ)
	cookieJ := rrLogJ.Result().Cookies()[0]

	// 2. Judge Dashboard displays Judge Workspace with identity and Tier 2 notice
	reqDash := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	reqDash.AddCookie(cookieJ)
	rrDash := httptest.NewRecorder()
	handler.ServeHTTP(rrDash, reqDash)
	if rrDash.Code != http.StatusOK {
		t.Fatalf("judge dashboard: expected 200, got %d", rrDash.Code)
	}
	body := rrDash.Body.String()
	if !strings.Contains(body, "Judge Workspace") {
		t.Errorf("judge dashboard missing Judge Workspace section")
	}
	if !strings.Contains(body, "Tomas Varga") {
		t.Errorf("judge dashboard missing judge name")
	}
	if !strings.Contains(body, "Tier 2") {
		t.Errorf("judge dashboard missing Tier 2 rubrics explanation")
	}

	// 3. Invariant: Judge cannot mutate organizer configuration
	reqTrack := httptest.NewRequest(http.MethodPost, "/api/organizer/tracks", strings.NewReader("name=UnauthorizedTrack"))
	reqTrack.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqTrack.AddCookie(cookieJ)
	rrTrack := httptest.NewRecorder()
	handler.ServeHTTP(rrTrack, reqTrack)
	if rrTrack.Code != http.StatusForbidden {
		t.Errorf("judge creating track: expected 403, got %d", rrTrack.Code)
	}

	// 4. Invariant: Judge cannot access CSV export
	reqExport := httptest.NewRequest(http.MethodGet, "/api/export.csv", nil)
	reqExport.AddCookie(cookieJ)
	rrExport := httptest.NewRecorder()
	handler.ServeHTTP(rrExport, reqExport)
	if rrExport.Code != http.StatusForbidden {
		t.Errorf("judge exporting CSV: expected 403, got %d", rrExport.Code)
	}

	// 5. Judge score route allows judge query but preserves isolation
	// 6. Invariant: Judge dashboard contains NO participant or organizer controls
	for _, forbidden := range []string{
		"Participant Workspace",
		"id=\"participant-workspace\"",
		"My Team",
		"Create Team",
		"Generate Invite Link",
		"My Project",
		"Submit Project",
		"Organizer Workspace",
		"id=\"organizer-workspace\"",
		"Event Lifecycle Windows",
		"+ Add Track",
		"+ Add Prize",
		"Download Results CSV",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("judge dashboard unexpectedly contained forbidden element: %s", forbidden)
		}
	}
}

// TestHumanAcceptance_FlowF_RoleUI_Isolation proves strict role-to-UI isolation across all personas.
func TestHumanAcceptance_FlowF_RoleUI_Isolation(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	// Helper to login and get dashboard HTML body
	getDashboardBody := func(t *testing.T, demoUser string) string {
		t.Helper()
		body := url.Values{"demo_user": {demoUser}}.Encode()
		reqLogin := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
		reqLogin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rrLogin := httptest.NewRecorder()
		handler.ServeHTTP(rrLogin, reqLogin)
		if rrLogin.Code != http.StatusSeeOther {
			t.Fatalf("login %s failed: status %d", demoUser, rrLogin.Code)
		}
		cookie := rrLogin.Result().Cookies()[0]

		reqDash := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
		reqDash.AddCookie(cookie)
		rrDash := httptest.NewRecorder()
		handler.ServeHTTP(rrDash, reqDash)
		if rrDash.Code != http.StatusOK {
			t.Fatalf("dashboard %s failed: status %d", demoUser, rrDash.Code)
		}
		return rrDash.Body.String()
	}

	// 1. Participant A (Participant role only)
	t.Run("Participant_A_Isolation", func(t *testing.T) {
		html := getDashboardBody(t, "participant_a")

		// Positive assertions
		for _, required := range []string{"Participant Workspace", "id=\"participant-workspace\"", "My Team", "My Project", "Gallery", "Sign Out"} {
			if !strings.Contains(html, required) {
				t.Errorf("Participant A missing required element: %s", required)
			}
		}

		// Negative assertions
		for _, forbidden := range []string{
			"Organizer Workspace",
			"id=\"organizer-workspace\"",
			"Event Lifecycle Windows",
			"+ Add Track",
			"+ Add Prize",
			"All Teams (",
			"All Projects (",
			"Download Results CSV",
			"Judge Workspace",
			"id=\"judge-workspace\"",
		} {
			if strings.Contains(html, forbidden) {
				t.Errorf("Participant A dashboard leaked forbidden element: %s", forbidden)
			}
		}
	})

	// 2. Participant B (Participant role only, no team initially)
	t.Run("Participant_B_Isolation", func(t *testing.T) {
		html := getDashboardBody(t, "participant_b")

		// Positive assertions
		for _, required := range []string{"Participant Workspace", "id=\"participant-workspace\"", "My Team", "Create Team", "My Project", "Gallery", "Sign Out"} {
			if !strings.Contains(html, required) {
				t.Errorf("Participant B missing required element: %s", required)
			}
		}

		// Negative assertions
		for _, forbidden := range []string{
			"Organizer Workspace",
			"id=\"organizer-workspace\"",
			"Event Lifecycle Windows",
			"+ Add Track",
			"+ Add Prize",
			"All Teams (",
			"All Projects (",
			"Download Results CSV",
			"Judge Workspace",
			"id=\"judge-workspace\"",
		} {
			if strings.Contains(html, forbidden) {
				t.Errorf("Participant B dashboard leaked forbidden element: %s", forbidden)
			}
		}
	})

	// 3. Organizer (Organizer & Admin roles, NOT participant, NOT judge)
	t.Run("Organizer_Isolation", func(t *testing.T) {
		html := getDashboardBody(t, "organizer")

		// Positive assertions
		for _, required := range []string{
			"Organizer Workspace",
			"id=\"organizer-workspace\"",
			"Event Lifecycle Windows",
			"Tracks (",
			"+ Add Track",
			"Prizes (",
			"+ Add Prize",
			"All Teams (",
			"All Projects (",
			"Download Results CSV",
			"Gallery",
			"Sign Out",
		} {
			if !strings.Contains(html, required) {
				t.Errorf("Organizer missing required element: %s", required)
			}
		}

		// Negative assertions: MUST NOT see participant workspace or judge workspace
		for _, forbidden := range []string{
			"Participant Workspace",
			"id=\"participant-workspace\"",
			"My Team",
			"Create Team",
			"+ Generate Invite Link",
			"My Project",
			"Create Project",
			"Submit Project",
			"Judge Workspace",
			"id=\"judge-workspace\"",
		} {
			if strings.Contains(html, forbidden) {
				t.Errorf("Organizer dashboard leaked forbidden element: %s", forbidden)
			}
		}
	})

	// 4. Judge A and Judge B (Judge role only)
	for _, judgeKey := range []string{"judge_a", "judge_b"} {
		t.Run("Judge_"+judgeKey+"_Isolation", func(t *testing.T) {
			html := getDashboardBody(t, judgeKey)

			// Positive assertions
			for _, required := range []string{
				"Judge Workspace",
				"id=\"judge-workspace\"",
				"Evaluation &amp; Assignments",
				"Tier 2",
				"Gallery",
				"Sign Out",
			} {
				if !strings.Contains(html, required) {
					t.Errorf("Judge %s missing required element: %s", judgeKey, required)
				}
			}

			// Negative assertions: MUST NOT see participant workspace or organizer workspace
			for _, forbidden := range []string{
				"Participant Workspace",
				"id=\"participant-workspace\"",
				"My Team",
				"Create Team",
				"+ Generate Invite Link",
				"My Project",
				"Create Project",
				"Submit Project",
				"Organizer Workspace",
				"id=\"organizer-workspace\"",
				"Event Lifecycle Windows",
				"+ Add Track",
				"+ Add Prize",
				"All Teams (",
				"All Projects (",
				"Download Results CSV",
			} {
				if strings.Contains(html, forbidden) {
					t.Errorf("Judge %s dashboard leaked forbidden element: %s", judgeKey, forbidden)
				}
			}
		})
	}

	// 5. Admin-only user (Admin role only, NOT organizer, NOT judge, NOT participant)
	t.Run("Admin_Only_Isolation", func(t *testing.T) {
		// Insert admin-only user directly into DB
		adminUID := "usr_admin_only_test"
		_, err := db.Exec("INSERT INTO users (id, email_normalized, display_name, created_at) VALUES (?, 'admin.only@example.org', 'Admin Only', '2026-02-01T00:00:00Z');", adminUID)
		if err != nil {
			t.Fatalf("insert admin user: %v", err)
		}
		var eventID string
		_ = db.QueryRow("SELECT id FROM events LIMIT 1;").Scan(&eventID)
		_, err = db.Exec("INSERT INTO event_roles (event_id, user_id, role, granted_at) VALUES (?, ?, 'admin', '2026-02-01T00:00:00Z');", eventID, adminUID)
		if err != nil {
			t.Fatalf("insert admin role: %v", err)
		}
		adminSessionToken, err := auth.CreateSession(context.Background(), db, adminUID, 24*time.Hour)
		if err != nil {
			t.Fatalf("create admin session: %v", err)
		}

		reqDash := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
		reqDash.AddCookie(&http.Cookie{Name: "session", Value: adminSessionToken})
		rrDash := httptest.NewRecorder()
		handler.ServeHTTP(rrDash, reqDash)
		if rrDash.Code != http.StatusOK {
			t.Fatalf("admin dashboard expected 200, got %d", rrDash.Code)
		}
		html := rrDash.Body.String()

		// Positive assertions: Admin has administrative / organizer workspace access
		for _, required := range []string{
			"Organizer Workspace",
			"id=\"organizer-workspace\"",
			"Event Lifecycle Windows",
			"All Teams (",
			"All Projects (",
			"Download Results CSV",
			"Gallery",
			"Sign Out",
		} {
			if !strings.Contains(html, required) {
				t.Errorf("Admin-only user missing required element: %s", required)
			}
		}

		// Negative assertions: Admin without participant role MUST NOT see participant or judge workspace
		for _, forbidden := range []string{
			"Participant Workspace",
			"id=\"participant-workspace\"",
			"My Team",
			"Create Team",
			"+ Generate Invite Link",
			"My Project",
			"Create Project",
			"Submit Project",
			"Judge Workspace",
			"id=\"judge-workspace\"",
		} {
			if strings.Contains(html, forbidden) {
				t.Errorf("Admin-only dashboard leaked forbidden element: %s", forbidden)
			}
		}
	})

	// 6. Dual-Role User (Organizer + Participant explicitly assigned in DB)
	t.Run("Dual_Role_Organizer_Participant", func(t *testing.T) {
		dualUID := "usr_dual_test"
		_, err := db.Exec("INSERT INTO users (id, email_normalized, display_name, created_at) VALUES (?, 'dual.role@example.org', 'Dual Role User', '2026-02-01T00:00:00Z');", dualUID)
		if err != nil {
			t.Fatalf("insert dual user: %v", err)
		}
		var eventID string
		_ = db.QueryRow("SELECT id FROM events LIMIT 1;").Scan(&eventID)
		_, _ = db.Exec("INSERT INTO event_roles (event_id, user_id, role, granted_at) VALUES (?, ?, 'organizer', '2026-02-01T00:00:00Z');", eventID, dualUID)
		_, _ = db.Exec("INSERT INTO event_roles (event_id, user_id, role, granted_at) VALUES (?, ?, 'participant', '2026-02-01T00:00:00Z');", eventID, dualUID)

		dualToken, err := auth.CreateSession(context.Background(), db, dualUID, 24*time.Hour)
		if err != nil {
			t.Fatalf("create dual session: %v", err)
		}

		reqDash := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
		reqDash.AddCookie(&http.Cookie{Name: "session", Value: dualToken})
		rrDash := httptest.NewRecorder()
		handler.ServeHTTP(rrDash, reqDash)
		if rrDash.Code != http.StatusOK {
			t.Fatalf("dual dashboard expected 200, got %d", rrDash.Code)
		}
		html := rrDash.Body.String()

		// Both Organizer Workspace AND Participant Workspace MUST be present
		for _, required := range []string{
			"Organizer Workspace",
			"id=\"organizer-workspace\"",
			"Participant Workspace",
			"id=\"participant-workspace\"",
			"My Team",
			"My Project",
		} {
			if !strings.Contains(html, required) {
				t.Errorf("Dual-role user missing expected element: %s", required)
			}
		}

		// Judge Workspace MUST still be absent
		for _, forbidden := range []string{
			"Judge Workspace",
			"id=\"judge-workspace\"",
		} {
			if strings.Contains(html, forbidden) {
				t.Errorf("Dual-role user leaked forbidden element: %s", forbidden)
			}
		}
	})
}
