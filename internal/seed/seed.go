package seed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// CanonicalFixtureSHA256 is the frozen canonical SHA-256 of official/fixtures.json.
const CanonicalFixtureSHA256 = "252896bc45d49fca69ad413be40c6bfde9d9b9f9dd8db702b3ff74eaaa181121"

// CanonicalBytes normalizes line endings to LF to prevent Windows/Git CRLF drift from altering provenance.
func CanonicalBytes(data []byte) []byte {
	return bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
}

// Fixture represents the top-level structure of official/fixtures.json.
type Fixture struct {
	Event    FixtureEvent     `json:"event"`
	Tracks   []FixtureTrack   `json:"tracks"`
	Judges   []FixtureJudge   `json:"judges"`
	Teams    []FixtureTeam    `json:"teams"`
	Projects []FixtureProject `json:"projects"`
	Scores   []json.RawMessage `json:"scores"` // Deferred to T2
}

type FixtureEvent struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	SubmissionsClose string `json:"submissions_close"`
}

type FixtureTrack struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type FixtureJudge struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Email  string   `json:"email"`
	Tracks []string `json:"tracks"` // Deferred to T2 (judge_track_eligibility)
}

type FixtureTeam struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

type FixtureProject struct {
	ID          string `json:"id"`
	Team        string `json:"team"`
	Track       string `json:"track"`
	Title       string `json:"title"`
	Summary     string `json:"summary"`
	RepoURL     string `json:"repo_url"`
	SubmittedAt string `json:"submitted_at"`
}

// Result records summary counts of what was seeded.
type Result struct {
	AlreadySeeded   bool
	EventID         string
	TracksCount     int
	JudgesCount     int
	MembersCount    int
	TeamsCount      int
	ProjectsCount   int
	SubmissionsCount int
	SourceHash      string
}

// HashBytes computes the lowercase hex-encoded SHA-256 of data.
func HashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// HashString computes the lowercase hex-encoded SHA-256 of string s.
func HashString(s string) string {
	return HashBytes([]byte(s))
}

// Run attempts to locate and seed the fixture file from configured paths.
func Run(ctx context.Context, db *sql.DB, explicitPath string) (*Result, error) {
	paths := []string{explicitPath}
	if explicitPath == "" {
		if envPath := os.Getenv("FIXTURE_PATH"); envPath != "" {
			paths = append(paths, envPath)
		}
		paths = append(paths, "official/fixtures.json", "/official/fixtures.json", "/fixtures.json", "fixtures.json")
	}

	var foundPath string
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			foundPath = p
			break
		}
	}

	if foundPath == "" {
		return nil, fmt.Errorf("no fixture file found in candidate paths: %v", paths)
	}

	data, err := os.ReadFile(foundPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read fixture file %s: %w", foundPath, err)
	}

	return Load(ctx, db, foundPath, data)
}

