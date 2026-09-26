package seed

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"dogfood/internal/migrations"
	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
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

func readOfficialFixtures(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "official", "fixtures.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read official fixtures at %s: %v", path, err)
	}
	return data
}

func TestSeed_OfficialFixtures(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	data := readOfficialFixtures(t)

	// 1. Initial Seed
	res, err := Load(ctx, db, "official/fixtures.json", data)
	if err != nil {
		t.Fatalf("initial seed.Load failed: %v", err)
	}
	if res.AlreadySeeded {
		t.Errorf("expected AlreadySeeded = false on initial seed")
	}

	// 2. Verify Exact Entity Counts
	checkCount := func(table string, expected int) {
		var cnt int
		err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+";").Scan(&cnt)
		if err != nil {
			t.Fatalf("failed to query count for %s: %v", table, err)
		}
		if cnt != expected {
			t.Errorf("table %s: expected %d records, got %d", table, expected, cnt)
		}
	}

	checkCount("events", 1)
	checkCount("tracks", 8)
	checkCount("teams", 40)
	checkCount("projects", 41)
	checkCount("submissions", 41)
	checkCount("team_memberships", 91)
	checkCount("users", 122) // 30 judges + 91 unique team members + 1 evaluation organizer
	checkCount("sessions", 4) // 4 evaluation sessions: org, jdg_a, jdg_b, prt
	checkCount("seed_imports", 1)

	// 3. Verify Specific Known Fixture Entities & IDs
	var eventName string
	err = db.QueryRowContext(ctx, "SELECT name FROM events WHERE id = 'evt_01';").Scan(&eventName)
	if err != nil {
		t.Fatalf("failed to query evt_01: %v", err)
	}
	if eventName != "Sample Hack 2026" {
		t.Errorf("expected event name 'Sample Hack 2026', got '%s'", eventName)
	}

	// 4. Verify Duplicate Projects on tm_07 are both preserved
	var tm07Projects int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM projects WHERE team_id = 'tm_07';").Scan(&tm07Projects)
	if err != nil {
		t.Fatalf("failed to query tm_07 projects: %v", err)
	}
	if tm07Projects != 2 {
		t.Errorf("expected 2 distinct projects for team tm_07, got %d", tm07Projects)
	}

	var prj07Title, prj41Title string
	if err := db.QueryRowContext(ctx, "SELECT title FROM submissions WHERE project_id = 'prj_07';").Scan(&prj07Title); err != nil {
		t.Fatalf("prj_07 submission missing: %v", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT title FROM submissions WHERE project_id = 'prj_41';").Scan(&prj41Title); err != nil {
		t.Fatalf("prj_41 submission missing: %v", err)
	}
	if prj07Title != "Dry Harbour" || prj41Title != "Dry Harbour" {
		t.Errorf("unexpected titles for duplicate candidate projects: prj_07=%s, prj_41=%s", prj07Title, prj41Title)
	}

	// 5. Verify Idempotent Restart
	res2, err := Load(ctx, db, "official/fixtures.json", data)
	if err != nil {
		t.Fatalf("second seed.Load failed: %v", err)
	}
	if !res2.AlreadySeeded {
		t.Errorf("expected AlreadySeeded = true on second seed")
	}

	// Verify counts unchanged after restart
	checkCount("events", 1)
	checkCount("tracks", 8)
	checkCount("teams", 40)
	checkCount("projects", 41)
	checkCount("submissions", 41)
	checkCount("team_memberships", 91)
	checkCount("users", 122)
	checkCount("sessions", 4)
	checkCount("seed_imports", 1)

	// 6. Verify seed_imports provenance record matches frozen canonical hash
	var importStatus, sourceHash string
	err = db.QueryRowContext(ctx, "SELECT status, source_hash_sha256 FROM seed_imports WHERE id = ?;", "seed_evt_01_"+CanonicalFixtureSHA256[:8]).Scan(&importStatus, &sourceHash)
	if err != nil {
		t.Fatalf("failed to query seed_imports record: %v", err)
	}
	if importStatus != "COMPLETED" {
		t.Errorf("expected seed_imports status 'COMPLETED', got '%s'", importStatus)
	}
	if sourceHash != CanonicalFixtureSHA256 {
		t.Errorf("expected canonical source hash '%s', got '%s'", CanonicalFixtureSHA256, sourceHash)
	}

	// 7. Verify Changed Fixture on Seeded DB is Refused
	mutatedData := append([]byte(" "), data...)
	_, err = Load(ctx, db, "official/fixtures.json", mutatedData)
	if err == nil {
		t.Errorf("expected error when trying to seed different fixture on already seeded DB, got nil")
	}
}

func TestSeed_MalformedData(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Empty data
	_, err := Load(ctx, db, "test.json", []byte{})
	if err == nil {
		t.Errorf("expected error on empty data, got nil")
	}

	// Invalid JSON
	_, err = Load(ctx, db, "test.json", []byte("{invalid"))
	if err == nil {
		t.Errorf("expected error on invalid JSON, got nil")
	}

	// Missing event ID
	_, err = Load(ctx, db, "test.json", []byte(`{"event": {"name": "No ID"}}`))
	if err == nil {
		t.Errorf("expected error on missing event ID, got nil")
	}
}

func TestSeed_CRLFResilience(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()
	rawLF := readOfficialFixtures(t)

	// Artificially convert LF to CRLF
	crlfData := bytes.ReplaceAll(rawLF, []byte("\n"), []byte("\r\n"))

	res, err := Load(ctx, db, "official/fixtures.json", crlfData)
	if err != nil {
		t.Fatalf("Load with CRLF data failed: %v", err)
	}

	if res.SourceHash != CanonicalFixtureSHA256 {
		t.Errorf("expected canonical hash %s even with CRLF data, got %s", CanonicalFixtureSHA256, res.SourceHash)
	}

	var importHash string
	err = db.QueryRowContext(ctx, "SELECT source_hash_sha256 FROM seed_imports WHERE id = ?;", "seed_evt_01_"+CanonicalFixtureSHA256[:8]).Scan(&importHash)
	if err != nil {
		t.Fatalf("failed to query seed_imports: %v", err)
	}
	if importHash != CanonicalFixtureSHA256 {
		t.Errorf("seed_imports recorded '%s', wanted canonical '%s'", importHash, CanonicalFixtureSHA256)
	}
}

