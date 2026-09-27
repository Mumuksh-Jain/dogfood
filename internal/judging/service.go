package judging

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

var (
	ErrUnauthorized      = errors.New("unauthorized: authentication required")
	ErrForbidden         = errors.New("forbidden: access denied for this resource")
	ErrNotFound          = errors.New("not found: requested resource does not exist")
	ErrBallotSubmitted   = errors.New("conflict: ballot has already been submitted and cannot be modified")
	ErrInvalidCriterion = errors.New("validation: invalid criterion definition")
	ErrMissingCriterion = errors.New("validation: required criterion score is missing")
	ErrScoreOutOfBounds = errors.New("validation: score is out of allowed bounds")
	ErrRubricPublished  = errors.New("conflict: published rubric version is immutable")
	ErrRubricInUse      = errors.New("conflict: rubric version is in use by submitted ballots and cannot be modified")
)

// Criterion defines an individual scoring dimension.
type Criterion struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	MinScore    float64 `json:"min_score"`
	MaxScore    float64 `json:"max_score"`
	Weight      float64 `json:"weight"`
	Required    bool    `json:"required"`
}

// RubricVersion represents a versioned criteria configuration.
type RubricVersion struct {
	ID                string      `json:"id"`
	EventID           string      `json:"event_id"`
	RubricID          string      `json:"rubric_id"`
	TrackID           string      `json:"track_id,omitempty"`
	VersionNo         int         `json:"version_no"`
	State             string      `json:"state"` // DRAFT, PUBLISHED, RETIRED
	Criteria          []Criterion `json:"criteria"`
	ConfigurationHash string      `json:"configuration_hash"`
	CreatedBy         string      `json:"created_by,omitempty"`
	CreatedAt         string      `json:"created_at"`
	PublishedAt       string      `json:"published_at,omitempty"`
}

// BallotVersion represents an immutable revision in an evaluation lifecycle.
type BallotVersion struct {
	ID                  string             `json:"id"`
	AssignmentID        string             `json:"assignment_id"`
	JudgeUserID         string             `json:"judge_user_id"`
	ProjectID           string             `json:"project_id"`
	RubricVersionID     string             `json:"rubric_version_id"`
	VersionNo           int                `json:"version_no"`
	SaveKind            string             `json:"save_kind"` // DRAFT or SUBMISSION
	CriteriaScores      map[string]float64 `json:"criteria_scores"`
	TotalScore          float64            `json:"total_score"`
	WeightedScore       float64            `json:"weighted_score"`
	Comment             string             `json:"comment"`
	CreatedBy           string             `json:"created_by,omitempty"`
	CreatedAt           string             `json:"created_at"`
	SupersedesVersionID string             `json:"supersedes_version_id,omitempty"`
}

// AssignmentDetail encapsulates full context for a judge evaluating a project.
type AssignmentDetail struct {
	AssignmentID    string         `json:"assignment_id"`
	EventID         string         `json:"event_id"`
	JudgeUserID     string         `json:"judge_user_id"`
	ProjectID       string         `json:"project_id"`
	ProjectTitle    string         `json:"project_title"`
	ProjectSummary  string         `json:"project_summary"`
	TeamName        string         `json:"team_name"`
	TrackID         string         `json:"track_id"`
	TrackName       string         `json:"track_name"`
	RepoURL         string         `json:"repo_url"`
	DemoURL         string         `json:"demo_url"`
	AssignmentStatus string        `json:"assignment_status"`
	Rubric          *RubricVersion `json:"rubric"`
	LatestBallot    *BallotVersion `json:"latest_ballot,omitempty"`
	IsSubmitted     bool           `json:"is_submitted"`
}

// Service manages rubric versions and ballot evaluations.
type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ComputeRubricHash deterministically computes the SHA-256 hash of criteria JSON.
func ComputeRubricHash(criteria []Criterion) (string, string, error) {
	sortedCriteria := make([]Criterion, len(criteria))
	copy(sortedCriteria, criteria)
	sort.Slice(sortedCriteria, func(i, j int) bool {
		return sortedCriteria[i].ID < sortedCriteria[j].ID
	})

	bytes, err := json.Marshal(sortedCriteria)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal criteria: %w", err)
	}

	h := sha256.Sum256(bytes)
	return hex.EncodeToString(h[:]), string(bytes), nil
}

