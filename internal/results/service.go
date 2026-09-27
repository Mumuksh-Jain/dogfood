package results

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

var (
	ErrNotFound          = errors.New("not found: requested result run does not exist")
	ErrPublishedImmutable = errors.New("conflict: published result runs are immutable")
	ErrInvalidStatus     = errors.New("conflict: invalid result run status transition")
)

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

// ComputeResults executes score aggregation and normalization for an event, storing a DRAFT run.
func (s *Service) ComputeResults(ctx context.Context, eventID, callerUserID string) (*ResultRun, error) {
	nowUTC := time.Now().UTC().Format(time.RFC3339)

	// 1. Load all eligible projects in the event
	projQuery := `
		SELECT p.id, p.event_id, p.track_id, COALESCE(t.name, ''), COALESCE(tm.name, 'Independent'),
		       COALESCE(s.title, 'Untitled Project'), COALESCE(s.summary, ''),
		       COALESCE(s.repo_url, ''), COALESCE(s.demo_url, '')
		FROM projects p
		JOIN tracks t ON p.track_id = t.id
		LEFT JOIN teams tm ON p.team_id = tm.id
		LEFT JOIN submissions s ON p.id = s.project_id AND s.version_no = (
			SELECT MAX(version_no) FROM submissions WHERE project_id = p.id
		)
		WHERE p.event_id = ?
		ORDER BY p.id ASC;
	`
	rows, err := s.db.QueryContext(ctx, projQuery, eventID)
	if err != nil {
		return nil, fmt.Errorf("failed to query projects: %w", err)
	}
	defer rows.Close()

	var projects []ProjectInfo
	for rows.Next() {
		var p ProjectInfo
		if err := rows.Scan(
			&p.ID, &p.EventID, &p.TrackID, &p.TrackName, &p.TeamName,
			&p.ProjectTitle, &p.ProjectSummary, &p.RepoURL, &p.DemoURL,
		); err != nil {
			return nil, fmt.Errorf("failed to scan project: %w", err)
		}
		projects = append(projects, p)
	}
	rows.Close()

	// 2. Load expected assignment count per project
	assignCountQuery := `
		SELECT project_id, COUNT(*)
		FROM assignments
		WHERE event_id = ? AND status NOT IN ('CANCELLED', 'REASSIGNED')
		GROUP BY project_id;
	`
	aRows, err := s.db.QueryContext(ctx, assignCountQuery, eventID)
	if err != nil {
		return nil, fmt.Errorf("failed to query assignment counts: %w", err)
	}
	defer aRows.Close()

	expectedPerProject := make(map[string]int)
	for aRows.Next() {
		var pid string
		var count int
		if err := aRows.Scan(&pid, &count); err == nil {
			expectedPerProject[pid] = count
		}
	}
	aRows.Close()

	// 3. Load latest submitted ballots for this event
	ballotQuery := `
		SELECT b.id, b.assignment_id, b.judge_user_id, COALESCE(u.display_name, b.judge_user_id),
		       b.project_id, b.rubric_version_id, b.version_no, b.scores_json, b.created_at
		FROM ballot_versions b
		JOIN users u ON b.judge_user_id = u.id
		JOIN assignments a ON b.assignment_id = a.id
		WHERE a.event_id = ? AND b.save_kind IN ('SUBMISSION', 'SUBMITTED', 'CORRECTION')
		  AND b.version_no = (
		      SELECT MAX(bv2.version_no) FROM ballot_versions bv2 
		      WHERE bv2.assignment_id = b.assignment_id AND bv2.save_kind IN ('SUBMISSION', 'SUBMITTED', 'CORRECTION')
		  )
		ORDER BY b.project_id ASC, b.judge_user_id ASC;
	`
	bRows, err := s.db.QueryContext(ctx, ballotQuery, eventID)
	if err != nil {
		return nil, fmt.Errorf("failed to query submitted ballots: %w", err)
	}
	defer bRows.Close()

	var rawBallots []RawBallotRecord
	for bRows.Next() {
		var (
			bID, aID, jID, jName, pID, rID, createdAt string
			vNo                                       int
			scoresJSON                                string
		)
		if err := bRows.Scan(&bID, &aID, &jID, &jName, &pID, &rID, &vNo, &scoresJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan ballot: %w", err)
		}

		var payload struct {
			Criteria      map[string]float64 `json:"criteria"`
			TotalScore    float64            `json:"total_score"`
			WeightedScore float64            `json:"weighted_score"`
		}
		rawScore := 0.0
		var criteria map[string]float64
		if err := json.Unmarshal([]byte(scoresJSON), &payload); err == nil {
			rawScore = payload.WeightedScore
			criteria = payload.Criteria
		} else {
			rawMap := make(map[string]float64)
			_ = json.Unmarshal([]byte(scoresJSON), &rawMap)
			criteria = rawMap
			sum := 0.0
			for _, v := range rawMap {
				sum += v
			}
			if len(rawMap) > 0 {
				rawScore = math.Round((sum/float64(len(rawMap)))*100) / 100
			}
		}

		rawBallots = append(rawBallots, RawBallotRecord{
			AssignmentID:    aID,
			JudgeUserID:     jID,
			JudgeName:       jName,
			ProjectID:       pID,
			RubricVersionID: rID,
			BallotVersionID: bID,
			BallotVersionNo: vNo,
			RawScore:        rawScore,
			CriteriaScores:  criteria,
			SubmittedAt:     createdAt,
		})
	}
	bRows.Close()

	// 4. Build canonical input manifest & SHA-256 digest
	_, manifestJSON, inputDigest, err := BuildInputManifest(eventID, AlgorithmZScoreStandardization, AlgorithmVersionV1, rawBallots)
	if err != nil {
		return nil, fmt.Errorf("failed to construct input manifest: %w", err)
	}

	runID := fmt.Sprintf("run_%s", randomHex(8))

	// 5. Compute defensible project entries
	entries, err := ComputeProjectResults(
		runID,
		eventID,
		projects,
		rawBallots,
		expectedPerProject,
		inputDigest,
		StatusDraft,
		"",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to compute project results: %w", err)
	}

	// 6. Check for active PUBLISHED run to record supersession
	var latestPublishedID sql.NullString
	_ = s.db.QueryRowContext(ctx, `
		SELECT id FROM result_runs 
		WHERE event_id = ? AND status = 'PUBLISHED' 
		ORDER BY created_at DESC LIMIT 1;
	`, eventID).Scan(&latestPublishedID)

	var supersedesID *string
	if latestPublishedID.Valid {
		idStr := latestPublishedID.String
		supersedesID = &idStr
	}

	// 7. Write run and entries atomically
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	insertRunSQL := `
		INSERT INTO result_runs (
			id, event_id, supersedes_result_run_id, algorithm_key, algorithm_version,
			config_json, cohort_policy, tie_policy, status, input_manifest_json,
			input_digest, created_by, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'DRAFT', ?, ?, ?, ?);
	`
	configJSON := `{"scale_center":2.5,"scale_factor":0.8,"min_scale":0.0,"max_scale":5.0}`
	_, err = tx.ExecContext(ctx, insertRunSQL,
		runID, eventID, supersedesID, AlgorithmZScoreStandardization, AlgorithmVersionV1,
		configJSON, CohortAllCompleted, TieDeterministicFallback, manifestJSON,
		inputDigest, callerUserID, nowUTC,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert result_run: %w", err)
	}

	insertEntrySQL := `
		INSERT INTO result_entries (
			result_run_id, project_id, raw_score, normalized_score, final_score,
			rank, tie_group, expected_reviews, completed_reviews, effective_reviews,
			fallback_count, explanation_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`
	for _, entry := range entries {
		_, err = tx.ExecContext(ctx, insertEntrySQL,
			runID, entry.ProjectID, entry.RawScore, entry.NormalizedScore, entry.FinalScore,
			entry.Rank, entry.TieGroup, entry.ExpectedReviews, entry.CompletedReviews,
			entry.EffectiveReviews, entry.FallbackCount, entry.ExplanationJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to insert result_entry for %s: %w", entry.ProjectID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit result run: %w", err)
	}

	return &ResultRun{
		ID:                    runID,
		EventID:               eventID,
		SupersedesResultRunID: supersedesID,
		AlgorithmKey:          AlgorithmZScoreStandardization,
		AlgorithmVersion:      AlgorithmVersionV1,
		ConfigJSON:            configJSON,
		CohortPolicy:          CohortAllCompleted,
		TiePolicy:             TieDeterministicFallback,
		Status:                StatusDraft,
		InputManifestJSON:     manifestJSON,
		InputDigest:           inputDigest,
		CreatedBy:             callerUserID,
		CreatedAt:             nowUTC,
	}, nil
}

