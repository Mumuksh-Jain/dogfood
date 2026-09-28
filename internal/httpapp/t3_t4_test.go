package httpapp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dogfood/internal/auth"
	_ "modernc.org/sqlite"
)

// setupT3T4TestServer builds an in-memory DB seeded with official fixtures.
func setupT3T4TestServer(t *testing.T) (*Server, *sql.DB, string, string) {
	t.Helper()
	db := setupSeededDB(t)

	// Ensure community settings exist for event
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `
		INSERT OR REPLACE INTO event_community_settings (event_id, voting_open, results_hidden, updated_at)
		VALUES ('evt_01', 1, 1, '2026-03-01T00:00:00Z');
	`)

	srv, err := NewServer(Config{Port: 8080, DB: db})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	// Create a second participant session for multi-user tests (user created by NewServer/ensureDemoUsers)
	tokHash2 := auth.HashToken("prt_demo_b")
	_, err = db.ExecContext(ctx, `
		INSERT OR REPLACE INTO sessions (id, user_id, token_hash, created_at, expires_at, last_seen_at)
		VALUES ('ses_prt_b', 'usr_demo_part_b', ?, '2026-01-01T00:00:00Z', '2035-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
	`, tokHash2)
	if err != nil {
		t.Fatalf("failed to insert second participant session: %v", err)
	}

	return srv, db, "prj_01", "prj_02"
}

func executeRequest(srv *Server, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	return rr
}

// -------------------------------------------------------------------------
// T3.1 & T3.2 Community Voting & Duplicate Protection Tests
// -------------------------------------------------------------------------

func TestT3_CommunityVoting_HappyPath_And_DuplicateProtection(t *testing.T) {
	srv, _, prj1, _ := setupT3T4TestServer(t)

	// 1. Unauthenticated vote attempt should be rejected with 401
	reqAnon := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/projects/%s/vote", prj1), nil)
	respAnon := executeRequest(srv, reqAnon)
	if respAnon.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for anonymous vote, got %d: %s", respAnon.Code, respAnon.Body.String())
	}

	// 2. Participant 1 casts a valid vote
	reqVote1 := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/projects/%s/vote", prj1), nil)
	reqVote1.Header.Set("Cookie", "session=prt_2e88")
	respVote1 := executeRequest(srv, reqVote1)
	if respVote1.Code != http.StatusCreated {
		t.Fatalf("expected 201 for valid vote, got %d: %s", respVote1.Code, respVote1.Body.String())
	}

	// 3. Participant 1 attempts duplicate vote on same project -> must fail with 409 Conflict
	reqVoteDup := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/projects/%s/vote", prj1), nil)
	reqVoteDup.Header.Set("Cookie", "session=prt_2e88")
	respVoteDup := executeRequest(srv, reqVoteDup)
	if respVoteDup.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for duplicate vote, got %d: %s", respVoteDup.Code, respVoteDup.Body.String())
	}

	// 4. Participant 2 casts a valid vote on prj1 -> should succeed
	reqVote2 := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/projects/%s/vote", prj1), nil)
	reqVote2.Header.Set("Cookie", "session=prt_demo_b")
	respVote2 := executeRequest(srv, reqVote2)
	if respVote2.Code != http.StatusCreated {
		t.Fatalf("expected 201 for second user vote, got %d: %s", respVote2.Code, respVote2.Body.String())
	}
}

func TestT3_CommunityVoting_Retract(t *testing.T) {
	srv, _, prj1, _ := setupT3T4TestServer(t)

	// Vote first
	reqVote := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/projects/%s/vote", prj1), nil)
	reqVote.Header.Set("Cookie", "session=prt_2e88")
	respVote := executeRequest(srv, reqVote)
	if respVote.Code != http.StatusCreated {
		t.Fatalf("expected 201 for vote, got %d: %s", respVote.Code, respVote.Body.String())
	}

	// Retract vote
	reqRetract := httptest.NewRequest("DELETE", fmt.Sprintf("/api/v1/projects/%s/vote", prj1), nil)
	reqRetract.Header.Set("Cookie", "session=prt_2e88")
	respRetract := executeRequest(srv, reqRetract)
	if respRetract.Code != http.StatusOK {
		t.Fatalf("expected 200 for vote retraction, got %d: %s", respRetract.Code, respRetract.Body.String())
	}

	// Retract second time -> should return 404 (no vote to retract)
	reqRetract2 := httptest.NewRequest("DELETE", fmt.Sprintf("/api/v1/projects/%s/vote", prj1), nil)
	reqRetract2.Header.Set("Cookie", "session=prt_2e88")
	respRetract2 := executeRequest(srv, reqRetract2)
	if respRetract2.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for retracting non-existent vote, got %d: %s", respRetract2.Code, respRetract2.Body.String())
	}
}