// ValidateCriteria ensures criteria definitions adhere to frozen scoring constraints.
func ValidateCriteria(criteria []Criterion) error {
	if len(criteria) == 0 {
		return fmt.Errorf("%w: rubric must contain at least one criterion", ErrInvalidCriterion)
	}

	seenIDs := make(map[string]bool)
	totalWeight := 0.0

	for _, c := range criteria {
		if strings.TrimSpace(c.ID) == "" {
			return fmt.Errorf("%w: criterion id cannot be empty", ErrInvalidCriterion)
		}
		if seenIDs[c.ID] {
			return fmt.Errorf("%w: duplicate criterion id %q", ErrInvalidCriterion, c.ID)
		}
		seenIDs[c.ID] = true

		if strings.TrimSpace(c.Name) == "" {
			return fmt.Errorf("%w: criterion name cannot be empty for %s", ErrInvalidCriterion, c.ID)
		}
		if c.MinScore < 0 {
			return fmt.Errorf("%w: min_score cannot be negative for %s", ErrInvalidCriterion, c.ID)
		}
		if c.MaxScore <= c.MinScore {
			return fmt.Errorf("%w: max_score (%v) must be greater than min_score (%v) for %s", ErrInvalidCriterion, c.MaxScore, c.MinScore, c.ID)
		}
		if c.Weight < 0 {
			return fmt.Errorf("%w: weight cannot be negative for %s", ErrInvalidCriterion, c.ID)
		}
		totalWeight += c.Weight
	}

	if totalWeight <= 0 {
		return fmt.Errorf("%w: sum of criterion weights must be strictly positive", ErrInvalidCriterion)
	}

	return nil
}

// CalculateScores computes total score and weighted average from criteria and scores map.
func CalculateScores(criteria []Criterion, scores map[string]float64) (float64, float64) {
	totalScore := 0.0
	weightedSum := 0.0
	sumWeights := 0.0

	critMap := make(map[string]Criterion)
	for _, c := range criteria {
		critMap[c.ID] = c
	}

	for id, score := range scores {
		if c, ok := critMap[id]; ok {
			totalScore += score
			if c.Weight > 0 {
				weightedSum += score * c.Weight
				sumWeights += c.Weight
			}
		}
	}

	weightedScore := 0.0
	if sumWeights > 0 {
		weightedScore = weightedSum / sumWeights
	}

	// Round to 2 decimal places
	totalScore = math.Round(totalScore*100) / 100
	weightedScore = math.Round(weightedScore*100) / 100

	return totalScore, weightedScore
}

// EnsureDefaultRubric creates and publishes the canonical 4-criteria default rubric for an event if none exists.
func (s *Service) EnsureDefaultRubric(ctx context.Context, eventID string, createdBy string) (*RubricVersion, error) {
	existing, err := s.GetPublishedRubric(ctx, eventID, "")
	if err == nil && existing != nil {
		return existing, nil
	}

	defaultCriteria := []Criterion{
		{
			ID:          "functionality",
			Name:        "Functionality & Feature Completeness",
			Description: "How well does the solution work? Are core features robust, stable, and complete?",
			MinScore:    0,
			MaxScore:    5,
			Weight:      1.0,
			Required:    true,
		},
		{
			ID:          "quality",
			Name:        "Code Quality & Technical Design",
			Description: "Software architecture, code cleanliness, test coverage, and documentation.",
			MinScore:    0,
			MaxScore:    5,
			Weight:      1.0,
			Required:    true,
		},
		{
			ID:          "innovation",
			Name:        "Innovation & Creativity",
			Description: "Originality of the concept, technical creativity, and unique problem-solving approach.",
			MinScore:    0,
			MaxScore:    5,
			Weight:      1.0,
			Required:    true,
		},
		{
			ID:          "impact",
			Name:        "Impact & Practical Usability",
			Description: "Practical value, user experience, potential adoption, and real-world usefulness.",
			MinScore:    0,
			MaxScore:    5,
			Weight:      1.0,
			Required:    true,
		},
	}

	return s.CreateAndPublishRubric(ctx, eventID, "default", "", defaultCriteria, createdBy)
}