// PublishResults publishes a draft results run, retiring prior publications and freezing immutability.
func (s *Service) PublishResults(ctx context.Context, runID, callerUserID string) (*ResultRun, error) {
	nowUTC := time.Now().UTC().Format(time.RFC3339)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var eventID, status, manifestJSON, inputDigest, algKey, algVer, configJSON, cohort, tie, createdBy, createdAt string
	var supersedesID sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT id, event_id, supersedes_result_run_id, algorithm_key, algorithm_version,
		       config_json, cohort_policy, tie_policy, status, input_manifest_json,
		       input_digest, COALESCE(created_by, ''), created_at
		FROM result_runs WHERE id = ?;
	`, runID).Scan(
		&runID, &eventID, &supersedesID, &algKey, &algVer,
		&configJSON, &cohort, &tie, &status, &manifestJSON,
		&inputDigest, &createdBy, &createdAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query result run failed: %w", err)
	}

	if status == StatusPublished {
		// Already published
		return s.GetResultRunByID(ctx, runID)
	}

	// Retire previously published runs for this event
	_, err = tx.ExecContext(ctx, `
		UPDATE result_runs SET status = 'RETIRED'
		WHERE event_id = ? AND status = 'PUBLISHED';
	`, eventID)
	if err != nil {
		return nil, fmt.Errorf("failed to retire previous results: %w", err)
	}

	// Publish target run
	_, err = tx.ExecContext(ctx, `
		UPDATE result_runs 
		SET status = 'PUBLISHED', published_at = ?
		WHERE id = ?;
	`, nowUTC, runID)
	if err != nil {
		return nil, fmt.Errorf("failed to publish result run: %w", err)
	}

	// Update publication metadata in each result_entry's explanation_json
	entryRows, err := tx.QueryContext(ctx, `
		SELECT project_id, explanation_json FROM result_entries WHERE result_run_id = ?;
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("query entries failed: %w", err)
	}
	defer entryRows.Close()

	type entryExpUpdate struct {
		projectID string
		rawExp    string
	}
	var updates []entryExpUpdate
	for entryRows.Next() {
		var u entryExpUpdate
		if err := entryRows.Scan(&u.projectID, &u.rawExp); err == nil {
			updates = append(updates, u)
		}
	}
	entryRows.Close()

	for _, u := range updates {
		var payload ExplanationPayload
		if err := json.Unmarshal([]byte(u.rawExp), &payload); err == nil {
			payload.RunStatus = StatusPublished
			payload.PublishedAt = nowUTC
			newBytes, _ := json.Marshal(payload)
			_, _ = tx.ExecContext(ctx, `
				UPDATE result_entries SET explanation_json = ? 
				WHERE result_run_id = ? AND project_id = ?;
			`, string(newBytes), runID, u.projectID)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit publication: %w", err)
	}

	pubAt := nowUTC
	var superStr *string
	if supersedesID.Valid {
		str := supersedesID.String
		superStr = &str
	}

	return &ResultRun{
		ID:                    runID,
		EventID:               eventID,
		SupersedesResultRunID: superStr,
		AlgorithmKey:          algKey,
		AlgorithmVersion:      algVer,
		ConfigJSON:            configJSON,
		CohortPolicy:          cohort,
		TiePolicy:             tie,
		Status:                StatusPublished,
		InputManifestJSON:     manifestJSON,
		InputDigest:           inputDigest,
		CreatedBy:             createdBy,
		CreatedAt:             createdAt,
		PublishedAt:           &pubAt,
	}, nil
}

