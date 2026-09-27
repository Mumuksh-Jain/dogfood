package assignment

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Service coordinates database operations and deterministic assignment runs.
type Service struct {
	db *sql.DB
}

// NewService creates an assignment Service instance.
func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

// Config controls an assignment generation run.
type Config struct {
	EventID          string
	TargetReviews    int      // e.g. 2, 3
	CreatedBy        string   // optional organizer user ID
	AlgorithmKey     string   // defaults to "bipartite_min_cost_flow"
	AlgorithmVersion string   // defaults to "1.0"
	RubricVersionID  string   // optional; resolved if empty
	RunID            string   // optional; generated if empty
	ProjectIDs       []string // optional; if empty, all eligible projects in event
}

// Assignment represents a persisted assignment record.
type Assignment struct {
	ID                     string         `json:"id"`
	EventID                string         `json:"event_id"`
	AssignmentRunID        string         `json:"assignment_run_id"`
	JudgeUserID            string         `json:"judge_user_id"`
	ProjectID              string         `json:"project_id"`
	RubricVersionID        string         `json:"rubric_version_id"`
	Status                 string         `json:"status"`
	AssignedAt             string         `json:"assigned_at"`
	SupersedesAssignmentID sql.NullString `json:"supersedes_assignment_id,omitempty"`
}