// Load loads fixture bytes into the database idempotently and transactionally.
func Load(ctx context.Context, db *sql.DB, sourceName string, data []byte) (*Result, error) {
	if len(data) == 0 {
		return nil, errors.New("empty fixture data provided")
	}

	canonicalData := CanonicalBytes(data)
	hash := HashBytes(canonicalData)

	// Check if this exact source + hash has already been successfully seeded.
	var existingStatus string
	var existingEventID sql.NullString
	err := db.QueryRowContext(ctx,
		"SELECT status, event_id FROM seed_imports WHERE source_name = ? AND source_hash_sha256 = ?;",
		sourceName, hash,
	).Scan(&existingStatus, &existingEventID)

	if err == nil {
		if existingStatus == "COMPLETED" {
			// Idempotent: already seeded successfully
			return &Result{
				AlreadySeeded: true,
				EventID:       existingEventID.String,
				SourceHash:    hash,
			}, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("failed to query seed_imports: %w", err)
	}

	// Verify that a different hash has not already been seeded on this database.
	var anyCompletedCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM seed_imports WHERE status = 'COMPLETED';").Scan(&anyCompletedCount); err != nil {
		return nil, fmt.Errorf("failed to check existing completed seed imports: %w", err)
	}
	if anyCompletedCount > 0 {
		return nil, fmt.Errorf("refusing to seed: database already seeded with a different fixture version")
	}

	var fixture Fixture
	if err := json.Unmarshal(canonicalData, &fixture); err != nil {
		return nil, fmt.Errorf("failed to parse fixture json: %w", err)
	}

	if fixture.Event.ID == "" {
		return nil, errors.New("invalid fixture: event.id is required")
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback()

	nowUTC := time.Now().UTC().Format(time.RFC3339)
	seedImportID := fmt.Sprintf("seed_%s_%s", fixture.Event.ID, hash[:8])

	// 1. Insert Event (independent table, no FKs)
	subClose := fixture.Event.SubmissionsClose
	subOpen := "2026-02-01T00:00:00Z"
	if subClose == "" {
		subClose = "2026-03-01T18:00:00Z"
	}

	insertEventSQL := `
	INSERT INTO events (
		id, name, registration_opens_at, registration_closes_at,
		submissions_open_at, submissions_close_at,
		judging_opens_at, judging_closes_at,
		default_expected_reviews, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`
	if _, err := tx.ExecContext(ctx, insertEventSQL,
		fixture.Event.ID,
		fixture.Event.Name,
		"2026-02-01T00:00:00Z", subClose,
		subOpen, subClose,
		subClose, "2026-03-03T18:00:00Z",
		3, "2026-02-01T00:00:00Z", "2026-02-01T00:00:00Z",
	); err != nil {
		return nil, fmt.Errorf("failed to insert event %s: %w", fixture.Event.ID, err)
	}

	// 2. Record seed import STARTED
	insertSeedSQL := `
	INSERT INTO seed_imports (id, source_name, source_hash_sha256, event_id, status, started_at)
	VALUES (?, ?, ?, ?, 'STARTED', ?);`
	if _, err := tx.ExecContext(ctx, insertSeedSQL, seedImportID, sourceName, hash, fixture.Event.ID, nowUTC); err != nil {
		return nil, fmt.Errorf("failed to record seed_imports start: %w", err)
	}

	// 2. Insert Tracks
	insertTrackSQL := `INSERT INTO tracks (id, event_id, name, created_at) VALUES (?, ?, ?, ?);`
	trackStmt, err := tx.PrepareContext(ctx, insertTrackSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare track stmt: %w", err)
	}
	defer trackStmt.Close()

	for _, trk := range fixture.Tracks {
		if _, err := trackStmt.ExecContext(ctx, trk.ID, fixture.Event.ID, trk.Name, "2026-02-01T00:00:00Z"); err != nil {
			return nil, fmt.Errorf("failed to insert track %s: %w", trk.ID, err)
		}
	}

	// 3. Insert Users & Event Roles
	insertUserSQL := `
	INSERT INTO users (id, email_normalized, display_name, password_hash, password_params, created_at)
	VALUES (?, ?, ?, ?, ?, ?);`
	userStmt, err := tx.PrepareContext(ctx, insertUserSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare user stmt: %w", err)
	}
	defer userStmt.Close()

	insertRoleSQL := `INSERT INTO event_roles (event_id, user_id, role, granted_at) VALUES (?, ?, ?, ?);`
	roleStmt, err := tx.PrepareContext(ctx, insertRoleSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare role stmt: %w", err)
	}
	defer roleStmt.Close()

	// Track seen emails to prevent duplicate user insertion
	emailToUserID := make(map[string]string)

	// 3a. Insert Judges
	for _, jdg := range fixture.Judges {
		normEmail := strings.ToLower(strings.TrimSpace(jdg.Email))
		if _, err := userStmt.ExecContext(ctx, jdg.ID, normEmail, jdg.Name, "", "", "2026-02-01T00:00:00Z"); err != nil {
			return nil, fmt.Errorf("failed to insert judge user %s: %w", jdg.ID, err)
		}
		emailToUserID[normEmail] = jdg.ID

		if _, err := roleStmt.ExecContext(ctx, fixture.Event.ID, jdg.ID, "judge", "2026-02-01T00:00:00Z"); err != nil {
			return nil, fmt.Errorf("failed to assign judge role to %s: %w", jdg.ID, err)
		}
	}

	// 3b. Insert Team Members
	for _, tm := range fixture.Teams {
		for _, memberEmail := range tm.Members {
			normEmail := strings.ToLower(strings.TrimSpace(memberEmail))
			if _, exists := emailToUserID[normEmail]; exists {
				continue
			}
			userID := fmt.Sprintf("usr_%s", HashString(normEmail)[:12])
			displayName := strings.Split(normEmail, "@")[0]

			if _, err := userStmt.ExecContext(ctx, userID, normEmail, displayName, "", "", "2026-02-01T00:00:00Z"); err != nil {
				return nil, fmt.Errorf("failed to insert member user %s: %w", userID, err)
			}
			emailToUserID[normEmail] = userID

			if _, err := roleStmt.ExecContext(ctx, fixture.Event.ID, userID, "participant", "2026-02-01T00:00:00Z"); err != nil {
				return nil, fmt.Errorf("failed to assign participant role to %s: %w", userID, err)
			}
		}
	}

	// 3c. Insert evaluation organizer account
	organizerID := "usr_organizer"
	organizerEmail := "organizer@example.org"
	if _, exists := emailToUserID[organizerEmail]; !exists {
		if _, err := userStmt.ExecContext(ctx, organizerID, organizerEmail, "Organizer", "", "", "2026-02-01T00:00:00Z"); err != nil {
			return nil, fmt.Errorf("failed to insert organizer user: %w", err)
		}
		emailToUserID[organizerEmail] = organizerID
		if _, err := roleStmt.ExecContext(ctx, fixture.Event.ID, organizerID, "organizer", "2026-02-01T00:00:00Z"); err != nil {
			return nil, fmt.Errorf("failed to assign organizer role: %w", err)
		}
		if _, err := roleStmt.ExecContext(ctx, fixture.Event.ID, organizerID, "admin", "2026-02-01T00:00:00Z"); err != nil {
			return nil, fmt.Errorf("failed to assign admin role: %w", err)
		}
	}

	// 4. Insert Evaluation Sessions (for .dogfood.toml checker auth)
	insertSessionSQL := `
	INSERT INTO sessions (id, user_id, token_hash, created_at, expires_at, last_seen_at)
	VALUES (?, ?, ?, ?, ?, ?);`
	sessionStmt, err := tx.PrepareContext(ctx, insertSessionSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare session stmt: %w", err)
	}
	defer sessionStmt.Close()

	// Pick first participant (member of tm_01)
	participantUserID := ""
	if len(fixture.Teams) > 0 && len(fixture.Teams[0].Members) > 0 {
		participantUserID = emailToUserID[strings.ToLower(strings.TrimSpace(fixture.Teams[0].Members[0]))]
	}

	evalSessions := []struct {
		id     string
		userID string
		token  string
	}{
		{"ses_org", organizerID, "org_7f2a"},
		{"ses_jdg_a", "jdg_01", "jdg_a_91bc"},
		{"ses_jdg_b", "jdg_02", "jdg_b_44de"},
		{"ses_prt", participantUserID, "prt_2e88"},
	}

	farFuture := "2035-01-01T00:00:00Z"
	for _, es := range evalSessions {
		if es.userID == "" {
			continue
		}
		tokenHash := HashString(es.token)
		if _, err := sessionStmt.ExecContext(ctx, es.id, es.userID, tokenHash, "2026-02-01T00:00:00Z", farFuture, "2026-02-01T00:00:00Z"); err != nil {
			return nil, fmt.Errorf("failed to insert evaluation session %s: %w", es.id, err)
		}
	}

	// 5. Insert Teams and Memberships
	insertTeamSQL := `INSERT INTO teams (id, event_id, name, created_by, created_at) VALUES (?, ?, ?, ?, ?);`
	teamStmt, err := tx.PrepareContext(ctx, insertTeamSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare team stmt: %w", err)
	}
	defer teamStmt.Close()

	insertMembershipSQL := `
	INSERT INTO team_memberships (id, event_id, team_id, user_id, membership_role, joined_at)
	VALUES (?, ?, ?, ?, ?, ?);`
	membershipStmt, err := tx.PrepareContext(ctx, insertMembershipSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare membership stmt: %w", err)
	}
	defer membershipStmt.Close()

	for _, tm := range fixture.Teams {
		var creatorUserID *string
		if len(tm.Members) > 0 {
			firstMemberID := emailToUserID[strings.ToLower(strings.TrimSpace(tm.Members[0]))]
			creatorUserID = &firstMemberID
		}

		if _, err := teamStmt.ExecContext(ctx, tm.ID, fixture.Event.ID, tm.Name, creatorUserID, "2026-02-01T00:00:00Z"); err != nil {
			return nil, fmt.Errorf("failed to insert team %s: %w", tm.ID, err)
		}

		for idx, memberEmail := range tm.Members {
			normEmail := strings.ToLower(strings.TrimSpace(memberEmail))
			userID := emailToUserID[normEmail]
			role := "member"
			if idx == 0 {
				role = "owner"
			}
			membershipID := fmt.Sprintf("tmm_%s_%02d", tm.ID, idx+1)
			if _, err := membershipStmt.ExecContext(ctx, membershipID, fixture.Event.ID, tm.ID, userID, role, "2026-02-01T00:00:00Z"); err != nil {
				return nil, fmt.Errorf("failed to insert membership for %s on %s: %w", userID, tm.ID, err)
			}
		}
	}

	// 6. Insert Projects and Submissions
	insertProjectSQL := `
	INSERT INTO projects (
		id, event_id, team_id, track_id, created_by,
		eligibility_status, duplicate_status, created_at
	) VALUES (?, ?, ?, ?, ?, 'ELIGIBLE', 'NONE', ?);`
	projectStmt, err := tx.PrepareContext(ctx, insertProjectSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare project stmt: %w", err)
	}
	defer projectStmt.Close()

	insertSubmissionSQL := `
	INSERT INTO submissions (
		id, event_id, project_id, version_no, state,
		title, summary, repo_url, created_at, submitted_at
	) VALUES (?, ?, ?, 1, 'SUBMITTED', ?, ?, ?, ?, ?);`
	subStmt, err := tx.PrepareContext(ctx, insertSubmissionSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare submission stmt: %w", err)
	}
	defer subStmt.Close()

	for _, prj := range fixture.Projects {
		subTime := prj.SubmittedAt
		if subTime == "" {
			subTime = "2026-02-27T00:00:00Z"
		}

		if _, err := projectStmt.ExecContext(ctx,
			prj.ID, fixture.Event.ID, prj.Team, prj.Track, nil, subTime,
		); err != nil {
			return nil, fmt.Errorf("failed to insert project %s: %w", prj.ID, err)
		}

		subID := fmt.Sprintf("sub_%s", prj.ID)
		if _, err := subStmt.ExecContext(ctx,
			subID, fixture.Event.ID, prj.ID, prj.Title, prj.Summary, prj.RepoURL, subTime, subTime,
		); err != nil {
			return nil, fmt.Errorf("failed to insert submission for project %s: %w", prj.ID, err)
		}
	}

	// 7. Update seed_imports status to COMPLETED
	updateSeedSQL := `UPDATE seed_imports SET status = 'COMPLETED', completed_at = ? WHERE id = ?;`
	completedAt := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, updateSeedSQL, completedAt, seedImportID); err != nil {
		return nil, fmt.Errorf("failed to mark seed_imports completed: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit seed transaction: %w", err)
	}

	return &Result{
		AlreadySeeded:    false,
		EventID:          fixture.Event.ID,
		TracksCount:      len(fixture.Tracks),
		JudgesCount:      len(fixture.Judges),
		MembersCount:     len(emailToUserID) - len(fixture.Judges) - 1, // minus judges and organizer
		TeamsCount:       len(fixture.Teams),
		ProjectsCount:    len(fixture.Projects),
		SubmissionsCount: len(fixture.Projects),
		SourceHash:       hash,
	}, nil
}