// GetActiveResults returns the latest PUBLISHED result run and its ranked entries.
func (s *Service) GetActiveResults(ctx context.Context, eventID string) (*ResultRun, []ResultEntry, error) {
	var runID string
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM result_runs 
		WHERE event_id = ? AND status = 'PUBLISHED'
		ORDER BY created_at DESC LIMIT 1;
	`, eventID).Scan(&runID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("failed to query active results: %w", err)
	}

	run, err := s.GetResultRunByID(ctx, runID)
	if err != nil {
		return nil, nil, err
	}

	entries, err := s.ListEntriesForRun(ctx, runID)
	if err != nil {
		return nil, nil, err
	}

	return run, entries, nil
}

// GetResultRunByID fetches a single ResultRun by its ID.
func (s *Service) GetResultRunByID(ctx context.Context, runID string) (*ResultRun, error) {
	var r ResultRun
	var superID, pubAt, failReason sql.NullString

	query := `
		SELECT id, event_id, supersedes_result_run_id, algorithm_key, algorithm_version,
		       config_json, cohort_policy, tie_policy, status, input_manifest_json,
		       input_digest, COALESCE(created_by, ''), created_at, published_at, failure_reason
		FROM result_runs WHERE id = ?;
	`
	err := s.db.QueryRowContext(ctx, query, runID).Scan(
		&r.ID, &r.EventID, &superID, &r.AlgorithmKey, &r.AlgorithmVersion,
		&r.ConfigJSON, &r.CohortPolicy, &r.TiePolicy, &r.Status, &r.InputManifestJSON,
		&r.InputDigest, &r.CreatedBy, &r.CreatedAt, &pubAt, &failReason,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query result run failed: %w", err)
	}

	if superID.Valid {
		str := superID.String
		r.SupersedesResultRunID = &str
	}
	if pubAt.Valid {
		str := pubAt.String
		r.PublishedAt = &str
	}
	if failReason.Valid {
		str := failReason.String
		r.FailureReason = &str
	}

	return &r, nil
}

// ListEntriesForRun loads all ranked entries for a given run with joined project metadata.
func (s *Service) ListEntriesForRun(ctx context.Context, runID string) ([]ResultEntry, error) {
	query := `
		SELECT re.result_run_id, re.project_id, re.raw_score, re.normalized_score, re.final_score,
		       re.rank, re.tie_group, re.expected_reviews, re.completed_reviews, re.effective_reviews,
		       re.fallback_count, re.explanation_json,
		       COALESCE(s.title, 'Untitled'), COALESCE(s.summary, ''),
		       p.team_id, COALESCE(tm.name, 'Independent'), p.track_id, COALESCE(t.name, ''),
		       COALESCE(s.repo_url, ''), COALESCE(s.demo_url, '')
		FROM result_entries re
		JOIN projects p ON re.project_id = p.id
		JOIN tracks t ON p.track_id = t.id
		LEFT JOIN teams tm ON p.team_id = tm.id
		LEFT JOIN submissions s ON p.id = s.project_id AND s.version_no = (
			SELECT MAX(version_no) FROM submissions WHERE project_id = p.id
		)
		WHERE re.result_run_id = ?
		ORDER BY re.rank ASC, re.project_id ASC;
	`
	rows, err := s.db.QueryContext(ctx, query, runID)
	if err != nil {
		return nil, fmt.Errorf("failed to query result entries: %w", err)
	}
	defer rows.Close()

	var entries []ResultEntry
	for rows.Next() {
		var e ResultEntry
		if err := rows.Scan(
			&e.ResultRunID, &e.ProjectID, &e.RawScore, &e.NormalizedScore, &e.FinalScore,
			&e.Rank, &e.TieGroup, &e.ExpectedReviews, &e.CompletedReviews, &e.EffectiveReviews,
			&e.FallbackCount, &e.ExplanationJSON,
			&e.ProjectTitle, &e.ProjectSummary,
			&e.TeamID, &e.TeamName, &e.TrackID, &e.TrackName,
			&e.RepoURL, &e.DemoURL,
		); err != nil {
			return nil, fmt.Errorf("failed to scan entry: %w", err)
		}

		var payload ExplanationPayload
		if err := json.Unmarshal([]byte(e.ExplanationJSON), &payload); err == nil {
			e.Explanation = &payload
		}

		entries = append(entries, e)
	}

	return entries, nil
}

// GetProjectExplanation retrieves the full derivation receipt for an individual project.
func (s *Service) GetProjectExplanation(ctx context.Context, runID, projectID string) (*ResultRun, *ResultEntry, *ExplanationPayload, error) {
	run, err := s.GetResultRunByID(ctx, runID)
	if err != nil {
		return nil, nil, nil, err
	}

	query := `
		SELECT re.result_run_id, re.project_id, re.raw_score, re.normalized_score, re.final_score,
		       re.rank, re.tie_group, re.expected_reviews, re.completed_reviews, re.effective_reviews,
		       re.fallback_count, re.explanation_json,
		       COALESCE(s.title, 'Untitled'), COALESCE(s.summary, ''),
		       p.team_id, COALESCE(tm.name, 'Independent'), p.track_id, COALESCE(t.name, ''),
		       COALESCE(s.repo_url, ''), COALESCE(s.demo_url, '')
		FROM result_entries re
		JOIN projects p ON re.project_id = p.id
		JOIN tracks t ON p.track_id = t.id
		LEFT JOIN teams tm ON p.team_id = tm.id
		LEFT JOIN submissions s ON p.id = s.project_id AND s.version_no = (
			SELECT MAX(version_no) FROM submissions WHERE project_id = p.id
		)
		WHERE re.result_run_id = ? AND re.project_id = ?;
	`
	var e ResultEntry
	err = s.db.QueryRowContext(ctx, query, runID, projectID).Scan(
		&e.ResultRunID, &e.ProjectID, &e.RawScore, &e.NormalizedScore, &e.FinalScore,
		&e.Rank, &e.TieGroup, &e.ExpectedReviews, &e.CompletedReviews, &e.EffectiveReviews,
		&e.FallbackCount, &e.ExplanationJSON,
		&e.ProjectTitle, &e.ProjectSummary,
		&e.TeamID, &e.TeamName, &e.TrackID, &e.TrackName,
		&e.RepoURL, &e.DemoURL,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, nil, ErrNotFound
		}
		return nil, nil, nil, fmt.Errorf("failed to query project entry: %w", err)
	}

	var payload ExplanationPayload
	if err := json.Unmarshal([]byte(e.ExplanationJSON), &payload); err == nil {
		e.Explanation = &payload
	}

	return run, &e, e.Explanation, nil
}

// ReplayRun performs independent recalculation against source manifest inputs and verifies equality.
func (s *Service) ReplayRun(ctx context.Context, runID string, filterProjectID string) (*ReplayReport, error) {
	run, err := s.GetResultRunByID(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("failed to load run: %w", err)
	}

	var manifest InputManifest
	if err := json.Unmarshal([]byte(run.InputManifestJSON), &manifest); err != nil {
		return nil, fmt.Errorf("failed to unmarshal input manifest: %w", err)
	}

	// 1. Verify Manifest Digest Integrity
	rawBallots := make([]RawBallotRecord, len(manifest.Ballots))
	for i, b := range manifest.Ballots {
		rawBallots[i] = RawBallotRecord{
			AssignmentID:    b.AssignmentID,
			JudgeUserID:     b.JudgeUserID,
			ProjectID:       b.ProjectID,
			RubricVersionID: b.RubricVersionID,
			BallotVersionID: b.BallotVersionID,
			BallotVersionNo: b.BallotVersionNo,
			RawScore:        b.RawScore,
			CriteriaScores:  b.CriteriaScores,
			SubmittedAt:     b.SubmittedAt,
		}
	}

	_, _, recomputedDigest, err := BuildInputManifest(manifest.EventID, manifest.AlgorithmKey, manifest.AlgorithmVersion, rawBallots)
	if err != nil {
		return nil, fmt.Errorf("failed to recompute manifest: %w", err)
	}

	digestMatches := (recomputedDigest == run.InputDigest)

	// 2. Load stored entries
	storedEntries, err := s.ListEntriesForRun(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("failed to load stored entries: %w", err)
	}

	storedMap := make(map[string]ResultEntry, len(storedEntries))
	projects := make([]ProjectInfo, 0, len(storedEntries))
	expectedCounts := make(map[string]int, len(storedEntries))

	for _, se := range storedEntries {
		storedMap[se.ProjectID] = se
		projects = append(projects, ProjectInfo{
			ID:           se.ProjectID,
			EventID:      run.EventID,
			TrackID:      se.TrackID,
			TrackName:    se.TrackName,
			TeamName:     se.TeamName,
			ProjectTitle: se.ProjectTitle,
			RepoURL:      se.RepoURL,
			DemoURL:      se.DemoURL,
		})
		expectedCounts[se.ProjectID] = se.ExpectedReviews
	}

	// 3. Recompute from source ballots
	recomputedEntries, err := ComputeProjectResults(
		runID,
		run.EventID,
		projects,
		rawBallots,
		expectedCounts,
		recomputedDigest,
		run.Status,
		"",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to recompute results: %w", err)
	}

	recomputedMap := make(map[string]ResultEntry, len(recomputedEntries))
	for _, re := range recomputedEntries {
		recomputedMap[re.ProjectID] = re
	}

	// 4. Compare itemized results
	var mismatches []ReplayMismatch
	evaluatedCount := 0

	for _, se := range storedEntries {
		if filterProjectID != "" && se.ProjectID != filterProjectID {
			continue
		}
		evaluatedCount++

		re, ok := recomputedMap[se.ProjectID]
		if !ok {
			mismatches = append(mismatches, ReplayMismatch{
				ProjectID: se.ProjectID,
				Field:     "presence",
				Expected:  "present",
				Actual:    "missing from recomputed results",
			})
			continue
		}

		if math.Abs(se.FinalScore-re.FinalScore) > 1e-4 {
			mismatches = append(mismatches, ReplayMismatch{
				ProjectID: se.ProjectID,
				Field:     "final_score",
				Expected:  fmt.Sprintf("%.2f", se.FinalScore),
				Actual:    fmt.Sprintf("%.2f", re.FinalScore),
			})
		}

		if se.Rank != re.Rank {
			mismatches = append(mismatches, ReplayMismatch{
				ProjectID: se.ProjectID,
				Field:     "rank",
				Expected:  fmt.Sprintf("%d", se.Rank),
				Actual:    fmt.Sprintf("%d", re.Rank),
			})
		}

		if se.TieGroup != re.TieGroup {
			mismatches = append(mismatches, ReplayMismatch{
				ProjectID: se.ProjectID,
				Field:     "tie_group",
				Expected:  fmt.Sprintf("%d", se.TieGroup),
				Actual:    fmt.Sprintf("%d", re.TieGroup),
			})
		}

		if math.Abs(se.RawScore-re.RawScore) > 1e-4 {
			mismatches = append(mismatches, ReplayMismatch{
				ProjectID: se.ProjectID,
				Field:     "raw_score",
				Expected:  fmt.Sprintf("%.2f", se.RawScore),
				Actual:    fmt.Sprintf("%.2f", re.RawScore),
			})
		}
	}

	passed := digestMatches && len(mismatches) == 0

	return &ReplayReport{
		RunID:            runID,
		FilterProjectID:  filterProjectID,
		Passed:           passed,
		ExpectedDigest:   run.InputDigest,
		ActualDigest:     recomputedDigest,
		DigestMatches:    digestMatches,
		EntriesEvaluated: evaluatedCount,
		Mismatches:       mismatches,
	}, nil
}
