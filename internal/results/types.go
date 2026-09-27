package results

const (
	AlgorithmZScoreStandardization = "z_score_standardization"
	AlgorithmVersionV1             = "v1.0.0"

	CohortAllCompleted        = "all_completed"
	TieDeterministicFallback = "deterministic_fallback"

	FallbackNone         = "NONE"
	FallbackLowN         = "FALLBACK_LOW_N"
	FallbackZeroVariance = "FALLBACK_ZERO_VARIANCE"

	StatusDraft     = "DRAFT"
	StatusPublished = "PUBLISHED"
	StatusRetired   = "RETIRED"

	ApprovedFairnessStatement = "This statistical normalization mitigates score-scale differences across judges without altering relative preference within a judge's portfolio."
)

// ResultRun represents an auditable results generation run.
type ResultRun struct {
	ID                    string  `json:"id"`
	EventID               string  `json:"event_id"`
	SupersedesResultRunID *string `json:"supersedes_result_run_id,omitempty"`
	AlgorithmKey          string  `json:"algorithm_key"`
	AlgorithmVersion      string  `json:"algorithm_version"`
	ConfigJSON            string  `json:"config_json"`
	CohortPolicy          string  `json:"cohort_policy"`
	TiePolicy             string  `json:"tie_policy"`
	Status                string  `json:"status"` // DRAFT, PUBLISHED, RETIRED
	InputManifestJSON     string  `json:"input_manifest_json"`
	InputDigest           string  `json:"input_digest"`
	CreatedBy             string  `json:"created_by,omitempty"`
	CreatedAt             string  `json:"created_at"`
	PublishedAt           *string `json:"published_at,omitempty"`
	FailureReason         *string `json:"failure_reason,omitempty"`
}

// ResultEntry represents a project's computed score and derivation receipt in a result run.
type ResultEntry struct {
	ResultRunID      string              `json:"result_run_id"`
	ProjectID        string              `json:"project_id"`
	RawScore         float64             `json:"raw_score"`
	NormalizedScore  float64             `json:"normalized_score"`
	FinalScore       float64             `json:"final_score"`
	Rank             int                 `json:"rank"`
	TieGroup         int                 `json:"tie_group"`
	ExpectedReviews  int                 `json:"expected_reviews"`
	CompletedReviews int                 `json:"completed_reviews"`
	EffectiveReviews int                 `json:"effective_reviews"`
	FallbackCount    int                 `json:"fallback_count"`
	ExplanationJSON  string              `json:"explanation_json"`
	Explanation      *ExplanationPayload `json:"explanation,omitempty"`

	// Enriched view fields
	ProjectTitle   string `json:"project_title,omitempty"`
	ProjectSummary string `json:"project_summary,omitempty"`
	TeamID         string `json:"team_id,omitempty"`
	TrackID        string `json:"track_id,omitempty"`
	TrackName      string `json:"track_name,omitempty"`
	TeamName       string `json:"team_name,omitempty"`
	RepoURL        string `json:"repo_url,omitempty"`
	DemoURL        string `json:"demo_url,omitempty"`
}

// JudgeCohortStats holds summary statistics for a judge's completed evaluation cohort.
type JudgeCohortStats struct {
	JudgeUserID string  `json:"judge_user_id"`
	Count       int     `json:"count"`
	Mean        float64 `json:"mean"`
	Variance    float64 `json:"variance"`
	StdDev      float64 `json:"std_dev"`
	MinScore    float64 `json:"min_score"`
	MaxScore    float64 `json:"max_score"`
}

// BallotAudit records transparent audit details for an individual ballot contribution.
type BallotAudit struct {
	BallotVersionID   string             `json:"ballot_version_id"`
	BallotVersionNo   int                `json:"ballot_version_no"`
	AssignmentID      string             `json:"assignment_id"`
	JudgeUserID       string             `json:"judge_user_id"`
	JudgeName         string             `json:"judge_name"`
	RubricVersionID   string             `json:"rubric_version_id"`
	CriteriaScores    map[string]float64 `json:"criteria_scores"`
	RawScore          float64            `json:"raw_score"`
	JudgeMean         float64            `json:"judge_mean"`
	JudgeStdDev       float64            `json:"judge_std_dev"`
	JudgeBallotCount  int                `json:"judge_ballot_count"`
	ZScore            *float64           `json:"z_score,omitempty"`
	NormalizedScore   float64            `json:"normalized_score"`
	FallbackCode      string             `json:"fallback_code"`
	FallbackReason    string             `json:"fallback_reason"`
	SubmittedAt       string             `json:"submitted_at"`
}