// -------------------------------------------------------------------------
// T3.3 Rate Limiting Test
// -------------------------------------------------------------------------

func TestT3_RateLimiting(t *testing.T) {
	srv, _, prj1, _ := setupT3T4TestServer(t)
	// Replace rate limiter with tight limit: max 2 hits per 10 seconds
	srv.rateLimiter = newVotingRateLimiter(10*time.Second, 2)

	// Hit 1
	req1 := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/projects/%s/vote", prj1), nil)
	req1.Header.Set("Cookie", "session=prt_2e88")
	resp1 := executeRequest(srv, req1)
	if resp1.Code != http.StatusCreated {
		t.Fatalf("expected 201 for hit 1, got %d", resp1.Code)
	}

	// Hit 2 (retract)
	req2 := httptest.NewRequest("DELETE", fmt.Sprintf("/api/v1/projects/%s/vote", prj1), nil)
	req2.Header.Set("Cookie", "session=prt_2e88")
	resp2 := executeRequest(srv, req2)
	if resp2.Code != http.StatusOK {
		t.Fatalf("expected 200 for hit 2, got %d", resp2.Code)
	}

	// Hit 3 (exceeds maxHits=2 within window) -> must return 429 Too Many Requests
	req3 := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/projects/%s/vote", prj1), nil)
	req3.Header.Set("Cookie", "session=prt_2e88")
	resp3 := executeRequest(srv, req3)
	if resp3.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests on rate limit breach, got %d: %s", resp3.Code, resp3.Body.String())
	}
	if resp3.Header().Get("Retry-After") == "" {
		t.Errorf("expected Retry-After header on 429 response")
	}
}

// -------------------------------------------------------------------------
// T3.5 Hidden Results Visibility Policy Test
// -------------------------------------------------------------------------

func TestT3_HiddenResults_VisibilityPolicy(t *testing.T) {
	srv, _, prj1, _ := setupT3T4TestServer(t)

	// Cast a vote
	reqVote := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/projects/%s/vote", prj1), nil)
	reqVote.Header.Set("Cookie", "session=prt_2e88")
	respVote := executeRequest(srv, reqVote)
	if respVote.Code != http.StatusCreated {
		t.Fatalf("expected 201 for vote, got %d: %s", respVote.Code, respVote.Body.String())
	}

	// 1. Anonymous query: counts should be HIDDEN while voting is open
	reqGetAnon := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/projects/%s/votes", prj1), nil)
	respGetAnon := executeRequest(srv, reqGetAnon)
	if respGetAnon.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", respGetAnon.Code, respGetAnon.Body.String())
	}
	var resAnon map[string]any
	_ = json.Unmarshal(respGetAnon.Body.Bytes(), &resAnon)
	if resAnon["hidden"] != true {
		t.Fatalf("expected hidden=true for anonymous caller, got %v", resAnon["hidden"])
	}

	// 2. Participant query: counts should also be HIDDEN
	reqGetPart := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/projects/%s/votes", prj1), nil)
	reqGetPart.Header.Set("Cookie", "session=prt_demo_b")
	respGetPart := executeRequest(srv, reqGetPart)
	var resPart map[string]any
	_ = json.Unmarshal(respGetPart.Body.Bytes(), &resPart)
	if resPart["hidden"] != true {
		t.Fatalf("expected hidden=true for participant caller, got %v", resPart["hidden"])
	}

	// 3. Organizer query: counts should be VISIBLE
	reqGetOrg := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/projects/%s/votes", prj1), nil)
	reqGetOrg.Header.Set("Cookie", "session=org_7f2a")
	respGetOrg := executeRequest(srv, reqGetOrg)
	if respGetOrg.Code != http.StatusOK {
		t.Fatalf("expected 200 for organizer, got %d: %s", respGetOrg.Code, respGetOrg.Body.String())
	}
	var resOrg map[string]any
	_ = json.Unmarshal(respGetOrg.Body.Bytes(), &resOrg)
	if resOrg["hidden"] != false {
		t.Fatalf("expected hidden=false for organizer caller, got %v", resOrg["hidden"])
	}
	if fmt.Sprintf("%v", resOrg["vote_count"]) != "1" {
		t.Fatalf("expected vote_count=1 for organizer, got %v", resOrg["vote_count"])
	}
}

