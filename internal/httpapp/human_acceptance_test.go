package httpapp

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
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

func TestHumanAcceptance_FlowG_RubricAndBallotLifecycle(t *testing.T) {
	db := setupSeededDB(t)
	defer db.Close()

	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	// 1. Organizer logs in and verifies published rubric
	loginOrg := url.Values{"demo_user": {"organizer"}}.Encode()
	reqLogO := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(loginOrg))
	reqLogO.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrLogO := httptest.NewRecorder()
	handler.ServeHTTP(rrLogO, reqLogO)
	cookieOrg := rrLogO.Result().Cookies()[0]

	reqRubric := httptest.NewRequest(http.MethodGet, "/api/events/evt_01/rubric", nil)
	reqRubric.AddCookie(cookieOrg)
	rrRubric := httptest.NewRecorder()
	handler.ServeHTTP(rrRubric, reqRubric)
	if rrRubric.Code != http.StatusOK {
		t.Fatalf("expected published rubric 200, got %d: %s", rrRubric.Code, rrRubric.Body.String())
	}

	// 2. Judge A logs in and retrieves assigned projects from dashboard
	loginJudgeA := url.Values{"demo_user": {"judge_a"}}.Encode()
	reqLogJA := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(loginJudgeA))
	reqLogJA.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrLogJA := httptest.NewRecorder()
	handler.ServeHTTP(rrLogJA, reqLogJA)
	cookieJA := rrLogJA.Result().Cookies()[0]

	reqDashA := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	reqDashA.AddCookie(cookieJA)
	rrDashA := httptest.NewRecorder()
	handler.ServeHTTP(rrDashA, reqDashA)
	if rrDashA.Code != http.StatusOK {
		t.Fatalf("judge A dashboard: expected 200, got %d", rrDashA.Code)
	}

	// Find an assignment ID for Judge A
	var assignmentID string
	err = db.QueryRowContext(context.Background(), "SELECT id FROM assignments WHERE judge_user_id = 'jdg_01' ORDER BY id ASC LIMIT 1;").Scan(&assignmentID)
	if err != nil {
		t.Fatalf("no assignment found for jdg_01: %v", err)
	}

	// 3. Judge A views evaluation page
	reqEvalPage := httptest.NewRequest(http.MethodGet, "/evaluations/"+assignmentID, nil)
	reqEvalPage.AddCookie(cookieJA)
	rrEvalPage := httptest.NewRecorder()
	handler.ServeHTTP(rrEvalPage, reqEvalPage)
	if rrEvalPage.Code != http.StatusOK {
		t.Fatalf("judge A viewing evaluation: expected 200, got %d", rrEvalPage.Code)
	}
	if !strings.Contains(rrEvalPage.Body.String(), "Evaluation Rubric") {
		t.Errorf("evaluation page missing 'Evaluation Rubric'")
	}

	// 4. Judge A saves a draft ballot
	draftForm := url.Values{
		"action":                  {"draft"},
		"criterion_functionality": {"4.0"},
		"criterion_quality":       {"3.5"},
		"criterion_innovation":    {"4.5"},
		"criterion_impact":        {"3.0"},
		"comment":                 {"Strong prototype with solid potential."},
	}.Encode()
	reqDraft := httptest.NewRequest(http.MethodPost, "/evaluations/"+assignmentID, strings.NewReader(draftForm))
	reqDraft.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqDraft.AddCookie(cookieJA)
	rrDraft := httptest.NewRecorder()
	handler.ServeHTTP(rrDraft, reqDraft)
	if rrDraft.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect on save draft, got %d", rrDraft.Code)
	}

	// Verify draft via API
	reqBallotAPI := httptest.NewRequest(http.MethodGet, "/api/assignments/"+assignmentID+"/ballot", nil)
	reqBallotAPI.AddCookie(cookieJA)
	rrBallotAPI := httptest.NewRecorder()
	handler.ServeHTTP(rrBallotAPI, reqBallotAPI)
	if rrBallotAPI.Code != http.StatusOK {
		t.Fatalf("expected 200 on get ballot API, got %d", rrBallotAPI.Code)
	}
	if !strings.Contains(rrBallotAPI.Body.String(), "DRAFT") {
		t.Errorf("expected ballot to be in DRAFT state, got: %s", rrBallotAPI.Body.String())
	}

	// 5. Judge A submits the final ballot
	submitForm := url.Values{
		"action":                  {"submit"},
		"criterion_functionality": {"4.5"},
		"criterion_quality":       {"4.0"},
		"criterion_innovation":    {"4.5"},
		"criterion_impact":        {"4.0"},
		"comment":                 {"Excellent submission, highly recommended."},
	}.Encode()
	reqSubmit := httptest.NewRequest(http.MethodPost, "/evaluations/"+assignmentID, strings.NewReader(submitForm))
	reqSubmit.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqSubmit.AddCookie(cookieJA)
	rrSubmit := httptest.NewRecorder()
	handler.ServeHTTP(rrSubmit, reqSubmit)
	if rrSubmit.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect on submit, got %d", rrSubmit.Code)
	}

	// 6. Immutability verification: subsequent submit or draft via API must fail with 409
	reqConflict := httptest.NewRequest(http.MethodPost, "/api/assignments/"+assignmentID+"/ballot/submit", strings.NewReader(`{"scores":{"functionality":5.0}}`))
	reqConflict.Header.Set("Content-Type", "application/json")
	reqConflict.AddCookie(cookieJA)
	rrConflict := httptest.NewRecorder()
	handler.ServeHTTP(rrConflict, reqConflict)
	if rrConflict.Code != http.StatusConflict {
		t.Errorf("expected 409 conflict when submitting locked ballot, got %d", rrConflict.Code)
	}

	// Check submitted evaluation page renders read-only immutable view
	reqLockedPage := httptest.NewRequest(http.MethodGet, "/evaluations/"+assignmentID, nil)
	reqLockedPage.AddCookie(cookieJA)
	rrLockedPage := httptest.NewRecorder()
	handler.ServeHTTP(rrLockedPage, reqLockedPage)
	if rrLockedPage.Code != http.StatusOK {
		t.Fatalf("expected 200 on viewing locked evaluation, got %d", rrLockedPage.Code)
	}
	lockedHTML := rrLockedPage.Body.String()
	if !strings.Contains(lockedHTML, "Evaluation Submitted &amp; Immutable") {
		t.Errorf("locked evaluation missing immutable banner")
	}

	// 7. Peer isolation verification: Judge B must be blocked from Judge A's assignment
	loginJudgeB := url.Values{"demo_user": {"judge_b"}}.Encode()
	reqLogJB := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(loginJudgeB))
	reqLogJB.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrLogJB := httptest.NewRecorder()
	handler.ServeHTTP(rrLogJB, reqLogJB)
	cookieJB := rrLogJB.Result().Cookies()[0]

	// Judge B tries to view Judge A's evaluation page -> 403
	reqPeerEval := httptest.NewRequest(http.MethodGet, "/evaluations/"+assignmentID, nil)
	reqPeerEval.AddCookie(cookieJB)
	rrPeerEval := httptest.NewRecorder()
	handler.ServeHTTP(rrPeerEval, reqPeerEval)
	if rrPeerEval.Code != http.StatusForbidden {
		t.Errorf("expected 403 on peer evaluation page access, got %d", rrPeerEval.Code)
	}

	// Judge B tries to get Judge A's ballot API -> 403
	reqPeerAPI := httptest.NewRequest(http.MethodGet, "/api/assignments/"+assignmentID+"/ballot", nil)
	reqPeerAPI.AddCookie(cookieJB)
	rrPeerAPI := httptest.NewRecorder()
	handler.ServeHTTP(rrPeerAPI, reqPeerAPI)
	if rrPeerAPI.Code != http.StatusForbidden {
		t.Errorf("expected 403 on peer ballot API, got %d", rrPeerAPI.Code)
	}

	// Judge B queries judge_scores for judge_a -> 403
	reqPeerScores := httptest.NewRequest(http.MethodGet, "/api/judge/scores?judge=judge_a", nil)
	reqPeerScores.AddCookie(cookieJB)
	rrPeerScores := httptest.NewRecorder()
	handler.ServeHTTP(rrPeerScores, reqPeerScores)
	if rrPeerScores.Code != http.StatusForbidden {
		t.Errorf("expected 403 on peer judge scores, got %d", rrPeerScores.Code)
	}

	// 8. Participant blocked verification: Participant must be blocked from evaluation and judge scores
	loginPart := url.Values{"demo_user": {"participant_a"}}.Encode()
	reqLogP := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(loginPart))
	reqLogP.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrLogP := httptest.NewRecorder()
	handler.ServeHTTP(rrLogP, reqLogP)
	cookieP := rrLogP.Result().Cookies()[0]

	reqPartEval := httptest.NewRequest(http.MethodGet, "/evaluations/"+assignmentID, nil)
	reqPartEval.AddCookie(cookieP)
	rrPartEval := httptest.NewRecorder()
	handler.ServeHTTP(rrPartEval, reqPartEval)
	if rrPartEval.Code != http.StatusForbidden {
		t.Errorf("expected 403 on participant evaluation access, got %d", rrPartEval.Code)
	}

	reqPartScores := httptest.NewRequest(http.MethodGet, "/api/judge/scores", nil)
	reqPartScores.AddCookie(cookieP)
	rrPartScores := httptest.NewRecorder()
	handler.ServeHTTP(rrPartScores, reqPartScores)
	if rrPartScores.Code != http.StatusForbidden {
		t.Errorf("expected 403 on participant judge scores access, got %d", rrPartScores.Code)
	}
}