// Result holds the final outcome of an assignment execution.
type Result struct {
	RunID                string                `json:"run_id"`
	Status               string                `json:"status"` // COMPLETED or INFEASIBLE
	Feasible             bool                  `json:"feasible"`
	InfeasibilityCode    string                `json:"infeasibility_code,omitempty"`
	InfeasibilityDetails *InfeasibilityDetails `json:"infeasibility_details,omitempty"`
	AssignmentsCount     int                   `json:"assignments_count"`
	Assignments          []Assignment          `json:"assignments,omitempty"`
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// GenerateAssignments executes a deterministic assignment run within a database transaction.
func (s *Service) GenerateAssignments(ctx context.Context, cfg Config) (*Result, error) {
	if cfg.EventID == "" {
		return nil, fmt.Errorf("event_id is required")
	}
	if cfg.TargetReviews <= 0 {
		cfg.TargetReviews = 3
	}
	if cfg.AlgorithmKey == "" {
		cfg.AlgorithmKey = "bipartite_min_cost_flow"
	}
	if cfg.AlgorithmVersion == "" {
		cfg.AlgorithmVersion = "1.0"
	}
	if cfg.RunID == "" {
		cfg.RunID = fmt.Sprintf("run_%s", randomHex(8))
	}

	nowUTC := time.Now().UTC().Format(time.RFC3339)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// 1. Load eligible projects for event
	projectQuery := `
		SELECT p.id, p.track_id
		FROM projects p
		WHERE p.event_id = ?
		  AND p.eligibility_status = 'ELIGIBLE'
		  AND (
		      NOT EXISTS (SELECT 1 FROM submissions s WHERE s.project_id = p.id)
		      OR EXISTS (SELECT 1 FROM submissions s WHERE s.project_id = p.id AND s.state = 'SUBMITTED')
		  )
		ORDER BY p.track_id ASC, p.id ASC;
	`
	rows, err := tx.QueryContext(ctx, projectQuery, cfg.EventID)
	if err != nil {
		return nil, fmt.Errorf("failed to load projects: %w", err)
	}
	defer rows.Close()

	var projects []ProjectInput
	allowedProjectsMap := make(map[string]bool)
	if len(cfg.ProjectIDs) > 0 {
		for _, pid := range cfg.ProjectIDs {
			allowedProjectsMap[pid] = true
		}
	}

	for rows.Next() {
		var pid, trkID string
		if err := rows.Scan(&pid, &trkID); err != nil {
			return nil, fmt.Errorf("scan project failed: %w", err)
		}
		if len(cfg.ProjectIDs) > 0 && !allowedProjectsMap[pid] {
			continue
		}
		projects = append(projects, ProjectInput{
			ID:                   pid,
			TrackID:              trkID,
			AssignedJudgeUserIDs: make(map[string]bool),
		})
	}
	rows.Close()

	// 2. Load eligible judges for event
	judgeQuery := `
		SELECT jp.user_id, jp.capacity
		FROM judge_profiles jp
		WHERE jp.event_id = ?
		  AND jp.active = 1
		  AND jp.capacity > 0
		  AND jp.deactivated_at IS NULL
		ORDER BY jp.user_id ASC;
	`
	jRows, err := tx.QueryContext(ctx, judgeQuery, cfg.EventID)
	if err != nil {
		return nil, fmt.Errorf("failed to load judges: %w", err)
	}
	defer jRows.Close()

	judgeMap := make(map[string]*JudgeInput)
	var judges []JudgeInput
	for jRows.Next() {
		var uid string
		var capVal int
		if err := jRows.Scan(&uid, &capVal); err != nil {
			return nil, fmt.Errorf("scan judge failed: %w", err)
		}
		ji := &JudgeInput{
			UserID:             uid,
			Capacity:           capVal,
			EligibleTracks:     make(map[string]bool),
			ExistingProjectIDs: make(map[string]bool),
		}
		judgeMap[uid] = ji
	}
	jRows.Close()

	// 3. Load judge track eligibility for event
	trackQuery := `
		SELECT judge_user_id, track_id
		FROM judge_track_eligibility
		WHERE event_id = ?
		  AND eligible = 1
		ORDER BY judge_user_id ASC, track_id ASC;
	`
	tRows, err := tx.QueryContext(ctx, trackQuery, cfg.EventID)
	if err != nil {
		return nil, fmt.Errorf("failed to load judge track eligibility: %w", err)
	}
	defer tRows.Close()

	for tRows.Next() {
		var jid, trkID string
		if err := tRows.Scan(&jid, &trkID); err != nil {
			return nil, fmt.Errorf("scan judge track eligibility failed: %w", err)
		}
		if ji, ok := judgeMap[jid]; ok {
			ji.EligibleTracks[trkID] = true
		}
	}
	tRows.Close()

	// 4. Load existing active assignments for event
	asgQuery := `
		SELECT judge_user_id, project_id
		FROM assignments
		WHERE event_id = ?
		  AND status NOT IN ('CANCELLED', 'REASSIGNED');
	`
	aRows, err := tx.QueryContext(ctx, asgQuery, cfg.EventID)
	if err != nil {
		return nil, fmt.Errorf("failed to load existing assignments: %w", err)
	}
	defer aRows.Close()

	projectIndexMap := make(map[string]int)
	for i, p := range projects {
		projectIndexMap[p.ID] = i
	}

	for aRows.Next() {
		var jid, pid string
		if err := aRows.Scan(&jid, &pid); err != nil {
			return nil, fmt.Errorf("scan existing assignment failed: %w", err)
		}
		if ji, ok := judgeMap[jid]; ok {
			ji.ActiveAssignmentsCount++
			ji.ExistingProjectIDs[pid] = true
		}
		if idx, ok := projectIndexMap[pid]; ok {
			projects[idx].ActiveReviewsCount++
			projects[idx].AssignedJudgeUserIDs[jid] = true
		}
	}
	aRows.Close()

	for _, ji := range judgeMap {
		judges = append(judges, *ji)
	}

	// 5. Resolve RubricVersionID
	rubricVersionID := cfg.RubricVersionID
	if rubricVersionID == "" {
		// Look for published event rubric version
		err = tx.QueryRowContext(ctx, `
			SELECT id FROM rubric_versions 
			WHERE event_id = ? AND state = 'PUBLISHED' 
			ORDER BY version_no DESC LIMIT 1;
		`, cfg.EventID).Scan(&rubricVersionID)
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("query rubric_versions failed: %w", err)
		}
		if rubricVersionID == "" {
			// Look for any rubric version
			err = tx.QueryRowContext(ctx, `
				SELECT id FROM rubric_versions 
				WHERE event_id = ? 
				ORDER BY version_no DESC LIMIT 1;
			`, cfg.EventID).Scan(&rubricVersionID)
			if err != nil && err != sql.ErrNoRows {
				return nil, fmt.Errorf("fallback query rubric_versions failed: %w", err)
			}
		}
		// If still none, create a default published rubric version for the event
		if rubricVersionID == "" {
			rubricVersionID = fmt.Sprintf("rub_%s_v1", cfg.EventID)
			_, err = tx.ExecContext(ctx, `
				INSERT INTO rubric_versions (id, event_id, rubric_id, version_no, state, criteria_json, configuration_hash, created_at, published_at)
				VALUES (?, ?, 'default', 1, 'PUBLISHED', '[]', '', ?, ?);
			`, rubricVersionID, cfg.EventID, nowUTC, nowUTC)
			if err != nil {
				return nil, fmt.Errorf("failed to create default rubric_version: %w", err)
			}
		}
	}

	// 6. Run the deterministic assignment algorithm
	engineRes := RunDeterministicAssignment(projects, judges, cfg.TargetReviews)

	// Serialize configuration JSON for audit trail
	configMap := map[string]interface{}{
		"target_reviews":    cfg.TargetReviews,
		"rubric_version_id": rubricVersionID,
	}
	configBytes, _ := json.Marshal(configMap)
	configJSON := string(configBytes)

	var creatorParam sql.NullString
	if cfg.CreatedBy != "" {
		creatorParam = sql.NullString{String: cfg.CreatedBy, Valid: true}
	}

	// 7. Persist run based on feasibility
	if !engineRes.Feasible {
		var infDetailsJSON string
		if engineRes.Details != nil {
			infDetailsJSON = engineRes.Details.ToJSON()
		}

		insertRunSQL := `
			INSERT INTO assignment_runs (
				id, event_id, algorithm_key, algorithm_version, config_json, 
				requested_reviews_total, status, created_by, created_at, 
				finished_at, infeasibility_code, infeasibility_details_json
			) VALUES (?, ?, ?, ?, ?, ?, 'INFEASIBLE', ?, ?, ?, ?, ?);
		`
		_, err = tx.ExecContext(ctx, insertRunSQL,
			cfg.RunID, cfg.EventID, cfg.AlgorithmKey, cfg.AlgorithmVersion, configJSON,
			engineRes.RequestedTotal, creatorParam, nowUTC, nowUTC,
			engineRes.InfeasibilityCode, infDetailsJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to persist infeasible assignment run: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("failed to commit infeasible run transaction: %w", err)
		}

		return &Result{
			RunID:                cfg.RunID,
			Status:               "INFEASIBLE",
			Feasible:             false,
			InfeasibilityCode:    engineRes.InfeasibilityCode,
			InfeasibilityDetails: engineRes.Details,
			AssignmentsCount:     0,
		}, nil
	}

	// 8. Run is feasible: record COMPLETED run
	insertRunSQL := `
		INSERT INTO assignment_runs (
			id, event_id, algorithm_key, algorithm_version, config_json, 
			requested_reviews_total, status, created_by, created_at, 
			finished_at, infeasibility_code, infeasibility_details_json
		) VALUES (?, ?, ?, ?, ?, ?, 'COMPLETED', ?, ?, ?, NULL, NULL);
	`
	_, err = tx.ExecContext(ctx, insertRunSQL,
		cfg.RunID, cfg.EventID, cfg.AlgorithmKey, cfg.AlgorithmVersion, configJSON,
		engineRes.RequestedTotal, creatorParam, nowUTC, nowUTC,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to persist completed assignment run: %w", err)
	}

	// 9. Persist assignments
	var persistedAssignments []Assignment
	for i, d := range engineRes.Decisions {
		asgID := fmt.Sprintf("asg_%s_%04d", cfg.RunID, i+1)

		// Check for a previously CANCELLED assignment to set supersedes_assignment_id
		var supersedesID sql.NullString
		err = tx.QueryRowContext(ctx, `
			SELECT id FROM assignments 
			WHERE event_id = ? AND judge_user_id = ? AND project_id = ? AND status = 'CANCELLED' 
			ORDER BY assigned_at DESC LIMIT 1;
		`, cfg.EventID, d.JudgeUserID, d.ProjectID).Scan(&supersedesID)
		if err != nil && err != sql.ErrNoRows {
			return nil, fmt.Errorf("query supersedes assignment failed: %w", err)
		}

		insertAsgSQL := `
			INSERT INTO assignments (
				id, event_id, assignment_run_id, judge_user_id, project_id, 
				rubric_version_id, status, assigned_at, supersedes_assignment_id
			) VALUES (?, ?, ?, ?, ?, ?, 'ASSIGNED', ?, ?);
		`
		_, err = tx.ExecContext(ctx, insertAsgSQL,
			asgID, cfg.EventID, cfg.RunID, d.JudgeUserID, d.ProjectID,
			rubricVersionID, nowUTC, supersedesID,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to insert assignment (%s -> %s): %w", d.JudgeUserID, d.ProjectID, err)
		}

		persistedAssignments = append(persistedAssignments, Assignment{
			ID:                     asgID,
			EventID:                cfg.EventID,
			AssignmentRunID:        cfg.RunID,
			JudgeUserID:            d.JudgeUserID,
			ProjectID:              d.ProjectID,
			RubricVersionID:        rubricVersionID,
			Status:                 "ASSIGNED",
			AssignedAt:             nowUTC,
			SupersedesAssignmentID: supersedesID,
		})
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit assignment transaction: %w", err)
	}

	return &Result{
		RunID:            cfg.RunID,
		Status:           "COMPLETED",
		Feasible:         true,
		AssignmentsCount: len(persistedAssignments),
		Assignments:      persistedAssignments,
	}, nil
}