// -------------------------------------------------------------------------
// T3.6 Randomized Gallery Sort Test
// -------------------------------------------------------------------------

func TestT3_RandomizedGallerySort(t *testing.T) {
	srv, _, _, _ := setupT3T4TestServer(t)

	// Default sort (deterministic by ID)
	reqDef := httptest.NewRequest("GET", "/api/v1/projects", nil)
	respDef := executeRequest(srv, reqDef)
	if respDef.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", respDef.Code)
	}

	// Random sort with seed
	reqRand1 := httptest.NewRequest("GET", "/api/v1/projects?sort=random&seed=42", nil)
	respRand1 := executeRequest(srv, reqRand1)
	if respRand1.Code != http.StatusOK {
		t.Fatalf("expected 200 for random sort, got %d", respRand1.Code)
	}

	reqRand2 := httptest.NewRequest("GET", "/api/v1/projects?sort=random&seed=42", nil)
	respRand2 := executeRequest(srv, reqRand2)

	// Same seed must produce identical deterministic shuffle order
	if respRand1.Body.String() != respRand2.Body.String() {
		t.Fatalf("expected identical project order with identical seed")
	}

	// HTML gallery with sort=random should also succeed
	reqHtmlRand := httptest.NewRequest("GET", "/projects?sort=random", nil)
	respHtmlRand := executeRequest(srv, reqHtmlRand)
	if respHtmlRand.Code != http.StatusOK {
		t.Fatalf("expected 200 for HTML gallery random sort, got %d", respHtmlRand.Code)
	}
}

// -------------------------------------------------------------------------
// T3.4 Comments Create & List Test
// -------------------------------------------------------------------------

func TestT3_Comments_CreateAndList(t *testing.T) {
	srv, _, prj1, _ := setupT3T4TestServer(t)

	// 1. Post comment as participant
	commentBody := `{"content": "Outstanding technical architecture and presentation!"}`
	reqPost := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/projects/%s/comments", prj1), strings.NewReader(commentBody))
	reqPost.Header.Set("Cookie", "session=prt_2e88")
	reqPost.Header.Set("Content-Type", "application/json")
	respPost := executeRequest(srv, reqPost)
	if respPost.Code != http.StatusCreated {
		t.Fatalf("expected 201 for comment create, got %d: %s", respPost.Code, respPost.Body.String())
	}

	// 2. Empty comment must be rejected with 400
	reqEmpty := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/projects/%s/comments", prj1), strings.NewReader(`{"content": "   "}`))
	reqEmpty.Header.Set("Cookie", "session=prt_2e88")
	reqEmpty.Header.Set("Content-Type", "application/json")
	respEmpty := executeRequest(srv, reqEmpty)
	if respEmpty.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty comment, got %d", respEmpty.Code)
	}

	// 3. List comments publicly
	reqList := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/projects/%s/comments", prj1), nil)
	respList := executeRequest(srv, reqList)
	if respList.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", respList.Code)
	}
	var res map[string]any
	_ = json.Unmarshal(respList.Body.Bytes(), &res)
	if fmt.Sprintf("%v", res["count"]) != "1" {
		t.Fatalf("expected 1 comment, got %v", res["count"])
	}
}

// -------------------------------------------------------------------------
// T3.7 Audit Trail & Access Control Test
// -------------------------------------------------------------------------

func TestT3_AuditTrail_AccessControl(t *testing.T) {
	srv, _, prj1, _ := setupT3T4TestServer(t)

	// Cast a vote to generate an audit event
	reqVote := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/projects/%s/vote", prj1), nil)
	reqVote.Header.Set("Cookie", "session=prt_2e88")
	_ = executeRequest(srv, reqVote)

	// Participant attempting to access audit log -> must be 403 Forbidden
	reqPart := httptest.NewRequest("GET", "/api/v1/community/audit", nil)
	reqPart.Header.Set("Cookie", "session=prt_2e88")
	respPart := executeRequest(srv, reqPart)
	if respPart.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for participant accessing audit trail, got %d", respPart.Code)
	}

	// Organizer accessing audit log -> must be 200 OK
	reqOrg := httptest.NewRequest("GET", "/api/v1/community/audit", nil)
	reqOrg.Header.Set("Cookie", "session=org_7f2a")
	respOrg := executeRequest(srv, reqOrg)
	if respOrg.Code != http.StatusOK {
		t.Fatalf("expected 200 for organizer accessing audit trail, got %d: %s", respOrg.Code, respOrg.Body.String())
	}
	var res map[string]any
	_ = json.Unmarshal(respOrg.Body.Bytes(), &res)
	if fmt.Sprintf("%v", res["count"]) != "1" {
		t.Fatalf("expected count=1 audit event, got %v", res["count"])
	}
}