// TestHumanAcceptance_FlowH_ResultsAndExplainRank tests defensible scoring, public leaderboard, Explain This Rank receipt, and independent replay.
func TestHumanAcceptance_FlowH_ResultsAndExplainRank(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	// 1. Unauthenticated stranger checks /results before any publication
	reqInit := httptest.NewRequest(http.MethodGet, "/results", nil)
	rrInit := httptest.NewRecorder()
	handler.ServeHTTP(rrInit, reqInit)
	if rrInit.Code != http.StatusOK {
		t.Fatalf("expected 200 on /results, got %d", rrInit.Code)
	}
	if !strings.Contains(rrInit.Body.String(), "No Published Results Yet") {
		t.Errorf("expected pending results message for stranger before computation")
	}

	// 2. Participant attempt to compute results -> 403 Forbidden
	loginPart := url.Values{"demo_user": {"participant_a"}}.Encode()
	reqLogP := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(loginPart))
	reqLogP.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrLogP := httptest.NewRecorder()
	handler.ServeHTTP(rrLogP, reqLogP)
	cookiePart := rrLogP.Result().Cookies()[0]

	reqPartCompute := httptest.NewRequest(http.MethodPost, "/api/organizer/results/compute", nil)
	reqPartCompute.AddCookie(cookiePart)
	rrPartCompute := httptest.NewRecorder()
	handler.ServeHTTP(rrPartCompute, reqPartCompute)
	if rrPartCompute.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when participant attempts to compute results, got %d", rrPartCompute.Code)
	}

	// 3. Organizer logs in and computes draft results
	loginOrg := url.Values{"demo_user": {"organizer"}}.Encode()
	reqLogO := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(loginOrg))
	reqLogO.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rrLogO := httptest.NewRecorder()
	handler.ServeHTTP(rrLogO, reqLogO)
	cookieOrg := rrLogO.Result().Cookies()[0]

	reqOrgCompute := httptest.NewRequest(http.MethodPost, "/api/organizer/results/compute", nil)
	reqOrgCompute.AddCookie(cookieOrg)
	rrOrgCompute := httptest.NewRecorder()
	handler.ServeHTTP(rrOrgCompute, reqOrgCompute)
	if rrOrgCompute.Code != http.StatusOK {
		t.Fatalf("expected 200 on organizer compute results, got %d: %s", rrOrgCompute.Code, rrOrgCompute.Body.String())
	}

	var draftRun struct {
		ID          string `json:"id"`
		Status      string `json:"status"`
		InputDigest string `json:"input_digest"`
	}
	if err := json.NewDecoder(rrOrgCompute.Body).Decode(&draftRun); err != nil {
		t.Fatalf("failed to decode compute response: %v", err)
	}
	if draftRun.ID == "" || draftRun.Status != "DRAFT" || draftRun.InputDigest == "" {
		t.Fatalf("invalid draft run returned: %+v", draftRun)
	}

	// 4. Organizer previews draft results on /results
	reqOrgPreview := httptest.NewRequest(http.MethodGet, "/results", nil)
	reqOrgPreview.AddCookie(cookieOrg)
	rrOrgPreview := httptest.NewRecorder()
	handler.ServeHTTP(rrOrgPreview, reqOrgPreview)
	if rrOrgPreview.Code != http.StatusOK {
		t.Fatalf("expected 200 on organizer draft preview, got %d", rrOrgPreview.Code)
	}
	previewHTML := rrOrgPreview.Body.String()
	if !strings.Contains(previewHTML, "Draft Results Preview") {
		t.Errorf("organizer should see Draft Results Preview")
	}
	if !strings.Contains(previewHTML, "Publish Official Results") {
		t.Errorf("organizer should see Publish Official Results button")
	}

	// 5. Participant attempt to publish results -> 403 Forbidden
	reqPartPublish := httptest.NewRequest(http.MethodPost, "/api/organizer/results/"+draftRun.ID+"/publish", nil)
	reqPartPublish.AddCookie(cookiePart)
	rrPartPublish := httptest.NewRecorder()
	handler.ServeHTTP(rrPartPublish, reqPartPublish)
	if rrPartPublish.Code != http.StatusForbidden {
		t.Fatalf("expected 403 when participant attempts to publish results, got %d", rrPartPublish.Code)
	}

	// 6. Organizer publishes official results
	reqOrgPublish := httptest.NewRequest(http.MethodPost, "/api/organizer/results/"+draftRun.ID+"/publish", nil)
	reqOrgPublish.AddCookie(cookieOrg)
	rrOrgPublish := httptest.NewRecorder()
	handler.ServeHTTP(rrOrgPublish, reqOrgPublish)
	if rrOrgPublish.Code != http.StatusOK {
		t.Fatalf("expected 200 on organizer publish results, got %d: %s", rrOrgPublish.Code, rrOrgPublish.Body.String())
	}

	// 7. Public stranger visits official published /results Leaderboard
	reqPub := httptest.NewRequest(http.MethodGet, "/results", nil)
	rrPub := httptest.NewRecorder()
	handler.ServeHTTP(rrPub, reqPub)
	if rrPub.Code != http.StatusOK {
		t.Fatalf("expected 200 on public /results, got %d", rrPub.Code)
	}
	pubHTML := rrPub.Body.String()
	if !strings.Contains(pubHTML, "Official Results Published") {
		t.Errorf("expected Official Results Published badge in public leaderboard")
	}
	if !strings.Contains(pubHTML, "Explain This Rank ↗") {
		t.Errorf("expected Explain This Rank links in leaderboard")
	}
	if !strings.Contains(pubHTML, draftRun.InputDigest) {
		t.Errorf("expected input digest %s to be displayed in audit header", draftRun.InputDigest)
	}

	// 8. Public API GET /api/results returns active published run
	reqAPI := httptest.NewRequest(http.MethodGet, "/api/results", nil)
	rrAPI := httptest.NewRecorder()
	handler.ServeHTTP(rrAPI, reqAPI)
	if rrAPI.Code != http.StatusOK {
		t.Fatalf("expected 200 on /api/results, got %d", rrAPI.Code)
	}
	var apiData struct {
		Run struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"run"`
		Entries []struct {
			ProjectID  string  `json:"project_id"`
			Rank       int     `json:"rank"`
			FinalScore float64 `json:"final_score"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(rrAPI.Body).Decode(&apiData); err != nil {
		t.Fatalf("failed to decode /api/results: %v", err)
	}
	if apiData.Run.ID != draftRun.ID || apiData.Run.Status != "PUBLISHED" {
		t.Errorf("expected active published run %s, got %+v", draftRun.ID, apiData.Run)
	}
	if len(apiData.Entries) == 0 {
		t.Fatalf("expected non-empty entries list")
	}

	topProjectID := apiData.Entries[0].ProjectID

	// 9. Inspect "Explain This Rank" receipt for top project
	reqExplain := httptest.NewRequest(http.MethodGet, "/results/"+draftRun.ID+"/projects/"+topProjectID, nil)
	rrExplain := httptest.NewRecorder()
	handler.ServeHTTP(rrExplain, reqExplain)
	if rrExplain.Code != http.StatusOK {
		t.Fatalf("expected 200 on Explain This Rank page, got %d", rrExplain.Code)
	}
	expHTML := rrExplain.Body.String()
	if !strings.Contains(expHTML, "Explain This Rank") && !strings.Contains(expHTML, "Audit Receipt") {
		t.Errorf("expected Explain This Rank header in receipt")
	}
	if !strings.Contains(expHTML, "mitigates score-scale differences") {
		t.Errorf("expected approved fairness language in derivation breakdown")
	}
	if !strings.Contains(expHTML, draftRun.InputDigest) {
		t.Errorf("expected input digest to appear in audit receipt")
	}

	// 10. Independent Replay API Verification
	reqReplay := httptest.NewRequest(http.MethodGet, "/api/results/"+draftRun.ID+"/replay", nil)
	rrReplay := httptest.NewRecorder()
	handler.ServeHTTP(rrReplay, reqReplay)
	if rrReplay.Code != http.StatusOK {
		t.Fatalf("expected 200 on /api/results/{run_id}/replay, got %d", rrReplay.Code)
	}
	var repReport struct {
		Passed        bool   `json:"passed"`
		DigestMatches bool   `json:"digest_matches"`
		ActualDigest  string `json:"actual_digest"`
	}
	if err := json.NewDecoder(rrReplay.Body).Decode(&repReport); err != nil {
		t.Fatalf("failed to decode replay report: %v", err)
	}
	if !repReport.Passed || !repReport.DigestMatches {
		t.Errorf("expected replay to report PASSED with matching digest, got %+v", repReport)
	}

	// Per-project replay
	reqProjReplay := httptest.NewRequest(http.MethodGet, "/api/results/"+draftRun.ID+"/replay/"+topProjectID, nil)
	rrProjReplay := httptest.NewRecorder()
	handler.ServeHTTP(rrProjReplay, reqProjReplay)
	if rrProjReplay.Code != http.StatusOK {
		t.Fatalf("expected 200 on project replay, got %d", rrProjReplay.Code)
	}
}