// CreateAndPublishRubric creates and publishes a rubric version in one atomic step.
func (s *Service) CreateAndPublishRubric(ctx context.Context, eventID, rubricID, trackID string, criteria []Criterion, createdBy string) (*RubricVersion, error) {
	if err := ValidateCriteria(criteria); err != nil {
		return nil, err
	}

	hash, jsonStr, err := ComputeRubricHash(criteria)
	if err != nil {
		return nil, err
	}

	if rubricID == "" {
		rubricID = "default"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Find next version number for (event_id, rubric_id)
	var maxVer int
	_ = tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version_no), 0) FROM rubric_versions 
		WHERE event_id = ? AND rubric_id = ?;
	`, eventID, rubricID).Scan(&maxVer)

	versionNo := maxVer + 1
	nowUTC := time.Now().UTC().Format(time.RFC3339)
	rubricVersionID := fmt.Sprintf("rub_%s_%s_v%d", eventID, rubricID, versionNo)

	var trackParam sql.NullString
	if trackID != "" {
		trackParam = sql.NullString{String: trackID, Valid: true}
	}
	var createdByParam sql.NullString
	if createdBy != "" {
		createdByParam = sql.NullString{String: createdBy, Valid: true}
	}

	insertSQL := `
		INSERT INTO rubric_versions (
			id, event_id, rubric_id, track_id, version_no, state, 
			criteria_json, configuration_hash, created_by, created_at, published_at
		) VALUES (?, ?, ?, ?, ?, 'PUBLISHED', ?, ?, ?, ?, ?);
	`
	_, err = tx.ExecContext(ctx, insertSQL,
		rubricVersionID, eventID, rubricID, trackParam, versionNo,
		jsonStr, hash, createdByParam, nowUTC, nowUTC,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert rubric_version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit rubric creation: %w", err)
	}

	return &RubricVersion{
		ID:                rubricVersionID,
		EventID:           eventID,
		RubricID:          rubricID,
		TrackID:           trackID,
		VersionNo:         versionNo,
		State:             "PUBLISHED",
		Criteria:          criteria,
		ConfigurationHash: hash,
		CreatedBy:         createdBy,
		CreatedAt:         nowUTC,
		PublishedAt:       nowUTC,
	}, nil
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// GetPublishedRubric retrieves the active published rubric for an event and optional track.
func (s *Service) GetPublishedRubric(ctx context.Context, eventID, trackID string) (*RubricVersion, error) {
	return s.getPublishedRubricWith(ctx, s.db, eventID, trackID)
}

func (s *Service) getPublishedRubricWith(ctx context.Context, q queryer, eventID, trackID string) (*RubricVersion, error) {
	// 1. Try track-specific published rubric
	if trackID != "" {
		rv, err := s.queryRubric(ctx, q, `
			SELECT id, event_id, rubric_id, COALESCE(track_id, ''), version_no, state, criteria_json, configuration_hash, COALESCE(created_by, ''), created_at, COALESCE(published_at, '')
			FROM rubric_versions
			WHERE event_id = ? AND track_id = ? AND state = 'PUBLISHED'
			ORDER BY version_no DESC LIMIT 1;
		`, eventID, trackID)
		if err == nil && rv != nil {
			return rv, nil
		}
	}

	// 2. Try event-default published rubric
	rv, err := s.queryRubric(ctx, q, `
		SELECT id, event_id, rubric_id, COALESCE(track_id, ''), version_no, state, criteria_json, configuration_hash, COALESCE(created_by, ''), created_at, COALESCE(published_at, '')
		FROM rubric_versions
		WHERE event_id = ? AND (track_id IS NULL OR track_id = '') AND state = 'PUBLISHED'
		ORDER BY version_no DESC LIMIT 1;
	`, eventID)
	if err == nil && rv != nil {
		return rv, nil
	}

	// 3. Fallback to any published rubric in the event
	rv, err = s.queryRubric(ctx, q, `
		SELECT id, event_id, rubric_id, COALESCE(track_id, ''), version_no, state, criteria_json, configuration_hash, COALESCE(created_by, ''), created_at, COALESCE(published_at, '')
		FROM rubric_versions
		WHERE event_id = ? AND state = 'PUBLISHED'
		ORDER BY version_no DESC LIMIT 1;
	`, eventID)
	if err == nil && rv != nil {
		return rv, nil
	}

	return nil, ErrNotFound
}

// GetRubricVersionByID fetches a specific rubric version by its unique ID.
func (s *Service) GetRubricVersionByID(ctx context.Context, id string) (*RubricVersion, error) {
	return s.getRubricVersionByIDWith(ctx, s.db, id)
}

func (s *Service) getRubricVersionByIDWith(ctx context.Context, q queryer, id string) (*RubricVersion, error) {
	return s.queryRubric(ctx, q, `
		SELECT id, event_id, rubric_id, COALESCE(track_id, ''), version_no, state, criteria_json, configuration_hash, COALESCE(created_by, ''), created_at, COALESCE(published_at, '')
		FROM rubric_versions
		WHERE id = ?;
	`, id)
}

func (s *Service) queryRubric(ctx context.Context, q queryer, query string, args ...any) (*RubricVersion, error) {
	var rv RubricVersion
	var criteriaJSON string

	err := q.QueryRowContext(ctx, query, args...).Scan(
		&rv.ID, &rv.EventID, &rv.RubricID, &rv.TrackID, &rv.VersionNo, &rv.State,
		&criteriaJSON, &rv.ConfigurationHash, &rv.CreatedBy, &rv.CreatedAt, &rv.PublishedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query rubric failed: %w", err)
	}

	if err := json.Unmarshal([]byte(criteriaJSON), &rv.Criteria); err != nil {
		return nil, fmt.Errorf("unmarshal criteria failed: %w", err)
	}

	return &rv, nil
}

// ListRubrics lists all rubric versions for an event.
func (s *Service) ListRubrics(ctx context.Context, eventID string) ([]RubricVersion, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, event_id, rubric_id, COALESCE(track_id, ''), version_no, state, criteria_json, configuration_hash, COALESCE(created_by, ''), created_at, COALESCE(published_at, '')
		FROM rubric_versions
		WHERE event_id = ?
		ORDER BY rubric_id ASC, version_no DESC;
	`, eventID)
	if err != nil {
		return nil, fmt.Errorf("failed to list rubrics: %w", err)
	}
	defer rows.Close()

	var result []RubricVersion
	for rows.Next() {
		var rv RubricVersion
		var criteriaJSON string
		if err := rows.Scan(
			&rv.ID, &rv.EventID, &rv.RubricID, &rv.TrackID, &rv.VersionNo, &rv.State,
			&criteriaJSON, &rv.ConfigurationHash, &rv.CreatedBy, &rv.CreatedAt, &rv.PublishedAt,
		); err != nil {
			return nil, fmt.Errorf("scan rubric failed: %w", err)
		}
		_ = json.Unmarshal([]byte(criteriaJSON), &rv.Criteria)
		result = append(result, rv)
	}

	return result, nil
}