// -------------------------------------------------------------------------
// T4.3 Verifiable Certificates & Judge Records Tests
// -------------------------------------------------------------------------

func TestT4_VerifiableCertificates(t *testing.T) {
	srv, _, prj1, _ := setupT3T4TestServer(t)

	// 1. Get certificate API
	reqCert := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/projects/%s/certificate", prj1), nil)
	respCert := executeRequest(srv, reqCert)
	if respCert.Code != http.StatusOK {
		t.Fatalf("expected 200 for certificate, got %d: %s", respCert.Code, respCert.Body.String())
	}
	var cert CertificateData
	_ = json.Unmarshal(respCert.Body.Bytes(), &cert)
	if cert.Status != "ISSUED" || cert.DigestSHA256 == "" {
		t.Fatalf("invalid certificate data: %+v", cert)
	}

	// 2. Verify certificate with valid digest
	reqVerifyValid := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/projects/%s/certificate/verify?digest=%s", prj1, cert.DigestSHA256), nil)
	respVerifyValid := executeRequest(srv, reqVerifyValid)
	if respVerifyValid.Code != http.StatusOK {
		t.Fatalf("expected 200 for certificate verification, got %d", respVerifyValid.Code)
	}
	var vRes map[string]any
	_ = json.Unmarshal(respVerifyValid.Body.Bytes(), &vRes)
	if vRes["verified"] != true {
		t.Fatalf("expected verified=true, got %v", vRes["verified"])
	}

	// 3. Verify certificate with tampered digest
	reqVerifyTampered := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/projects/%s/certificate/verify?digest=tampered_digest_123", prj1), nil)
	respVerifyTampered := executeRequest(srv, reqVerifyTampered)
	var tRes map[string]any
	_ = json.Unmarshal(respVerifyTampered.Body.Bytes(), &tRes)
	if tRes["verified"] != false {
		t.Fatalf("expected verified=false for tampered digest, got %v", tRes["verified"])
	}

	// 4. HTML certificate view
	reqHTML := httptest.NewRequest("GET", fmt.Sprintf("/projects/%s/certificate", prj1), nil)
	respHTML := executeRequest(srv, reqHTML)
	if respHTML.Code != http.StatusOK || !strings.Contains(respHTML.Body.String(), "Certificate of Completion") {
		t.Fatalf("expected 200 with HTML certificate, got %d", respHTML.Code)
	}
}

func TestT4_JudgeVerifiableRecord_RoleIsolation(t *testing.T) {
	srv, _, _, _ := setupT3T4TestServer(t)

	// Judge Alpha (jdg_01) accesses own record -> 200 OK
	reqOwn := httptest.NewRequest("GET", "/api/v1/judges/jdg_01/record", nil)
	reqOwn.Header.Set("Cookie", "session=jdg_a_91bc")
	respOwn := executeRequest(srv, reqOwn)
	if respOwn.Code != http.StatusOK {
		t.Fatalf("expected 200 for judge querying own record, got %d: %s", respOwn.Code, respOwn.Body.String())
	}

	// Judge Beta (jdg_02) attempts to inspect Judge Alpha's record -> MUST be 403 Forbidden (T2 invariant preserved!)
	reqPeer := httptest.NewRequest("GET", "/api/v1/judges/jdg_01/record", nil)
	reqPeer.Header.Set("Cookie", "session=jdg_b_44de")
	respPeer := executeRequest(srv, reqPeer)
	if respPeer.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for peer judge inspecting record, got %d", respPeer.Code)
	}

	// Organizer can inspect judge record -> 200 OK
	reqOrg := httptest.NewRequest("GET", "/api/v1/judges/jdg_01/record", nil)
	reqOrg.Header.Set("Cookie", "session=org_7f2a")
	respOrg := executeRequest(srv, reqOrg)
	if respOrg.Code != http.StatusOK {
		t.Fatalf("expected 200 for organizer inspecting record, got %d", respOrg.Code)
	}
}

// -------------------------------------------------------------------------
// T4.2 Webhooks CRUD Test
// -------------------------------------------------------------------------