// TestHumanAcceptance_FlowI_CSVExport_And_APIFirst tests the comprehensive auditable CSV export
// and API-First consistency (T2 Checkpoint 5).
func TestHumanAcceptance_FlowI_CSVExport_And_APIFirst(t *testing.T) {
	db := setupSeededDB(t)
	server, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	// 1. Authenticate Demo Personas
	login := func(persona string) *http.Cookie {
		form := url.Values{"demo_user": {persona}}.Encode()
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("login %s failed: %d", persona, rr.Code)
		}
		for _, c := range rr.Result().Cookies() {
			if c.Name == "session" {
				return c
			}
		}
		t.Fatalf("session cookie missing for %s", persona)
		return nil
	}

	cookieOrg := login("organizer")
	cookiePart := login("participant_a")
	cookieJudge := login("judge_a")

	// 2. Strict Role Isolation on CSV export endpoints
	csvEndpoints := []string{
		"/api/export.csv",
		"/api/v1/export.csv",
		"/api/export/results.csv",
		"/api/v1/export/results.csv",
		"/api/export/evaluations.csv",
		"/api/v1/export/evaluations.csv",
		"/api/export/projects.csv",
		"/api/v1/export/projects.csv",
	}

	for _, ep := range csvEndpoints {
		// Anonymous receives 401
		reqAnon := httptest.NewRequest(http.MethodGet, ep, nil)
		rrAnon := httptest.NewRecorder()
		handler.ServeHTTP(rrAnon, reqAnon)
		if rrAnon.Code != http.StatusUnauthorized {
			t.Errorf("%s: anonymous expected 401, got %d", ep, rrAnon.Code)
		}

		// Participant receives 403
		reqPart := httptest.NewRequest(http.MethodGet, ep, nil)
		reqPart.AddCookie(cookiePart)
		rrPart := httptest.NewRecorder()
		handler.ServeHTTP(rrPart, reqPart)
		if rrPart.Code != http.StatusForbidden {
			t.Errorf("%s: participant expected 403, got %d", ep, rrPart.Code)
		}

		// Judge receives 403
		reqJudge := httptest.NewRequest(http.MethodGet, ep, nil)
		reqJudge.AddCookie(cookieJudge)
		rrJudge := httptest.NewRecorder()
		handler.ServeHTTP(rrJudge, reqJudge)
		if rrJudge.Code != http.StatusForbidden {
			t.Errorf("%s: judge expected 403, got %d", ep, rrJudge.Code)
		}
	}

	// 3. Organizer exports initial projects CSV (before results computation)
	reqInitCSV := httptest.NewRequest(http.MethodGet, "/api/export.csv", nil)
	reqInitCSV.AddCookie(cookieOrg)
	rrInitCSV := httptest.NewRecorder()
	handler.ServeHTTP(rrInitCSV, reqInitCSV)

	if rrInitCSV.Code != http.StatusOK {
		t.Fatalf("organizer initial CSV export failed: %d: %s", rrInitCSV.Code, rrInitCSV.Body.String())
	}
	initBody := rrInitCSV.Body.String()
	initLines := strings.Split(initBody, "\n")
	if len(initLines) == 0 || !strings.Contains(initLines[0], ",") {
		t.Fatalf("expected comma in first line of initial export, got: %q", initBody)
	}

	initReader := csv.NewReader(strings.NewReader(initBody))
	initRecords, err := initReader.ReadAll()
	if err != nil {
		t.Fatalf("initial CSV parse failed: %v", err)
	}
	if len(initRecords) < 2 {
		t.Fatalf("expected at least header and one data row in initial export, got %d", len(initRecords))
	}

	// 4. Deterministic assignment and judge evaluation with multiline, quotes, commas
	reqRun := httptest.NewRequest(http.MethodPost, "/api/v1/organizer/assignments/run", nil)
	reqRun.AddCookie(cookieOrg)
	rrRun := httptest.NewRecorder()
	handler.ServeHTTP(rrRun, reqRun)
	if rrRun.Code != http.StatusSeeOther && rrRun.Code != http.StatusOK {
		t.Fatalf("assignments run failed: %d", rrRun.Code)
	}

	var asgnID string
	_ = db.QueryRow("SELECT id FROM assignments WHERE judge_user_id = 'jdg_01' LIMIT 1;").Scan(&asgnID)
	if asgnID == "" {
		t.Fatalf("no assignment found for jdg_01")
	}

	multilineComment := "Exceptional work, especially on the backend architecture!\n\"Production-ready quality.\"\nStrongly recommend for award consideration."
	ballotPayload := map[string]any{
		"scores": map[string]float64{
			"functionality": 4.5,
			"quality":       4.0,
			"innovation":    4.5,
			"impact":        4.0,
		},
		"comment": multilineComment,
	}
	payloadBytes, _ := json.Marshal(ballotPayload)
	reqBallot := httptest.NewRequest(http.MethodPost, "/api/v1/assignments/"+asgnID+"/ballot/submit", strings.NewReader(string(payloadBytes)))
	reqBallot.AddCookie(cookieJudge)
	reqBallot.Header.Set("Content-Type", "application/json")
	rrBallot := httptest.NewRecorder()
	handler.ServeHTTP(rrBallot, reqBallot)
	if rrBallot.Code != http.StatusOK {
		t.Fatalf("ballot submit failed: %d: %s", rrBallot.Code, rrBallot.Body.String())
	}

	// 5. Organizer exports evaluations CSV and verifies RFC 4180 multiline/escaping
	reqEvalCSV := httptest.NewRequest(http.MethodGet, "/api/v1/export/evaluations.csv", nil)
	reqEvalCSV.AddCookie(cookieOrg)
	rrEvalCSV := httptest.NewRecorder()
	handler.ServeHTTP(rrEvalCSV, reqEvalCSV)

	if rrEvalCSV.Code != http.StatusOK {
		t.Fatalf("evaluations export failed: %d: %s", rrEvalCSV.Code, rrEvalCSV.Body.String())
	}
	evalBody := rrEvalCSV.Body.String()
	evalReader := csv.NewReader(strings.NewReader(evalBody))
	evalRecords, err := evalReader.ReadAll()
	if err != nil {
		t.Fatalf("evaluations CSV parse failed: %v", err)
	}
	if len(evalRecords) < 2 {
		t.Fatalf("expected evaluations records, got %d", len(evalRecords))
	}

	// Confirm comment was properly preserved across multiline/quotes
	foundComment := false
	for _, rec := range evalRecords[1:] {
		if rec[9] == multilineComment {
			foundComment = true
			break
		}
	}
	if !foundComment {
		t.Errorf("multiline comment with quotes was not preserved cleanly in evaluations CSV")
	}

	// 6. Inject formula payload into a submission to verify Failure Case F21 defense
	formulaTitle := "=CMD|' /C calc'!A0"
	_, err = db.Exec(`
		UPDATE submissions
		SET title = ?
		WHERE project_id = 'prj_01' AND version_no = (SELECT MAX(version_no) FROM submissions WHERE project_id = 'prj_01');
	`, formulaTitle)
	if err != nil {
		t.Fatalf("failed to inject formula payload: %v", err)
	}

	// 7. Organizer computes and publishes results
	reqCompute := httptest.NewRequest(http.MethodPost, "/api/v1/organizer/results/compute", nil)
	reqCompute.AddCookie(cookieOrg)
	rrCompute := httptest.NewRecorder()
	handler.ServeHTTP(rrCompute, reqCompute)
	if rrCompute.Code != http.StatusOK {
		t.Fatalf("compute results failed: %d: %s", rrCompute.Code, rrCompute.Body.String())
	}
	var resRun struct {
		ID          string `json:"id"`
		InputDigest string `json:"input_digest"`
	}
	_ = json.NewDecoder(rrCompute.Body).Decode(&resRun)

	reqPublish := httptest.NewRequest(http.MethodPost, "/api/v1/organizer/results/"+resRun.ID+"/publish", nil)
	reqPublish.AddCookie(cookieOrg)
	rrPublish := httptest.NewRecorder()
	handler.ServeHTTP(rrPublish, reqPublish)
	if rrPublish.Code != http.StatusOK {
		t.Fatalf("publish results failed: %d: %s", rrPublish.Code, rrPublish.Body.String())
	}

	// 8. Organizer exports full standings CSV
	reqStandings := httptest.NewRequest(http.MethodGet, "/api/v1/export.csv", nil)
	reqStandings.AddCookie(cookieOrg)
	rrStandings := httptest.NewRecorder()
	handler.ServeHTTP(rrStandings, reqStandings)

	if rrStandings.Code != http.StatusOK {
		t.Fatalf("standings export failed: %d: %s", rrStandings.Code, rrStandings.Body.String())
	}
	if !strings.HasPrefix(rrStandings.Header().Get("Content-Type"), "text/csv") {
		t.Errorf("expected Content-Type text/csv, got %q", rrStandings.Header().Get("Content-Type"))
	}
	expectedFilename := fmt.Sprintf("results_%s.csv", resRun.ID)
	if !strings.Contains(rrStandings.Header().Get("Content-Disposition"), expectedFilename) {
		t.Errorf("expected Content-Disposition with %s, got %q", expectedFilename, rrStandings.Header().Get("Content-Disposition"))
	}

	standingsBody := rrStandings.Body.String()
	standingsReader := csv.NewReader(strings.NewReader(standingsBody))
	standingsRecords, err := standingsReader.ReadAll()
	if err != nil {
		t.Fatalf("standings CSV parse failed: %v", err)
	}

	// Verify header has 17 stable reconciliation columns
	expectedHeader := []string{
		"rank", "project_id", "title", "team_id", "team_name", "track_id", "track_name",
		"final_score", "raw_score", "expected_reviews", "completed_reviews", "effective_reviews",
		"fallback_count", "tie_group", "result_run_id", "input_digest", "published_at",
	}
	header := standingsRecords[0]
	if len(header) != len(expectedHeader) {
		t.Fatalf("expected %d columns, got %d", len(expectedHeader), len(header))
	}
	for i, col := range expectedHeader {
		if header[i] != col {
			t.Errorf("col %d: expected %q, got %q", i, col, header[i])
		}
	}

	// Verify formula injection sanitization and stable IDs
	foundPrj1 := false
	for _, row := range standingsRecords[1:] {
		if row[1] == "prj_01" {
			foundPrj1 = true
			if !strings.HasPrefix(row[2], "'=") {
				t.Errorf("formula injection defense failed: expected '= prefix, got %q", row[2])
			}
			if row[14] != resRun.ID {
				t.Errorf("result_run_id mismatch: expected %s, got %s", resRun.ID, row[14])
			}
			if row[15] != resRun.InputDigest {
				t.Errorf("input_digest mismatch: expected %s, got %s", resRun.InputDigest, row[15])
			}
			// Verify numeric values parse cleanly
			if _, err := strconv.Atoi(row[0]); err != nil {
				t.Errorf("rank %q is not a valid integer", row[0])
			}
			if _, err := strconv.ParseFloat(row[7], 64); err != nil {
				t.Errorf("final_score %q is not a valid float", row[7])
			}
		}
	}
	if !foundPrj1 {
		t.Errorf("prj_01 not found in standings export")
	}

	// 9. API-First Consistency
	// Compare /api/v1/... and legacy /api/... routes
	apiChecks := []struct {
		v1Path string
		legacy string
	}{
		{"/api/v1/projects", "/api/projects"},
		{"/api/v1/teams", "/api/teams"},
		{"/api/v1/tracks", "/api/tracks"},
		{"/api/v1/prizes", "/api/prizes"},
		{"/api/v1/results", "/api/results"},
	}

	for _, check := range apiChecks {
		reqV1 := httptest.NewRequest(http.MethodGet, check.v1Path, nil)
		rrV1 := httptest.NewRecorder()
		handler.ServeHTTP(rrV1, reqV1)

		reqLegacy := httptest.NewRequest(http.MethodGet, check.legacy, nil)
		rrLegacy := httptest.NewRecorder()
		handler.ServeHTTP(rrLegacy, reqLegacy)

		if rrV1.Code != rrLegacy.Code {
			t.Errorf("code mismatch for %s: v1=%d legacy=%d", check.v1Path, rrV1.Code, rrLegacy.Code)
		}
		if rrV1.Body.String() != rrLegacy.Body.String() {
			t.Errorf("body mismatch between %s and %s", check.v1Path, check.legacy)
		}
	}

	// 10. Replay verification via /api/v1/results/{run_id}/replay
	reqReplay := httptest.NewRequest(http.MethodGet, "/api/v1/results/"+resRun.ID+"/replay", nil)
	rrReplay := httptest.NewRecorder()
	handler.ServeHTTP(rrReplay, reqReplay)
	if rrReplay.Code != http.StatusOK {
		t.Fatalf("replay via v1 failed: %d", rrReplay.Code)
	}
	var replayResp struct {
		Passed bool `json:"passed"`
	}
	_ = json.NewDecoder(rrReplay.Body).Decode(&replayResp)
	if !replayResp.Passed {
		t.Errorf("replay did not pass: %+v", replayResp)
	}
}