// FormulaBreakdown provides transparent step-by-step arithmetic for the final score.
type FormulaBreakdown struct {
	RawScores         []float64 `json:"raw_scores"`
	RawAverage        float64   `json:"raw_average"`
	NormalizedScores  []float64 `json:"normalized_scores"`
	NormalizedAverage float64   `json:"normalized_average"`
	FinalScore        float64   `json:"final_score"`
	RoundingPolicy    string    `json:"rounding_policy"`
	ApprovedLanguage  string    `json:"approved_language"`
}

// TieBreakAudit records the deterministic tie-breaking rationale.
type TieBreakAudit struct {
	TiePolicy      string `json:"tie_policy"`
	Rank           int    `json:"rank"`
	TieGroup       int    `json:"tie_group"`
	SortOrderRule  string `json:"sort_order_rule"`
}

// ExplanationPayload is the full audit receipt stored in result_entries.explanation_json.
type ExplanationPayload struct {
	RunID            string           `json:"run_id"`
	ProjectID        string           `json:"project_id"`
	ProjectTitle     string           `json:"project_title"`
	TeamName         string           `json:"team_name"`
	TrackID          string           `json:"track_id"`
	TrackName        string           `json:"track_name"`
	RubricVersionID  string           `json:"rubric_version_id"`
	ExpectedReviews  int              `json:"expected_reviews"`
	CompletedReviews int              `json:"completed_reviews"`
	EffectiveReviews int              `json:"effective_reviews"`
	FallbackCount    int              `json:"fallback_count"`
	BallotAudits     []BallotAudit    `json:"ballot_audits"`
	FormulaBreakdown FormulaBreakdown `json:"formula_breakdown"`
	TieBreak         TieBreakAudit    `json:"tie_break"`
	InputDigest      string           `json:"input_digest"`
	RunStatus        string           `json:"run_status"`
	PublishedAt      string           `json:"published_at,omitempty"`
}

// ManifestBallotItem is the canonical representation of a submitted ballot in the input manifest.
type ManifestBallotItem struct {
	AssignmentID    string             `json:"assignment_id"`
	JudgeUserID     string             `json:"judge_user_id"`
	ProjectID       string             `json:"project_id"`
	RubricVersionID string             `json:"rubric_version_id"`
	BallotVersionID string             `json:"ballot_version_id"`
	BallotVersionNo int                `json:"ballot_version_no"`
	RawScore        float64            `json:"raw_score"`
	CriteriaScores  map[string]float64 `json:"criteria_scores"`
	SubmittedAt     string             `json:"submitted_at"`
}

// InputManifest is the complete, canonical list of inputs that produced a result run.
type InputManifest struct {
	EventID          string               `json:"event_id"`
	AlgorithmKey     string               `json:"algorithm_key"`
	AlgorithmVersion string               `json:"algorithm_version"`
	CohortPolicy     string               `json:"cohort_policy"`
	TiePolicy        string               `json:"tie_policy"`
	Config           map[string]any       `json:"config"`
	Ballots          []ManifestBallotItem `json:"ballots"`
}

// ReplayMismatch captures a variance during independent verification.
type ReplayMismatch struct {
	ProjectID string `json:"project_id"`
	Field     string `json:"field"`
	Expected  string `json:"expected"`
	Actual    string `json:"actual"`
}

// ReplayReport represents the result of independent recalculation against source ballots.
type ReplayReport struct {
	RunID            string           `json:"run_id"`
	FilterProjectID  string           `json:"filter_project_id,omitempty"`
	Passed           bool             `json:"passed"`
	ExpectedDigest   string           `json:"expected_digest"`
	ActualDigest     string           `json:"actual_digest"`
	DigestMatches    bool             `json:"digest_matches"`
	EntriesEvaluated int              `json:"entries_evaluated"`
	Mismatches       []ReplayMismatch `json:"mismatches,omitempty"`
}

// ProjectInfo provides metadata for a project during score aggregation.
type ProjectInfo struct {
	ID           string
	EventID      string
	TrackID      string
	TrackName    string
	TeamName     string
	ProjectTitle string
	ProjectSummary string
	RepoURL      string
	DemoURL      string
}

// RawBallotRecord provides database ballot data for aggregation.
type RawBallotRecord struct {
	AssignmentID    string
	JudgeUserID     string
	JudgeName       string
	ProjectID       string
	RubricVersionID string
	BallotVersionID string
	BallotVersionNo int
	RawScore        float64
	CriteriaScores  map[string]float64
	SubmittedAt     string
}