func TestT4_Webhooks_CRUD(t *testing.T) {
	srv, _, _, _ := setupT3T4TestServer(t)

	// Participant blocked from creating webhook -> 403 Forbidden
	reqPart := httptest.NewRequest("POST", "/api/v1/webhooks", strings.NewReader(`{"target_url": "https://example.com/wh"}`))
	reqPart.Header.Set("Cookie", "session=prt_2e88")
	respPart := executeRequest(srv, reqPart)
	if respPart.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for participant registering webhook, got %d", respPart.Code)
	}

	// Organizer registers webhook -> 201 Created
	whPayload := `{"target_url": "https://hooks.example.com/dogfood", "event_type": "vote.cast", "secret": "whsec_test123"}`
	reqOrg := httptest.NewRequest("POST", "/api/v1/webhooks", strings.NewReader(whPayload))
	reqOrg.Header.Set("Cookie", "session=org_7f2a")
	respOrg := executeRequest(srv, reqOrg)
	if respOrg.Code != http.StatusCreated {
		t.Fatalf("expected 201 for organizer registering webhook, got %d: %s", respOrg.Code, respOrg.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(respOrg.Body.Bytes(), &created)
	subID := fmt.Sprintf("%v", created["id"])

	// Organizer lists webhooks -> 200 OK
	reqList := httptest.NewRequest("GET", "/api/v1/webhooks", nil)
	reqList.Header.Set("Cookie", "session=org_7f2a")
	respList := executeRequest(srv, reqList)
	if respList.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", respList.Code)
	}

	// Organizer deletes webhook -> 200 OK
	reqDel := httptest.NewRequest("DELETE", fmt.Sprintf("/api/v1/webhooks/%s", subID), nil)
	reqDel.Header.Set("Cookie", "session=org_7f2a")
	respDel := executeRequest(srv, reqDel)
	if respDel.Code != http.StatusOK {
		t.Fatalf("expected 200 for webhook delete, got %d", respDel.Code)
	}
}

// -------------------------------------------------------------------------
// T4.5 Embeddable Gallery & Card Widgets Test
// -------------------------------------------------------------------------

func TestT4_EmbeddableGalleryAndCard(t *testing.T) {
	srv, _, prj1, _ := setupT3T4TestServer(t)

	// Embed gallery
	reqGal := httptest.NewRequest("GET", "/projects/embed", nil)
	respGal := executeRequest(srv, reqGal)
	if respGal.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", respGal.Code)
	}
	if respGal.Header().Get("Content-Security-Policy") != "frame-ancestors *" {
		t.Errorf("expected frame-ancestors * header on embed gallery")
	}

	// Embed single project card
	reqCard := httptest.NewRequest("GET", fmt.Sprintf("/projects/%s/embed", prj1), nil)
	respCard := executeRequest(srv, reqCard)
	if respCard.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", respCard.Code)
	}
	if !strings.Contains(respCard.Body.String(), "Glass Signal") {
		t.Fatalf("expected fixture project title in embed card body")
	}
}

// -------------------------------------------------------------------------
// T4.6 Bulk JSON Export Test
// -------------------------------------------------------------------------

func TestT4_BulkJSONExport_AuthorizationAndManifest(t *testing.T) {
	srv, _, _, _ := setupT3T4TestServer(t)

	// Participant blocked -> 403 Forbidden
	reqPart := httptest.NewRequest("GET", "/api/v1/export.json", nil)
	reqPart.Header.Set("Cookie", "session=prt_2e88")
	respPart := executeRequest(srv, reqPart)
	if respPart.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for participant exporting full JSON, got %d", respPart.Code)
	}

	// Organizer export -> 200 OK with manifest_sha256
	reqOrg := httptest.NewRequest("GET", "/api/v1/export.json", nil)
	reqOrg.Header.Set("Cookie", "session=org_7f2a")
	respOrg := executeRequest(srv, reqOrg)
	if respOrg.Code != http.StatusOK {
		t.Fatalf("expected 200 for organizer export, got %d: %s", respOrg.Code, respOrg.Body.String())
	}
	var exportData map[string]any
	_ = json.Unmarshal(respOrg.Body.Bytes(), &exportData)
	if exportData["export_type"] != "DOGFOOD_FULL_JSON_BUNDLE" {
		t.Fatalf("unexpected export type: %v", exportData["export_type"])
	}
	if exportData["manifest_sha256"] == "" {
		t.Fatalf("expected cryptographic manifest_sha256 in export")
	}
}