// CreateRubricDraft creates a new draft version of a rubric.
func (s *Service) CreateRubricDraft(ctx context.Context, eventID, rubricID, trackID string, criteria []Criterion, createdBy string) (*RubricVersion, error) {
	if err := ValidateCriteria(criteria); err != nil {
		return nil, err
	}

	hash, jsonStr, err := ComputeRubricHash(criteria)
	if err != nil {
		return nil, err
	}

	if rubricID == "" {
		rubricID = "default"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var maxVer int
	_ = tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version_no), 0) FROM rubric_versions 
		WHERE event_id = ? AND rubric_id = ?;
	`, eventID, rubricID).Scan(&maxVer)

	versionNo := maxVer + 1
	nowUTC := time.Now().UTC().Format(time.RFC3339)
	rubricVersionID := fmt.Sprintf("rub_%s_%s_v%d", eventID, rubricID, versionNo)

	var trackParam sql.NullString
	if trackID != "" {
		trackParam = sql.NullString{String: trackID, Valid: true}
	}
	var createdByParam sql.NullString
	if createdBy != "" {
		createdByParam = sql.NullString{String: createdBy, Valid: true}
	}

	insertSQL := `
		INSERT INTO rubric_versions (
			id, event_id, rubric_id, track_id, version_no, state, 
			criteria_json, configuration_hash, created_by, created_at
		) VALUES (?, ?, ?, ?, ?, 'DRAFT', ?, ?, ?, ?);
	`
	_, err = tx.ExecContext(ctx, insertSQL,
		rubricVersionID, eventID, rubricID, trackParam, versionNo,
		jsonStr, hash, createdByParam, nowUTC,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert draft rubric: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit draft: %w", err)
	}

	return &RubricVersion{
		ID:                rubricVersionID,
		EventID:           eventID,
		RubricID:          rubricID,
		TrackID:           trackID,
		VersionNo:         versionNo,
		State:             "DRAFT",
		Criteria:          criteria,
		ConfigurationHash: hash,
		CreatedBy:         createdBy,
		CreatedAt:         nowUTC,
	}, nil
}

// PublishRubricVersion publishes a draft rubric version.
func (s *Service) PublishRubricVersion(ctx context.Context, rubricVersionID string) (*RubricVersion, error) {
	nowUTC := time.Now().UTC().Format(time.RFC3339)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var eventID, rubricID, state, criteriaJSON string
	err = tx.QueryRowContext(ctx, `
		SELECT event_id, rubric_id, state, criteria_json 
		FROM rubric_versions WHERE id = ?;
	`, rubricVersionID).Scan(&eventID, &rubricID, &state, &criteriaJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query rubric failed: %w", err)
	}

	if state == "PUBLISHED" {
		// Already published
		return s.GetRubricVersionByID(ctx, rubricVersionID)
	}

	// Validate criteria before publishing
	var criteria []Criterion
	if err := json.Unmarshal([]byte(criteriaJSON), &criteria); err != nil {
		return nil, fmt.Errorf("malformed criteria JSON: %w", err)
	}
	if err := ValidateCriteria(criteria); err != nil {
		return nil, err
	}

	// Retire previously published versions for this logical rubric
	_, err = tx.ExecContext(ctx, `
		UPDATE rubric_versions SET state = 'RETIRED'
		WHERE event_id = ? AND rubric_id = ? AND state = 'PUBLISHED';
	`, eventID, rubricID)
	if err != nil {
		return nil, fmt.Errorf("failed to retire previous rubrics: %w", err)
	}

	// Publish target version
	_, err = tx.ExecContext(ctx, `
		UPDATE rubric_versions SET state = 'PUBLISHED', published_at = ?
		WHERE id = ?;
	`, nowUTC, rubricVersionID)
	if err != nil {
		return nil, fmt.Errorf("failed to publish rubric: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit publication: %w", err)
	}

	return s.GetRubricVersionByID(ctx, rubricVersionID)
}

// --- Ballot Operations ---

// GetAssignmentDetail loads the evaluation context, enforcing that caller is assigned to this project.
func (s *Service) GetAssignmentDetail(ctx context.Context, assignmentID, callerUserID string, isAdmin bool) (*AssignmentDetail, error) {
	var ad AssignmentDetail

	query := `
		SELECT a.id, a.event_id, a.judge_user_id, a.project_id, a.rubric_version_id, a.status,
		       p.track_id, t.name, COALESCE(tm.name, 'Independent'),
		       s.title, s.summary, COALESCE(s.repo_url, ''), COALESCE(s.demo_url, '')
		FROM assignments a
		JOIN projects p ON a.project_id = p.id
		JOIN tracks t ON p.track_id = t.id
		LEFT JOIN teams tm ON p.team_id = tm.id
		LEFT JOIN submissions s ON p.id = s.project_id AND s.version_no = (SELECT MAX(version_no) FROM submissions WHERE project_id = p.id)
		WHERE a.id = ?;
	`
	var rubricVersionID string
	err := s.db.QueryRowContext(ctx, query, assignmentID).Scan(
		&ad.AssignmentID, &ad.EventID, &ad.JudgeUserID, &ad.ProjectID, &rubricVersionID, &ad.AssignmentStatus,
		&ad.TrackID, &ad.TrackName, &ad.TeamName,
		&ad.ProjectTitle, &ad.ProjectSummary, &ad.RepoURL, &ad.DemoURL,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to load assignment: %w", err)
	}

	// Peer isolation security boundary
	if !isAdmin && ad.JudgeUserID != callerUserID {
		return nil, ErrForbidden
	}

	// Load Rubric Version
	rubric, err := s.GetRubricVersionByID(ctx, rubricVersionID)
	if err != nil {
		// Fallback to active event rubric
		rubric, err = s.GetPublishedRubric(ctx, ad.EventID, ad.TrackID)
		if err != nil {
			rubric, err = s.EnsureDefaultRubric(ctx, ad.EventID, "")
			if err != nil {
				return nil, fmt.Errorf("failed to resolve rubric: %w", err)
			}
		}
	}
	ad.Rubric = rubric

	// Load latest ballot version
	latestBallot, err := s.GetLatestBallot(ctx, assignmentID)
	if err == nil && latestBallot != nil {
		ad.LatestBallot = latestBallot
		ad.IsSubmitted = (latestBallot.SaveKind == "SUBMISSION" || latestBallot.SaveKind == "SUBMITTED" || latestBallot.SaveKind == "CORRECTION")
	}

	return &ad, nil
}

// GetLatestBallot retrieves the most recent ballot version for an assignment.
func (s *Service) GetLatestBallot(ctx context.Context, assignmentID string) (*BallotVersion, error) {
	var bv BallotVersion
	var scoresJSON string
	var supersedes sql.NullString

	query := `
		SELECT id, assignment_id, judge_user_id, project_id, rubric_version_id,
		       version_no, save_kind, scores_json, comment, COALESCE(created_by, ''), created_at, supersedes_version_id
		FROM ballot_versions
		WHERE assignment_id = ?
		ORDER BY version_no DESC LIMIT 1;
	`
	err := s.db.QueryRowContext(ctx, query, assignmentID).Scan(
		&bv.ID, &bv.AssignmentID, &bv.JudgeUserID, &bv.ProjectID, &bv.RubricVersionID,
		&bv.VersionNo, &bv.SaveKind, &scoresJSON, &bv.Comment, &bv.CreatedBy, &bv.CreatedAt, &supersedes,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // No ballot created yet
		}
		return nil, fmt.Errorf("query latest ballot failed: %w", err)
	}

	if supersedes.Valid {
		bv.SupersedesVersionID = supersedes.String
	}

	// Unpack scores JSON
	var payload struct {
		Criteria      map[string]float64 `json:"criteria"`
		TotalScore    float64            `json:"total_score"`
		WeightedScore float64            `json:"weighted_score"`
	}
	if err := json.Unmarshal([]byte(scoresJSON), &payload); err == nil && payload.Criteria != nil {
		bv.CriteriaScores = payload.Criteria
		bv.TotalScore = payload.TotalScore
		bv.WeightedScore = payload.WeightedScore
	} else {
		// Raw map fallback
		rawMap := make(map[string]float64)
		_ = json.Unmarshal([]byte(scoresJSON), &rawMap)
		bv.CriteriaScores = rawMap
	}

	return &bv, nil
}

// SaveDraftBallot saves or updates a judge's working draft ballot.
func (s *Service) SaveDraftBallot(ctx context.Context, assignmentID, callerUserID string, scores map[string]float64, comment string) (*BallotVersion, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Verify assignment and ownership
	var eventID, judgeUserID, projectID, rubricVersionID, status string
	err = tx.QueryRowContext(ctx, `
		SELECT event_id, judge_user_id, project_id, rubric_version_id, status
		FROM assignments WHERE id = ?;
	`, assignmentID).Scan(&eventID, &judgeUserID, &projectID, &rubricVersionID, &status)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to query assignment: %w", err)
	}

	if judgeUserID != callerUserID {
		return nil, ErrForbidden
	}

	// 2. Check if ballot has already been submitted (immutability rule)
	var submittedCount int
	_ = tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM ballot_versions 
		WHERE assignment_id = ? AND save_kind IN ('SUBMISSION', 'SUBMITTED', 'CORRECTION');
	`, assignmentID).Scan(&submittedCount)
	if submittedCount > 0 {
		return nil, ErrBallotSubmitted
	}

	// 3. Load rubric to validate score ranges and calculate total
	rubric, err := s.getRubricVersionByIDWith(ctx, tx, rubricVersionID)
	if err != nil {
		rubric, err = s.getPublishedRubricWith(ctx, tx, eventID, "")
		if err != nil {
			return nil, fmt.Errorf("rubric unavailable: %w", err)
		}
	}

	critMap := make(map[string]Criterion)
	for _, c := range rubric.Criteria {
		critMap[c.ID] = c
	}

	// Validate score bounds for provided values
	for id, score := range scores {
		c, ok := critMap[id]
		if !ok {
			return nil, fmt.Errorf("%w: unknown criterion %q", ErrInvalidCriterion, id)
		}
		if math.IsNaN(score) || math.IsInf(score, 0) {
			return nil, fmt.Errorf("%w: invalid score for %s", ErrScoreOutOfBounds, id)
		}
		if score < c.MinScore || score > c.MaxScore {
			return nil, fmt.Errorf("%w: score %v for %s outside [%v, %v]", ErrScoreOutOfBounds, score, id, c.MinScore, c.MaxScore)
		}
	}

	totalScore, weightedScore := CalculateScores(rubric.Criteria, scores)

	// Format scores JSON
	scoresPayload := map[string]any{
		"criteria":       scores,
		"total_score":    totalScore,
		"weighted_score": weightedScore,
	}
	scoresJSONBytes, _ := json.Marshal(scoresPayload)
	scoresJSON := string(scoresJSONBytes)

	// 4. Determine next version number
	var maxVer int
	_ = tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version_no), 0) FROM ballot_versions WHERE assignment_id = ?;
	`, assignmentID).Scan(&maxVer)
	nextVer := maxVer + 1

	nowUTC := time.Now().UTC().Format(time.RFC3339)
	ballotID := fmt.Sprintf("bv_%s", randomHex(10))

	insertSQL := `
		INSERT INTO ballot_versions (
			id, assignment_id, judge_user_id, project_id, rubric_version_id,
			version_no, save_kind, scores_json, comment, created_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?, 'DRAFT', ?, ?, ?, ?);
	`
	_, err = tx.ExecContext(ctx, insertSQL,
		ballotID, assignmentID, callerUserID, projectID, rubric.ID,
		nextVer, scoresJSON, comment, callerUserID, nowUTC,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert draft ballot version: %w", err)
	}

	// Update assignment status to STARTED if currently ASSIGNED
	if status == "ASSIGNED" {
		_, _ = tx.ExecContext(ctx, `
			UPDATE assignments SET status = 'STARTED', started_at = ? WHERE id = ?;
		`, nowUTC, assignmentID)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit draft: %w", err)
	}

	return &BallotVersion{
		ID:              ballotID,
		AssignmentID:    assignmentID,
		JudgeUserID:     callerUserID,
		ProjectID:       projectID,
		RubricVersionID: rubric.ID,
		VersionNo:       nextVer,
		SaveKind:        "DRAFT",
		CriteriaScores:  scores,
		TotalScore:      totalScore,
		WeightedScore:   weightedScore,
		Comment:         comment,
		CreatedBy:       callerUserID,
		CreatedAt:       nowUTC,
	}, nil
}

// SubmitBallot validates all required criteria, computes the final score, and commits an immutable submission.
func (s *Service) SubmitBallot(ctx context.Context, assignmentID, callerUserID string, scores map[string]float64, comment string) (*BallotVersion, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Verify assignment and ownership
	var eventID, judgeUserID, projectID, rubricVersionID string
	err = tx.QueryRowContext(ctx, `
		SELECT event_id, judge_user_id, project_id, rubric_version_id
		FROM assignments WHERE id = ?;
	`, assignmentID).Scan(&eventID, &judgeUserID, &projectID, &rubricVersionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("failed to query assignment: %w", err)
	}

	if judgeUserID != callerUserID {
		return nil, ErrForbidden
	}

	// 2. Reject if already submitted (duplicate submission rejected safely)
	var submittedCount int
	_ = tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM ballot_versions 
		WHERE assignment_id = ? AND save_kind IN ('SUBMISSION', 'SUBMITTED', 'CORRECTION');
	`, assignmentID).Scan(&submittedCount)
	if submittedCount > 0 {
		return nil, ErrBallotSubmitted
	}

	// 3. Load rubric to strictly validate criteria
	rubric, err := s.getRubricVersionByIDWith(ctx, tx, rubricVersionID)
	if err != nil {
		rubric, err = s.getPublishedRubricWith(ctx, tx, eventID, "")
		if err != nil {
			return nil, fmt.Errorf("rubric unavailable: %w", err)
		}
	}

	critMap := make(map[string]Criterion)
	for _, c := range rubric.Criteria {
		critMap[c.ID] = c
	}

	// Reject unknown criteria
	for id := range scores {
		if _, ok := critMap[id]; !ok {
			return nil, fmt.Errorf("%w: unknown criterion %q", ErrInvalidCriterion, id)
		}
	}

	// Enforce all required criteria are present and within bounds
	for _, c := range rubric.Criteria {
		val, exists := scores[c.ID]
		if c.Required && !exists {
			return nil, fmt.Errorf("%w: criterion %q (%s) is required", ErrMissingCriterion, c.ID, c.Name)
		}
		if exists {
			if math.IsNaN(val) || math.IsInf(val, 0) {
				return nil, fmt.Errorf("%w: invalid score for %s", ErrScoreOutOfBounds, c.ID)
			}
			if val < c.MinScore || val > c.MaxScore {
				return nil, fmt.Errorf("%w: score %v for %s outside [%v, %v]", ErrScoreOutOfBounds, val, c.ID, c.MinScore, c.MaxScore)
			}
		}
	}

	totalScore, weightedScore := CalculateScores(rubric.Criteria, scores)

	scoresPayload := map[string]any{
		"criteria":       scores,
		"total_score":    totalScore,
		"weighted_score": weightedScore,
	}
	scoresJSONBytes, _ := json.Marshal(scoresPayload)
	scoresJSON := string(scoresJSONBytes)

	// 4. Determine next version number
	var maxVer int
	_ = tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version_no), 0) FROM ballot_versions WHERE assignment_id = ?;
	`, assignmentID).Scan(&maxVer)
	nextVer := maxVer + 1

	nowUTC := time.Now().UTC().Format(time.RFC3339)
	ballotID := fmt.Sprintf("bv_%s", randomHex(10))

	insertSQL := `
		INSERT INTO ballot_versions (
			id, assignment_id, judge_user_id, project_id, rubric_version_id,
			version_no, save_kind, scores_json, comment, created_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?, 'SUBMISSION', ?, ?, ?, ?);
	`
	_, err = tx.ExecContext(ctx, insertSQL,
		ballotID, assignmentID, callerUserID, projectID, rubric.ID,
		nextVer, scoresJSON, comment, callerUserID, nowUTC,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert submission ballot version: %w", err)
	}

	// 5. Update assignment status to COMPLETED
	_, err = tx.ExecContext(ctx, `
		UPDATE assignments SET status = 'COMPLETED', completed_at = ? WHERE id = ?;
	`, nowUTC, assignmentID)
	if err != nil {
		return nil, fmt.Errorf("failed to update assignment to COMPLETED: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit submission: %w", err)
	}

	return &BallotVersion{
		ID:              ballotID,
		AssignmentID:    assignmentID,
		JudgeUserID:     callerUserID,
		ProjectID:       projectID,
		RubricVersionID: rubric.ID,
		VersionNo:       nextVer,
		SaveKind:        "SUBMISSION",
		CriteriaScores:  scores,
		TotalScore:      totalScore,
		WeightedScore:   weightedScore,
		Comment:         comment,
		CreatedBy:       callerUserID,
		CreatedAt:       nowUTC,
	}, nil
}

// ListJudgeAssignments returns all assignments allocated to a specific judge in an event with evaluation progress.
func (s *Service) ListJudgeAssignments(ctx context.Context, eventID, judgeUserID string) ([]AssignmentDetail, error) {
	query := `
		SELECT a.id, a.event_id, a.judge_user_id, a.project_id, a.rubric_version_id, a.status,
		       p.track_id, t.name, COALESCE(tm.name, 'Independent'),
		       s.title, s.summary, COALESCE(s.repo_url, ''), COALESCE(s.demo_url, '')
		FROM assignments a
		JOIN projects p ON a.project_id = p.id
		JOIN tracks t ON p.track_id = t.id
		LEFT JOIN teams tm ON p.team_id = tm.id
		LEFT JOIN submissions s ON p.id = s.project_id AND s.version_no = (SELECT MAX(version_no) FROM submissions WHERE project_id = p.id)
		WHERE a.event_id = ? AND a.judge_user_id = ? AND a.status NOT IN ('CANCELLED', 'REASSIGNED')
		ORDER BY t.name ASC, p.id ASC;
	`
	rows, err := s.db.QueryContext(ctx, query, eventID, judgeUserID)
	if err != nil {
		return nil, fmt.Errorf("failed to list judge assignments: %w", err)
	}
	defer rows.Close()

	var list []AssignmentDetail
	for rows.Next() {
		var ad AssignmentDetail
		var rubricVersionID string
		if err := rows.Scan(
			&ad.AssignmentID, &ad.EventID, &ad.JudgeUserID, &ad.ProjectID, &rubricVersionID, &ad.AssignmentStatus,
			&ad.TrackID, &ad.TrackName, &ad.TeamName,
			&ad.ProjectTitle, &ad.ProjectSummary, &ad.RepoURL, &ad.DemoURL,
		); err != nil {
			return nil, fmt.Errorf("scan assignment failed: %w", err)
		}
		list = append(list, ad)
	}
	rows.Close()

	for i := range list {
		latestBallot, err := s.GetLatestBallot(ctx, list[i].AssignmentID)
		if err == nil && latestBallot != nil {
			list[i].LatestBallot = latestBallot
			list[i].IsSubmitted = (latestBallot.SaveKind == "SUBMISSION" || latestBallot.SaveKind == "SUBMITTED" || latestBallot.SaveKind == "CORRECTION")
		}
	}

	return list, nil
}
