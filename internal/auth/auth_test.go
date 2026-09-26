package auth

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"dogfood/internal/migrations"
	"dogfood/internal/seed"
	_ "modernc.org/sqlite"
)

func setupAuthTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open in-memory db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
		t.Fatalf("failed to enable foreign_keys: %v", err)
	}
	if err := migrations.Run(ctx, db); err != nil {
		t.Fatalf("migrations.Run failed: %v", err)
	}
	return db
}

func TestAuthenticate_SeededTokens(t *testing.T) {
	db := setupAuthTestDB(t)
	ctx := context.Background()

	rawFixture := readFixtures(t)
	if _, err := seed.Load(ctx, db, "official/fixtures.json", rawFixture); err != nil {
		t.Fatalf("seed.Load: %v", err)
	}

	// 1. Test Organizer
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Cookie", "session=org_7f2a")
	id, err := Authenticate(ctx, db, req)
	if err != nil {
		t.Fatalf("authenticate organizer failed: %v", err)
	}
	if !id.HasRole("organizer") {
		t.Errorf("expected organizer role, got roles: %v", id.Roles)
	}

	// 2. Test Judge A
	reqJudge := httptest.NewRequest(http.MethodGet, "/test", nil)
	reqJudge.Header.Set("Cookie", "session=jdg_a_91bc")
	idJudge, err := Authenticate(ctx, db, reqJudge)
	if err != nil {
		t.Fatalf("authenticate judge_a failed: %v", err)
	}
	if idJudge.UserID != "jdg_01" {
		t.Errorf("expected judge_a UserID 'jdg_01', got '%s'", idJudge.UserID)
	}
	if !idJudge.HasRole("judge") {
		t.Errorf("expected judge role, got roles: %v", idJudge.Roles)
	}

	// 3. Test Invalid Token
	reqInvalid := httptest.NewRequest(http.MethodGet, "/test", nil)
	reqInvalid.Header.Set("Cookie", "session=invalid_token")
	_, err = Authenticate(ctx, db, reqInvalid)
	if err == nil {
		t.Errorf("expected error for invalid session token, got nil")
	}
}

func TestLoginUser_AndRevoke(t *testing.T) {
	db := setupAuthTestDB(t)
	ctx := context.Background()

	rawFixture := readFixtures(t)
	if _, err := seed.Load(ctx, db, "official/fixtures.json", rawFixture); err != nil {
		t.Fatalf("seed.Load: %v", err)
	}

	// Login existing fixture user (Tomas Varga)
	token, id, err := LoginUser(ctx, db, "tomas.varga@example.org", 24*time.Hour)
	if err != nil {
		t.Fatalf("LoginUser failed: %v", err)
	}
	if id.UserID != "jdg_01" {
		t.Errorf("expected user id 'jdg_01', got '%s'", id.UserID)
	}
	if !id.HasRole("judge") {
		t.Errorf("expected judge role, got %v", id.Roles)
	}

	// Verify authenticated with new token
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.Header.Set("Cookie", "session="+token)
	authID, err := Authenticate(ctx, db, req)
	if err != nil {
		t.Fatalf("Authenticate with created token failed: %v", err)
	}
	if authID.UserID != "jdg_01" {
		t.Errorf("expected user id 'jdg_01', got '%s'", authID.UserID)
	}

	// Revoke session
	if err := RevokeSession(ctx, db, token); err != nil {
		t.Fatalf("RevokeSession failed: %v", err)
	}

	// Verify no longer authenticated
	_, err = Authenticate(ctx, db, req)
	if err == nil {
		t.Errorf("expected authentication failure after revocation, got success")
	}
}

func readFixtures(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("../../official/fixtures.json")
	if err != nil {
		t.Fatalf("failed to read fixtures: %v", err)
	}
	return b
}
