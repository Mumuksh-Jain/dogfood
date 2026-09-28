package httpapp

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"dogfood/internal/assignment"
	"dogfood/internal/auth"
	"dogfood/internal/judging"
	"dogfood/internal/results"
	"dogfood/web"
)

// Config configures the HTTP server.
type Config struct {
	Port int
	DB   *sql.DB
}

// Server encapsulates the HTTP server and routing.
type Server struct {
	httpServer        *http.Server
	listener          net.Listener
	db                *sql.DB
	tmpl              *template.Template
	judgingService    *judging.Service
	assignmentService *assignment.Service
	resultsService    *results.Service
}

// ProjectView holds project data for UI and API representations.
type ProjectView struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Summary       string `json:"summary"`
	TeamID        string `json:"team_id"`
	TrackID       string `json:"track_id"`
	TrackName     string `json:"track_name"`
	RepoURL       string `json:"repo_url"`
	DemoURL       string `json:"demo_url"`
	SubmittedAt   string `json:"submitted_at"`
	FormattedDate string `json:"formatted_date"`
	State         string `json:"state"`
	VersionNo     int    `json:"version_no"`
}

// PrizeView holds prize metadata for UI and API representations.
type PrizeView struct {
	ID          string `json:"id"`
	EventID     string `json:"event_id"`
	TrackID     string `json:"track_id"`
	TrackName   string `json:"track_name"`
	Name        string `json:"name"`
	Description string `json:"description"`
	AmountText  string `json:"amount_text"`
	CreatedAt   string `json:"created_at"`
}

// TrackCount holds track metadata with project count.
type TrackCount struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// GalleryData is passed to the gallery HTML template.
type GalleryData struct {
	EventName       string
	EventID         string
	SubmissionsOpen bool
	DeadlineText    string
	Projects        []ProjectView
	Tracks          []TrackCount
	Prizes          []PrizeView
	TotalCount      int
	User            *auth.Identity
}

// DemoAccount holds persona info for instant human demo authentication.
type DemoAccount struct {
	Key         string `json:"key"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Purpose     string `json:"purpose"`
}

// LoginData is passed to the login HTML template.
type LoginData struct {
	EventName    string
	Error        string
	Message      string
	ReturnTo     string
	DemoAccounts []DemoAccount
}

// TeamMemberView holds team member details.
type TeamMemberView struct {
	UserID      string
	DisplayName string
	Email       string
	Role        string
	JoinedAt    string
}

// TeamInviteView holds team invite status.
type TeamInviteView struct {
	ID         string
	TokenHash  string
	CreatedAt  string
	ExpiresAt  string
	AcceptedAt string
	AcceptedBy string
	RevokedAt  string
}

// TeamView holds team summary.
type TeamView struct {
	ID   string
	Name string
}

// OrganizerTeamView holds summary of a team for organizer overview.
type OrganizerTeamView struct {
	ID          string
	Name        string
	MemberCount int
	LeadName    string
	CreatedAt   string
}

// TeamJoinData is passed to team_join.html template.
type TeamJoinData struct {
	EventName       string
	Token           string
	TeamID          string
	TeamName        string
	MemberCount     int
	CreatorName     string
	ExpiresAt       string
	FormattedExpiry string
	User            *auth.Identity
	Error           string
}

// DashboardData is passed to the dashboard template.
type DashboardData struct {
	EventName          string
	EventID            string
	SubmissionsOpen    bool
	SubmissionsCloseAt string
	FormattedDeadline  string
	User               *auth.Identity
	IsOrganizer        bool
	IsAdmin            bool
	IsJudge            bool
	IsParticipant      bool

	// Participant workspace
	Team             *TeamView
	TeamMembers      []TeamMemberView
	TeamInvites      []TeamInviteView
	TeamProjects     []ProjectView
	CurrentInviteURL string

	// Organizer workspace
	AllTeams           []OrganizerTeamView
	AllProjects        []ProjectView
	Tracks             []TrackCount
	Prizes             []PrizeView
	Rubrics            []judging.RubricVersion
	AssignmentRunCount int
	TotalAssignments   int

	// Organizer Judging Control Room & Progress
	JudgingProgress *OrganizerJudgingProgress
	JudgeProgress   []OrganizerJudgeProgressView
	ProjectCoverage []OrganizerProjectCoverageView
	TrackProgress   []OrganizerTrackProgressView
	AllJudges       []OrganizerJudgeRosterView

	// Judge workspace
	JudgeAssignments []judging.AssignmentDetail

	// Results & Leaderboard status
	ResultsPublished    bool
	ActiveResultRunID   string
	PendingBallotCount  int
	DraftResultRunCount int

	Message string
	Error   string
}

// OrganizerJudgingProgress aggregates overall evaluation metrics for the event.
type OrganizerJudgingProgress struct {
	TotalAssignments      int     `json:"total_assignments"`
	CompletedAssignments  int     `json:"completed_assignments"`
	InProgressAssignments int     `json:"in_progress_assignments"`
	UnstartedAssignments  int     `json:"unstarted_assignments"`
	CompletionRate        float64 `json:"completion_rate"`
	TotalJudges           int     `json:"total_judges"`
	ActiveJudges          int     `json:"active_judges"`
	CompletedJudges       int     `json:"completed_judges"`
	TotalProjects         int     `json:"total_projects"`
	FullyReviewedProjects int     `json:"fully_reviewed_projects"`
}

// OrganizerJudgeProgressView represents real-time progress for an individual judge.
type OrganizerJudgeProgressView struct {
	JudgeUserID    string   `json:"judge_user_id"`
	DisplayName    string   `json:"display_name"`
	Email          string   `json:"email"`
	Capacity       int      `json:"capacity"`
	Active         bool     `json:"active"`
	AssignedCount  int      `json:"assigned_count"`
	CompletedCount int      `json:"completed_count"`
	DraftCount     int      `json:"draft_count"`
	UnstartedCount int      `json:"unstarted_count"`
	EligibleTracks []string `json:"eligible_tracks"`
	Status         string   `json:"status"` // "Completed", "In Progress", "Not Started"
}

// OrganizerProjectCoverageView represents evaluation coverage for a single project submission.
type OrganizerProjectCoverageView struct {
	ProjectID        string  `json:"project_id"`
	Title            string  `json:"title"`
	TeamName         string  `json:"team_name"`
	TrackName        string  `json:"track_name"`
	TargetReviews    int     `json:"target_reviews"`
	AssignedReviews  int     `json:"assigned_reviews"`
	CompletedReviews int     `json:"completed_reviews"`
	CoveragePercent  float64 `json:"coverage_percent"`
	Status           string  `json:"status"` // "Fully Reviewed", "Partially Reviewed", "Pending"
}

// OrganizerTrackProgressView tracks judging completion per track.
type OrganizerTrackProgressView struct {
	TrackID          string  `json:"track_id"`
	TrackName        string  `json:"track_name"`
	ProjectCount     int     `json:"project_count"`
	TotalAssignments int     `json:"total_assignments"`
	CompletedReviews int     `json:"completed_reviews"`
	CompletionRate   float64 `json:"completion_rate"`
}

// OrganizerJudgeRosterView represents judge configuration and management.
type OrganizerJudgeRosterView struct {
	UserID         string   `json:"user_id"`
	DisplayName    string   `json:"display_name"`
	Email          string   `json:"email"`
	Capacity       int      `json:"capacity"`
	Active         bool     `json:"active"`
	InvitedAt      string   `json:"invited_at"`
	EligibleTracks []string `json:"eligible_tracks"`
	AssignedCount  int      `json:"assigned_count"`
	CompletedCount int      `json:"completed_count"`
}

// EvaluationData is passed to evaluation.html template.
type EvaluationData struct {
	EventName    string
	User         *auth.Identity
	Assignment   *judging.AssignmentDetail
	Rubric       *judging.RubricVersion
	LatestBallot *judging.BallotVersion
	IsSubmitted  bool
	Message      string
	Error        string
}

// ProjectDetailData is passed to project_detail.html.
type ProjectDetailData struct {
	EventName       string
	SubmissionsOpen bool
	Project         ProjectView
	CanEdit         bool
	User            *auth.Identity
}

// ResultsData is passed to results.html template.
type ResultsData struct {
	EventName          string
	EventID            string
	Run                *results.ResultRun
	Entries            []results.ResultEntry
	Tracks             []TrackCount
	User               *auth.Identity
	IsOrganizer        bool
	Message            string
	Error              string
	DraftRun           *results.ResultRun
	IsDraftPreview     bool
	HasPendingBallots  bool
	PendingBallotCount int
	HasPublishedRun    bool
}

// ExplainRankData is passed to explain_rank.html template.
type ExplainRankData struct {
	EventName   string
	EventID     string
	Run         *results.ResultRun
	Entry       *results.ResultEntry
	Explanation *results.ExplanationPayload
	User        *auth.Identity
	Message     string
	Error       string
}

func getDemoAccounts() []DemoAccount {
	return []DemoAccount{
		{
			Key:         "participant_a",
			Email:       "participant_a@example.org",
			DisplayName: "Alex Chen (Participant A)",
			Role:        "Participant",
			Purpose:     "Create a team and project",
		},
		{
			Key:         "participant_b",
			Email:       "participant_b@example.org",
			DisplayName: "Blair Taylor (Participant B)",
			Role:        "Participant",
			Purpose:     "Join another participant's team",
		},
		{
			Key:         "organizer",
			Email:       "organizer@example.org",
			DisplayName: "Alex Rivera (Organizer)",
			Role:        "Organizer",
			Purpose:     "Manage event",
		},
		{
			Key:         "judge_a",
			Email:       "tomas.varga@example.org",
			DisplayName: "Tomas Varga (Judge A)",
			Role:        "Judge",
			Purpose:     "Judging workspace",
		},
		{
			Key:         "judge_b",
			Email:       "wei.lindqvist@example.org",
			DisplayName: "Wei Lindqvist (Judge B)",
			Role:        "Judge",
			Purpose:     "Judging workspace",
		},
	}
}

// NewServer builds and initializes the application router and HTTP server.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Port <= 0 {
		cfg.Port = 8080
	}

	tmplFS, err := web.TemplatesFS()
	if err != nil {
		return nil, fmt.Errorf("failed to get templates fs: %w", err)
	}

	tmpl, err := template.New("").Funcs(template.FuncMap{
		"lower": strings.ToLower,
		"add": func(a, b int) int {
			return a + b
		},
		"scoreFor": func(ballot *judging.BallotVersion, critID string) string {
			if ballot == nil || ballot.CriteriaScores == nil {
				return ""
			}
			if v, ok := ballot.CriteriaScores[critID]; ok {
				return fmt.Sprintf("%.1f", v)
			}
			return ""
		},
		"scoreValue": func(ballot *judging.BallotVersion, critID string) float64 {
			if ballot == nil || ballot.CriteriaScores == nil {
				return 0
			}
			return ballot.CriteriaScores[critID]
		},
	}).ParseFS(tmplFS, "*.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse templates: %w", err)
	}

	staticFS, err := web.StaticFS()
	if err != nil {
		return nil, fmt.Errorf("failed to get static fs: %w", err)
	}

	s := &Server{
		db:                cfg.DB,
		tmpl:              tmpl,
		judgingService:    judging.NewService(cfg.DB),
		assignmentService: assignment.NewService(cfg.DB),
		resultsService:    results.NewService(cfg.DB),
	}

	mux := http.NewServeMux()

	// 1. Health check endpoint
	mux.HandleFunc("GET /healthz", s.handleHealthz)

	// 2. Static files
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	// 3. Public project gallery
	mux.HandleFunc("GET /{$}", s.handleGallery)
	mux.HandleFunc("GET /projects", s.handleGallery)
	mux.HandleFunc("GET /projects/{id}", s.handleProjectDetail)

	// 4. Auth & Session UX
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("GET /logout", s.handleLogout)
	mux.HandleFunc("POST /logout", s.handleLogout)

	// 5. Dashboard
	mux.HandleFunc("GET /dashboard", s.handleDashboard)

	// 6. Submission endpoints
	mux.HandleFunc("POST /projects/new", s.handleSubmit) // compatibility with run.py probe
	mux.HandleFunc("POST /api/projects", s.handleCreateProject)
	mux.HandleFunc("POST /api/v1/projects", s.handleCreateProject)
	mux.HandleFunc("GET /api/projects", s.handleListProjectsAPI)
	mux.HandleFunc("GET /api/v1/projects", s.handleListProjectsAPI)
	mux.HandleFunc("POST /api/projects/{id}/edit", s.handleEditProject)
	mux.HandleFunc("POST /api/v1/projects/{id}/edit", s.handleEditProject)
	mux.HandleFunc("POST /api/projects/{id}/submit", s.handleSubmitProject)
	mux.HandleFunc("POST /api/v1/projects/{id}/submit", s.handleSubmitProject)
	mux.HandleFunc("GET /api/projects/{id}", s.handleGetProjectAPI)
	mux.HandleFunc("GET /api/v1/projects/{id}", s.handleGetProjectAPI)

	// 7. Teams & Invites
	mux.HandleFunc("GET /api/teams", s.handleListTeamsAPI)
	mux.HandleFunc("GET /api/v1/teams", s.handleListTeamsAPI)
	mux.HandleFunc("POST /api/teams", s.handleCreateTeam)
	mux.HandleFunc("POST /api/v1/teams", s.handleCreateTeam)
	mux.HandleFunc("POST /api/teams/{id}/invites", s.handleCreateInvite)
	mux.HandleFunc("POST /api/v1/teams/{id}/invites", s.handleCreateInvite)
	mux.HandleFunc("GET /teams/join", s.handleJoinTeam)
	mux.HandleFunc("POST /teams/join", s.handleJoinTeam)
	mux.HandleFunc("POST /api/teams/join", s.handleJoinTeam)
	mux.HandleFunc("POST /api/v1/teams/join", s.handleJoinTeam)

	// 8. Event configuration (organizer / admin)
	mux.HandleFunc("POST /api/organizer/events", s.handleCreateEvent)
	mux.HandleFunc("POST /api/v1/organizer/events", s.handleCreateEvent)
	mux.HandleFunc("POST /api/events", s.handleCreateEvent)
	mux.HandleFunc("POST /api/v1/events", s.handleCreateEvent)
	mux.HandleFunc("POST /api/organizer/event", s.handleUpdateEvent)
	mux.HandleFunc("POST /api/v1/organizer/event", s.handleUpdateEvent)
	mux.HandleFunc("GET /api/tracks", s.handleGetTracks)
	mux.HandleFunc("GET /api/v1/tracks", s.handleGetTracks)
	mux.HandleFunc("POST /api/organizer/tracks", s.handleCreateTrack)
	mux.HandleFunc("POST /api/v1/organizer/tracks", s.handleCreateTrack)
	mux.HandleFunc("GET /api/prizes", s.handleGetPrizes)
	mux.HandleFunc("GET /api/v1/prizes", s.handleGetPrizes)
	mux.HandleFunc("POST /api/organizer/prizes", s.handleCreatePrize)
	mux.HandleFunc("POST /api/v1/organizer/prizes", s.handleCreatePrize)

	// 9. Judge score routes (peer isolation & role enforcement)
	mux.HandleFunc("GET /api/judge/scores", s.handleJudgeScores)
	mux.HandleFunc("GET /api/v1/judge/scores", s.handleJudgeScores)

	// 10. CSV export routes (organizer role enforcement & formula defense)
	mux.HandleFunc("GET /api/export.csv", s.handleCSVExport)
	mux.HandleFunc("GET /api/v1/export.csv", s.handleCSVExport)
	mux.HandleFunc("GET /api/export/results.csv", s.handleCSVExport)
	mux.HandleFunc("GET /api/v1/export/results.csv", s.handleCSVExport)
	mux.HandleFunc("GET /api/export/ballots.csv", s.handleCSVExport)
	mux.HandleFunc("GET /api/v1/export/ballots.csv", s.handleCSVExport)
	mux.HandleFunc("GET /api/export/evaluations.csv", s.handleCSVExport)
	mux.HandleFunc("GET /api/v1/export/evaluations.csv", s.handleCSVExport)
	mux.HandleFunc("GET /api/export/projects.csv", s.handleCSVExport)
	mux.HandleFunc("GET /api/v1/export/projects.csv", s.handleCSVExport)

	// 11. Evaluations & Rubrics (T2 judging lifecycle)
	mux.HandleFunc("GET /evaluations/{id}", s.handleEvaluationPage)
	mux.HandleFunc("POST /evaluations/{id}", s.handleEvaluationSubmit)
	mux.HandleFunc("GET /api/assignments/{id}/ballot", s.handleGetBallotAPI)
	mux.HandleFunc("GET /api/v1/assignments/{id}/ballot", s.handleGetBallotAPI)
	mux.HandleFunc("POST /api/assignments/{id}/ballot", s.handleSaveDraftBallotAPI)
	mux.HandleFunc("POST /api/v1/assignments/{id}/ballot", s.handleSaveDraftBallotAPI)
	mux.HandleFunc("POST /api/assignments/{id}/ballot/submit", s.handleSubmitBallotAPI)
	mux.HandleFunc("POST /api/v1/assignments/{id}/ballot/submit", s.handleSubmitBallotAPI)
	mux.HandleFunc("GET /api/events/{event_id}/rubric", s.handleGetRubricAPI)
	mux.HandleFunc("GET /api/v1/events/{event_id}/rubric", s.handleGetRubricAPI)
	mux.HandleFunc("POST /api/events/{event_id}/rubrics", s.handleCreateRubricDraftAPI)
	mux.HandleFunc("POST /api/v1/events/{event_id}/rubrics", s.handleCreateRubricDraftAPI)
	mux.HandleFunc("POST /api/rubrics/{rubric_version_id}/publish", s.handlePublishRubricAPI)
	mux.HandleFunc("POST /api/v1/rubrics/{rubric_version_id}/publish", s.handlePublishRubricAPI)
	mux.HandleFunc("POST /api/organizer/assignments/run", s.handleRunAssignments)
	mux.HandleFunc("POST /api/v1/organizer/assignments/run", s.handleRunAssignments)
 
	// 12. Results, Leaderboard & Explain-This-Rank Auditability
	mux.HandleFunc("GET /results", s.handleResultsPage)
	mux.HandleFunc("GET /results/{run_id}/projects/{project_id}", s.handleExplainRankPage)
	mux.HandleFunc("GET /results/{run_id}/explain/{project_id}", s.handleExplainRankPage)
	mux.HandleFunc("POST /api/organizer/results/compute", s.handleComputeResults)
	mux.HandleFunc("POST /api/v1/organizer/results/compute", s.handleComputeResults)
	mux.HandleFunc("POST /api/organizer/results/{id}/publish", s.handlePublishResults)
	mux.HandleFunc("POST /api/v1/organizer/results/{id}/publish", s.handlePublishResults)
	mux.HandleFunc("POST /api/organizer/results/recompute-publish", s.handleRecomputeAndPublishResults)
	mux.HandleFunc("POST /api/v1/organizer/results/recompute-publish", s.handleRecomputeAndPublishResults)
	mux.HandleFunc("GET /api/results", s.handleGetActiveResultsAPI)
	mux.HandleFunc("GET /api/v1/results", s.handleGetActiveResultsAPI)
	mux.HandleFunc("GET /api/results/{run_id}/projects/{project_id}", s.handleGetExplanationAPI)
	mux.HandleFunc("GET /api/v1/results/{run_id}/projects/{project_id}", s.handleGetExplanationAPI)
	mux.HandleFunc("GET /api/v1/results/{run_id}/explain/{project_id}", s.handleGetExplanationAPI)
	mux.HandleFunc("GET /api/results/{run_id}/replay", s.handleReplayAPI)
	mux.HandleFunc("GET /api/v1/results/{run_id}/replay", s.handleReplayAPI)
	mux.HandleFunc("POST /api/results/{run_id}/replay", s.handleReplayAPI)
	mux.HandleFunc("POST /api/v1/results/{run_id}/replay", s.handleReplayAPI)
	mux.HandleFunc("GET /api/results/{run_id}/replay/{project_id}", s.handleReplayProjectAPI)
	mux.HandleFunc("GET /api/v1/results/{run_id}/replay/{project_id}", s.handleReplayProjectAPI)

	// 13. Organizer Judging Operations & Progress Control Room
	mux.HandleFunc("GET /api/organizer/progress", s.handleOrganizerProgressAPI)
	mux.HandleFunc("GET /api/v1/organizer/progress", s.handleOrganizerProgressAPI)
	mux.HandleFunc("POST /api/organizer/judges", s.handleOrganizerCreateJudgeAPI)
	mux.HandleFunc("POST /api/v1/organizer/judges", s.handleOrganizerCreateJudgeAPI)
	mux.HandleFunc("POST /api/organizer/judges/{id}/eligibility", s.handleOrganizerUpdateJudgeEligibilityAPI)
	mux.HandleFunc("POST /api/v1/organizer/judges/{id}/eligibility", s.handleOrganizerUpdateJudgeEligibilityAPI)
	mux.HandleFunc("POST /api/organizer/rubrics", s.handleOrganizerCreateRubricAPI)
	mux.HandleFunc("POST /api/v1/organizer/rubrics", s.handleOrganizerCreateRubricAPI)

	addr := fmt.Sprintf(":%d", cfg.Port)
	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	if s.db != nil {
		_ = s.ensureDemoUsers(context.Background())
	}

	return s, nil
}

func (s *Server) ensureDemoUsers(ctx context.Context) error {
	if s.db == nil {
		return nil
	}
	var eventID string
	err := s.db.QueryRowContext(ctx, "SELECT id FROM events ORDER BY created_at ASC LIMIT 1;").Scan(&eventID)
	if err != nil {
		return nil
	}

	demos := []struct {
		id          string
		email       string
		displayName string
		roles       []string
	}{
		{"usr_organizer", "organizer@example.org", "Alex Rivera (Organizer)", []string{"organizer", "admin"}},
		{"jdg_01", "tomas.varga@example.org", "Tomas Varga (Judge A)", []string{"judge"}},
		{"jdg_02", "wei.lindqvist@example.org", "Wei Lindqvist (Judge B)", []string{"judge"}},
		{"usr_demo_part_a", "participant_a@example.org", "Alex Chen (Participant A)", []string{"participant"}},
		{"usr_demo_part_b", "participant_b@example.org", "Blair Taylor (Participant B)", []string{"participant"}},
	}

	for _, d := range demos {
		_ = auth.EnsureUser(ctx, s.db, d.id, d.email, d.displayName)
		for _, r := range []string{"participant", "organizer", "admin", "judge"} {
			desired := false
			for _, dr := range d.roles {
				if dr == r {
					desired = true
					break
				}
			}
			if desired {
				_ = auth.EnsureRole(ctx, s.db, eventID, d.id, r)
			} else {
				_, _ = s.db.ExecContext(ctx, "DELETE FROM event_roles WHERE user_id = ? AND role = ?;", d.id, r)
			}
		}
	}

	// Purge non-participant demo accounts from team memberships if contaminated in previous manual tests
	_, _ = s.db.ExecContext(ctx, "DELETE FROM team_memberships WHERE user_id IN ('usr_organizer', 'jdg_01', 'jdg_02');")

	for token := range auth.ProtectedAcceptanceTokens {
		tokenHash := auth.HashToken(token)
		_, _ = s.db.ExecContext(ctx, "UPDATE sessions SET revoked_at = NULL WHERE token_hash = ?;", tokenHash)
	}

	// Ensure default rubric is published for the event
	if eventID != "" && s.judgingService != nil {
		_, _ = s.judgingService.EnsureDefaultRubric(ctx, eventID, "usr_organizer")
	}

	// Ensure judge profiles and track eligibility exist for event judges
	if eventID != "" {
		nowStr := time.Now().UTC().Format(time.RFC3339)
		judgeRows, err := s.db.QueryContext(ctx, "SELECT user_id FROM event_roles WHERE event_id = ? AND role = 'judge';", eventID)
		if err == nil {
			var judgeUIDs []string
			for judgeRows.Next() {
				var juid string
				if err := judgeRows.Scan(&juid); err == nil {
					judgeUIDs = append(judgeUIDs, juid)
				}
			}
			judgeRows.Close()

			for _, juid := range judgeUIDs {
				_, _ = s.db.ExecContext(ctx, `
					INSERT INTO judge_profiles (event_id, user_id, capacity, active, invited_at, accepted_at)
					VALUES (?, ?, 10, 1, ?, ?)
					ON CONFLICT(event_id, user_id) DO UPDATE SET active = 1, capacity = 10;
				`, eventID, juid, nowStr, nowStr)

				var trackCount int
				_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM judge_track_eligibility WHERE event_id = ? AND judge_user_id = ?;", eventID, juid).Scan(&trackCount)
				if trackCount == 0 {
					_, _ = s.db.ExecContext(ctx, `
						INSERT INTO judge_track_eligibility (event_id, judge_user_id, track_id, eligible, source, updated_at)
						SELECT ?, ?, id, 1, 'auto', ? FROM tracks WHERE event_id = ?
						ON CONFLICT(event_id, judge_user_id, track_id) DO NOTHING;
					`, eventID, juid, nowStr, eventID)
				}
			}
		}
	}

	// Auto-seed assignments if none exist yet for the event
	if eventID != "" && s.assignmentService != nil {
		var assignmentCount int
		_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM assignments WHERE event_id = ?;", eventID).Scan(&assignmentCount)
		if assignmentCount == 0 {
			_, _ = s.assignmentService.GenerateAssignments(ctx, assignment.Config{
				EventID:       eventID,
				TargetReviews: 3,
				CreatedBy:     "usr_organizer",
			})
		}
	}

	return nil
}

// Handler returns the underlying http.Handler for testing.
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

// Start listens and serves traffic.
func (s *Server) Start() error {
	var err error
	s.listener, err = net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.httpServer.Addr, err)
	}
	return s.httpServer.Serve(s.listener)
}

// Addr returns the network address the server is listening on.
func (s *Server) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.httpServer.Addr
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func formatDate(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	return t.UTC().Format("Jan 02, 2006, 15:04 UTC")
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(b))
}

func canAdminister(id *auth.Identity) bool {
	if id == nil {
		return false
	}
	return id.HasRole("admin") || id.HasRole("organizer")
}

func validateDateOrder(openStr, closeStr, label string) error {
	var openT, closeT time.Time
	var hasOpen, hasClose bool
	if strings.TrimSpace(openStr) != "" {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(openStr))
		if err != nil {
			return fmt.Errorf("invalid %s open timestamp format, expected RFC3339", label)
		}
		openT = t
		hasOpen = true
	}
	if strings.TrimSpace(closeStr) != "" {
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(closeStr))
		if err != nil {
			return fmt.Errorf("invalid %s close timestamp format, expected RFC3339", label)
		}
		closeT = t
		hasClose = true
	}
	if hasOpen && hasClose && openT.After(closeT) {
		return fmt.Errorf("%s open time cannot be after close time", label)
	}
	return nil
}

func (s *Server) getCurrentUser(r *http.Request) *auth.Identity {
	if s.db == nil {
		return nil
	}
	id, err := auth.Authenticate(r.Context(), s.db, r)
	if err != nil {
		return nil
	}
	return id
}

func (s *Server) isSubmissionOpen(ctx context.Context) (bool, string, error) {
	if s.db == nil {
		return true, "", nil
	}
	var openTimeStr, closeTimeStr sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT submissions_open_at, submissions_close_at FROM events ORDER BY created_at ASC LIMIT 1;").Scan(&openTimeStr, &closeTimeStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return true, "", nil
		}
		return false, "", err
	}
	now := time.Now().UTC()
	if openTimeStr.Valid && openTimeStr.String != "" {
		if openT, err := time.Parse(time.RFC3339, openTimeStr.String); err == nil {
			if now.Before(openT) {
				return false, "Submissions open at " + formatDate(openTimeStr.String), nil
			}
		}
	}
	if closeTimeStr.Valid && closeTimeStr.String != "" {
		if closeT, err := time.Parse(time.RFC3339, closeTimeStr.String); err == nil {
			if now.After(closeT) {
				return false, "Submissions closed at " + formatDate(closeTimeStr.String), nil
			}
		}
	}
	return true, "", nil
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if s.db != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.db.PingContext(ctx); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status": "unhealthy",
				"error":  err.Error(),
			})
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
	})
}

func (s *Server) handleGallery(w http.ResponseWriter, r *http.Request) {
	data := GalleryData{
		EventName:       "Dogfood 2026",
		SubmissionsOpen: true,
		User:            s.getCurrentUser(r),
	}

	if s.db != nil {
		var closeTimeStr string
		_ = s.db.QueryRowContext(r.Context(), "SELECT id, name, submissions_close_at FROM events ORDER BY created_at ASC LIMIT 1;").Scan(&data.EventID, &data.EventName, &closeTimeStr)
		if closeTimeStr != "" {
			if closeTime, err := time.Parse(time.RFC3339, closeTimeStr); err == nil {
				data.SubmissionsOpen = time.Now().UTC().Before(closeTime)
				data.DeadlineText = formatDate(closeTimeStr)
			}
		}

		// Fetch tracks with counts
		trackRows, err := s.db.QueryContext(r.Context(), `
			SELECT t.id, t.name, COUNT(p.id)
			FROM tracks t
			LEFT JOIN projects p ON t.id = p.track_id
			GROUP BY t.id, t.name
			ORDER BY t.name ASC;
		`)
		if err == nil {
			defer trackRows.Close()
			for trackRows.Next() {
				var tc TrackCount
				if err := trackRows.Scan(&tc.ID, &tc.Name, &tc.Count); err == nil {
					data.Tracks = append(data.Tracks, tc)
				}
			}
		}

		// Fetch prizes
		przRows, err := s.db.QueryContext(r.Context(), `
			SELECT p.id, p.event_id, COALESCE(p.track_id, ''), COALESCE(t.name, 'All Tracks'), p.name, p.description, p.amount_text, p.created_at
			FROM prizes p
			LEFT JOIN tracks t ON p.track_id = t.id
			ORDER BY p.name ASC;
		`)
		if err == nil {
			defer przRows.Close()
			for przRows.Next() {
				var prz PrizeView
				if err := przRows.Scan(&prz.ID, &prz.EventID, &prz.TrackID, &prz.TrackName, &prz.Name, &prz.Description, &prz.AmountText, &prz.CreatedAt); err == nil {
					data.Prizes = append(data.Prizes, prz)
				}
			}
		}

		// Fetch projects
		query := `
		SELECT p.id, s.title, s.summary, p.team_id, p.track_id, t.name, COALESCE(s.repo_url, ''), COALESCE(s.demo_url, ''), s.submitted_at
		FROM projects p
		JOIN submissions s ON p.id = s.project_id AND s.state = 'SUBMITTED'
		JOIN tracks t ON p.track_id = t.id
		ORDER BY p.id ASC;`

		rows, err := s.db.QueryContext(r.Context(), query)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var pv ProjectView
				if err := rows.Scan(&pv.ID, &pv.Title, &pv.Summary, &pv.TeamID, &pv.TrackID, &pv.TrackName, &pv.RepoURL, &pv.DemoURL, &pv.SubmittedAt); err == nil {
					pv.FormattedDate = formatDate(pv.SubmittedAt)
					data.Projects = append(data.Projects, pv)
				}
			}
		}
	}

	data.TotalCount = len(data.Projects)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := s.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
}

func (s *Server) handleProjectDetail(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if projectID == "" {
		http.NotFound(w, r)
		return
	}

	user := s.getCurrentUser(r)
	open, _, _ := s.isSubmissionOpen(r.Context())

	var pv ProjectView
	query := `
	SELECT p.id, p.team_id, p.track_id, t.name, s.title, s.summary, COALESCE(s.repo_url, ''), COALESCE(s.demo_url, ''), s.state, s.version_no, COALESCE(s.submitted_at, s.created_at)
	FROM projects p
	JOIN tracks t ON p.track_id = t.id
	JOIN submissions s ON p.id = s.project_id
	WHERE p.id = ?
	ORDER BY s.version_no DESC LIMIT 1;`

	err := s.db.QueryRowContext(r.Context(), query, projectID).Scan(
		&pv.ID, &pv.TeamID, &pv.TrackID, &pv.TrackName, &pv.Title, &pv.Summary, &pv.RepoURL, &pv.DemoURL, &pv.State, &pv.VersionNo, &pv.SubmittedAt,
	)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pv.FormattedDate = formatDate(pv.SubmittedAt)

	// Check access permissions:
	// If project is DRAFT, only team members or admins/organizers can view
	canEdit := false
	if user != nil {
		if canAdminister(user) {
			canEdit = true
		} else {
			var isMember int
			_ = s.db.QueryRowContext(r.Context(), `SELECT 1 FROM team_memberships WHERE team_id = ? AND user_id = ? AND left_at IS NULL;`, pv.TeamID, user.UserID).Scan(&isMember)
			if isMember == 1 {
				canEdit = true
			}
		}
	}

	if pv.State == "DRAFT" && !canEdit {
		http.Error(w, "forbidden: private draft", http.StatusForbidden)
		return
	}

	data := ProjectDetailData{
		EventName:       "Dogfood 2026",
		SubmissionsOpen: open,
		Project:         pv,
		CanEdit:         canEdit,
		User:            user,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.tmpl.ExecuteTemplate(w, "project_detail.html", data)
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	returnTo := strings.TrimSpace(r.URL.Query().Get("return_to"))
	if user := s.getCurrentUser(r); user != nil {
		if returnTo != "" && strings.HasPrefix(returnTo, "/") {
			http.Redirect(w, r, returnTo, http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	data := LoginData{
		EventName:    "Dogfood 2026",
		DemoAccounts: getDemoAccounts(),
		ReturnTo:     returnTo,
		Message:      r.URL.Query().Get("msg"),
		Error:        r.URL.Query().Get("error"),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.tmpl.ExecuteTemplate(w, "login.html", data)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	_ = r.ParseForm()
	tokenInput := strings.TrimSpace(r.FormValue("token"))
	emailInput := strings.TrimSpace(r.FormValue("email"))
	demoUser := strings.TrimSpace(r.FormValue("demo_user"))
	returnTo := strings.TrimSpace(r.FormValue("return_to"))

	if demoUser != "" {
		for _, da := range getDemoAccounts() {
			if da.Key == demoUser {
				emailInput = da.Email
				break
			}
		}
	}

	var sessionToken string

	if tokenInput != "" {
		// Quick evaluation token login
		tokenHash := auth.HashToken(tokenInput)
		nowUTC := time.Now().UTC().Format(time.RFC3339)
		var uid string
		err := s.db.QueryRowContext(r.Context(), `SELECT user_id FROM sessions WHERE token_hash = ? AND revoked_at IS NULL AND expires_at > ?;`, tokenHash, nowUTC).Scan(&uid)
		if err != nil {
			errRedirect := "/login?error=Invalid+evaluation+token"
			if returnTo != "" {
				errRedirect += "&return_to=" + url.QueryEscape(returnTo)
			}
			http.Redirect(w, r, errRedirect, http.StatusSeeOther)
			return
		}
		sessionToken = tokenInput
	} else if emailInput != "" {
		var err error
		sessionToken, _, err = auth.LoginUser(r.Context(), s.db, emailInput, 7*24*time.Hour)
		if err != nil {
			errRedirect := "/login?error=" + url.QueryEscape(err.Error())
			if returnTo != "" {
				errRedirect += "&return_to=" + url.QueryEscape(returnTo)
			}
			http.Redirect(w, r, errRedirect, http.StatusSeeOther)
			return
		}
	} else {
		errRedirect := "/login?error=Please+enter+email+or+select+a+demo+persona"
		if returnTo != "" {
			errRedirect += "&return_to=" + url.QueryEscape(returnTo)
		}
		http.Redirect(w, r, errRedirect, http.StatusSeeOther)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7 * 86400,
	})

	target := "/dashboard"
	if returnTo != "" && strings.HasPrefix(returnTo, "/") {
		target = returnTo
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s.db != nil {
		token := auth.ExtractToken(r)
		if token != "" {
			_ = auth.RevokeSession(r.Context(), s.db, token)
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})

	http.Redirect(w, r, "/login?msg=Signed+out+successfully", http.StatusSeeOther)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	isParticipant := user.HasRole("participant")
	if !isParticipant && s.db != nil {
		var hasMembership int
		_ = s.db.QueryRowContext(r.Context(), `SELECT 1 FROM team_memberships WHERE user_id = ? AND left_at IS NULL LIMIT 1;`, user.UserID).Scan(&hasMembership)
		if hasMembership == 1 {
			isParticipant = true
		}
	}

	data := DashboardData{
		EventName:     "Dogfood 2026",
		User:          user,
		IsOrganizer:   user.HasRole("organizer"),
		IsAdmin:       user.HasRole("admin"),
		IsJudge:       user.HasRole("judge"),
		IsParticipant: isParticipant,
		Message:       r.URL.Query().Get("msg"),
		Error:         r.URL.Query().Get("error"),
	}

	// Fetch event details
	_ = s.db.QueryRowContext(r.Context(), "SELECT id, name, COALESCE(submissions_close_at, '') FROM events ORDER BY created_at ASC LIMIT 1;").Scan(
		&data.EventID, &data.EventName, &data.SubmissionsCloseAt,
	)
	if data.SubmissionsCloseAt != "" {
		if ct, err := time.Parse(time.RFC3339, data.SubmissionsCloseAt); err == nil {
			data.SubmissionsOpen = time.Now().UTC().Before(ct)
		}
	} else {
		data.SubmissionsOpen = true
	}

	// Fetch tracks with project count
	tRows, err := s.db.QueryContext(r.Context(), `
		SELECT t.id, t.name, COUNT(p.id)
		FROM tracks t
		LEFT JOIN projects p ON t.id = p.track_id
		GROUP BY t.id, t.name
		ORDER BY t.name ASC;
	`)
	if err == nil {
		defer tRows.Close()
		for tRows.Next() {
			var tc TrackCount
			if err := tRows.Scan(&tc.ID, &tc.Name, &tc.Count); err == nil {
				data.Tracks = append(data.Tracks, tc)
			}
		}
	}

	// Fetch prizes
	przRows, err := s.db.QueryContext(r.Context(), `
		SELECT p.id, p.event_id, COALESCE(p.track_id, ''), COALESCE(t.name, 'All Tracks'), p.name, p.description, p.amount_text, p.created_at
		FROM prizes p
		LEFT JOIN tracks t ON p.track_id = t.id
		ORDER BY p.name ASC;
	`)
	if err == nil {
		defer przRows.Close()
		for przRows.Next() {
			var prz PrizeView
			if err := przRows.Scan(&prz.ID, &prz.EventID, &prz.TrackID, &prz.TrackName, &prz.Name, &prz.Description, &prz.AmountText, &prz.CreatedAt); err == nil {
				data.Prizes = append(data.Prizes, prz)
			}
		}
	}

	// Organizer overview: fetch all teams and all projects
	if data.IsOrganizer || data.IsAdmin {
		allTeamsRows, err := s.db.QueryContext(r.Context(), `
			SELECT t.id, t.name,
				(SELECT COUNT(*) FROM team_memberships WHERE team_id = t.id AND left_at IS NULL),
				COALESCE((SELECT u.display_name FROM team_memberships tm JOIN users u ON tm.user_id = u.id WHERE tm.team_id = t.id AND (tm.membership_role = 'lead' OR tm.membership_role = 'leader') AND tm.left_at IS NULL LIMIT 1), 'None'),
				t.created_at
			FROM teams t
			ORDER BY t.created_at DESC;
		`)
		if err == nil {
			defer allTeamsRows.Close()
			for allTeamsRows.Next() {
				var otv OrganizerTeamView
				if err := allTeamsRows.Scan(&otv.ID, &otv.Name, &otv.MemberCount, &otv.LeadName, &otv.CreatedAt); err == nil {
					data.AllTeams = append(data.AllTeams, otv)
				}
			}
		}

		allPrjRows, err := s.db.QueryContext(r.Context(), `
			SELECT p.id, p.team_id, p.track_id, t.name, s.title, s.summary, COALESCE(s.repo_url, ''), COALESCE(s.demo_url, ''), s.state, s.version_no, COALESCE(s.submitted_at, s.created_at)
			FROM projects p
			JOIN tracks t ON p.track_id = t.id
			JOIN submissions s ON p.id = s.project_id
			WHERE s.version_no = (SELECT MAX(version_no) FROM submissions WHERE project_id = p.id)
			ORDER BY s.submitted_at DESC, s.created_at DESC;
		`)
		if err == nil {
			defer allPrjRows.Close()
			for allPrjRows.Next() {
				var pv ProjectView
				if err := allPrjRows.Scan(&pv.ID, &pv.TeamID, &pv.TrackID, &pv.TrackName, &pv.Title, &pv.Summary, &pv.RepoURL, &pv.DemoURL, &pv.State, &pv.VersionNo, &pv.SubmittedAt); err == nil {
					data.AllProjects = append(data.AllProjects, pv)
				}
			}
		}

		if s.judgingService != nil {
			data.Rubrics, _ = s.judgingService.ListRubrics(r.Context(), data.EventID)
		}
		_ = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM assignments WHERE event_id = ?;", data.EventID).Scan(&data.TotalAssignments)
		_ = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM assignment_runs WHERE event_id = ?;", data.EventID).Scan(&data.AssignmentRunCount)

		if s.resultsService != nil {
			activeRun, _, err := s.resultsService.GetActiveResults(r.Context(), data.EventID)
			if err == nil && activeRun != nil {
				data.ActiveResultRunID = activeRun.ID
				data.ResultsPublished = true
				refTime := activeRun.CreatedAt
				if activeRun.PublishedAt != nil && *activeRun.PublishedAt != "" {
					refTime = *activeRun.PublishedAt
				}
				_ = s.db.QueryRowContext(r.Context(), `
					SELECT COUNT(*) FROM ballot_versions b
					JOIN assignments a ON b.assignment_id = a.id
					WHERE a.event_id = ?
					  AND b.save_kind IN ('SUBMISSION', 'SUBMITTED', 'CORRECTION')
					  AND b.created_at > ?;
				`, data.EventID, refTime).Scan(&data.PendingBallotCount)
			} else {
				var draftCount int
				_ = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM result_runs WHERE event_id = ? AND status = 'DRAFT';", data.EventID).Scan(&draftCount)
				data.DraftResultRunCount = draftCount
			}
		}

		if prog, jProg, pCov, tProg, allJudges, err := s.computeJudgingProgress(r.Context(), data.EventID); err == nil {
			data.JudgingProgress = prog
			data.JudgeProgress = jProg
			data.ProjectCoverage = pCov
			data.TrackProgress = tProg
			data.AllJudges = allJudges
		}
	}

	// Judge overview: fetch assigned projects
	if data.IsJudge && s.judgingService != nil {
		data.JudgeAssignments, _ = s.judgingService.ListJudgeAssignments(r.Context(), data.EventID, user.UserID)
	}

	// Fetch user's team if participant
	if data.IsParticipant {
		var teamID, teamName string
	err = s.db.QueryRowContext(r.Context(), `
		SELECT t.id, t.name
		FROM team_memberships tm
		JOIN teams t ON tm.team_id = t.id
		WHERE tm.user_id = ? AND tm.left_at IS NULL
		LIMIT 1;
	`, user.UserID).Scan(&teamID, &teamName)
	if err == nil {
		data.Team = &TeamView{ID: teamID, Name: teamName}

		// Invite token query param
		if inviteToken := r.URL.Query().Get("invite_token"); inviteToken != "" {
			data.CurrentInviteURL = "/teams/join?token=" + inviteToken
		}

		// Fetch team members
		mRows, err := s.db.QueryContext(r.Context(), `
			SELECT u.id, u.display_name, u.email_normalized, tm.membership_role, tm.joined_at
			FROM team_memberships tm
			JOIN users u ON tm.user_id = u.id
			WHERE tm.team_id = ? AND tm.left_at IS NULL
			ORDER BY tm.joined_at ASC;
		`, teamID)
		if err == nil {
			defer mRows.Close()
			for mRows.Next() {
				var mem TeamMemberView
				if err := mRows.Scan(&mem.UserID, &mem.DisplayName, &mem.Email, &mem.Role, &mem.JoinedAt); err == nil {
					data.TeamMembers = append(data.TeamMembers, mem)
				}
			}
		}

		// Fetch team invites
		iRows, err := s.db.QueryContext(r.Context(), `
			SELECT id, token_hash, created_at, expires_at, COALESCE(accepted_at, ''), COALESCE(accepted_by, ''), COALESCE(revoked_at, '')
			FROM team_invites
			WHERE team_id = ?
			ORDER BY created_at DESC;
		`, teamID)
		if err == nil {
			defer iRows.Close()
			for iRows.Next() {
				var inv TeamInviteView
				if err := iRows.Scan(&inv.ID, &inv.TokenHash, &inv.CreatedAt, &inv.ExpiresAt, &inv.AcceptedAt, &inv.AcceptedBy, &inv.RevokedAt); err == nil {
					data.TeamInvites = append(data.TeamInvites, inv)
				}
			}
		}

		// Fetch team projects
		pRows, err := s.db.QueryContext(r.Context(), `
			SELECT p.id, p.track_id, t.name, s.title, s.summary, COALESCE(s.repo_url, ''), COALESCE(s.demo_url, ''), s.state, s.version_no, COALESCE(s.submitted_at, s.created_at)
			FROM projects p
			JOIN tracks t ON p.track_id = t.id
			JOIN submissions s ON p.id = s.project_id
			WHERE p.team_id = ?
			ORDER BY s.version_no DESC;
		`, teamID)
		if err == nil {
			defer pRows.Close()
			for pRows.Next() {
				var pv ProjectView
				if err := pRows.Scan(&pv.ID, &pv.TrackID, &pv.TrackName, &pv.Title, &pv.Summary, &pv.RepoURL, &pv.DemoURL, &pv.State, &pv.VersionNo, &pv.SubmittedAt); err == nil {
					data.TeamProjects = append(data.TeamProjects, pv)
				}
			}
		}
	}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.tmpl.ExecuteTemplate(w, "dashboard.html", data)
}

func isBrowserForm(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

func (s *Server) handleCreateTeam(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if user == nil {
		if !isBrowserForm(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "/login?error=Please+sign+in+first", http.StatusSeeOther)
		return
	}

	if (user.HasRole("judge") || user.HasRole("organizer")) && !user.HasRole("participant") {
		http.Error(w, "forbidden: non-participant role cannot create team", http.StatusForbidden)
		return
	}

	_ = r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		var req struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		name = strings.TrimSpace(req.Name)
	}
	if name == "" {
		if !isBrowserForm(r) {
			http.Error(w, "team name cannot be empty", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/dashboard?error=Team+name+cannot+be+empty", http.StatusSeeOther)
		return
	}

	teamID := newID("tm")
	nowUTC := time.Now().UTC().Format(time.RFC3339)

	var eventID string
	_ = s.db.QueryRowContext(r.Context(), "SELECT id FROM events ORDER BY created_at ASC LIMIT 1;").Scan(&eventID)
	if eventID == "" {
		eventID = "evt_01"
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(r.Context(), `INSERT INTO teams (id, event_id, name, created_by, created_at) VALUES (?, ?, ?, ?, ?);`,
		teamID, eventID, name, user.UserID, nowUTC)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	membershipID := newID("mem")
	_, err = tx.ExecContext(r.Context(), `INSERT INTO team_memberships (id, event_id, team_id, user_id, membership_role, joined_at) VALUES (?, ?, ?, ?, 'lead', ?);`,
		membershipID, eventID, teamID, user.UserID, nowUTC)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_, _ = tx.ExecContext(r.Context(), `INSERT OR IGNORE INTO event_roles (event_id, user_id, role, granted_at) VALUES (?, ?, 'participant', ?);`,
		eventID, user.UserID, nowUTC)

	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if !isBrowserForm(r) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": teamID, "name": name})
		return
	}
	http.Redirect(w, r, "/dashboard?msg=Team+created+successfully", http.StatusSeeOther)
}

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if user == nil {
		if !isBrowserForm(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "/login?error=Please+sign+in+first", http.StatusSeeOther)
		return
	}

	teamID := r.PathValue("id")
	if teamID == "" {
		if !isBrowserForm(r) {
			http.Error(w, "team id required", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/dashboard?error=Team+ID+required", http.StatusSeeOther)
		return
	}

	// Verify membership
	var isMember int
	_ = s.db.QueryRowContext(r.Context(), `SELECT 1 FROM team_memberships WHERE team_id = ? AND user_id = ? AND left_at IS NULL;`, teamID, user.UserID).Scan(&isMember)
	if isMember != 1 && !user.HasRole("organizer") {
		if !isBrowserForm(r) {
			http.Error(w, "forbidden: not a member of this team", http.StatusForbidden)
			return
		}
		http.Redirect(w, r, "/dashboard?error=Forbidden:+not+a+member+of+this+team", http.StatusSeeOther)
		return
	}

	// Transactional capacity check: Max 4 members
	var activeMembers int
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM team_memberships WHERE team_id = ? AND left_at IS NULL;`, teamID).Scan(&activeMembers)
	if activeMembers >= 4 {
		if !isBrowserForm(r) {
			http.Error(w, "team has already reached maximum capacity of 4 members", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/dashboard?error=Team+has+already+reached+maximum+capacity+of+4+members", http.StatusSeeOther)
		return
	}

	tokenBytes := make([]byte, 16)
	_, _ = rand.Read(tokenBytes)
	rawToken := hex.EncodeToString(tokenBytes)
	tokenHash := auth.HashToken(rawToken)

	inviteID := newID("inv")
	now := time.Now().UTC()
	nowUTC := now.Format(time.RFC3339)
	expiresUTC := now.Add(7 * 24 * time.Hour).Format(time.RFC3339)

	var eventID string
	_ = s.db.QueryRowContext(r.Context(), "SELECT event_id FROM teams WHERE id = ?;", teamID).Scan(&eventID)

	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO team_invites (id, event_id, team_id, token_hash, created_by, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?);
	`, inviteID, eventID, teamID, tokenHash, user.UserID, nowUTC, expiresUTC)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if !isBrowserForm(r) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"invite_token": rawToken,
			"invite_url":   "/teams/join?token=" + rawToken,
			"expires_at":   expiresUTC,
		})
		return
	}
	http.Redirect(w, r, "/dashboard?invite_token="+url.QueryEscape(rawToken)+"&msg=Invite+link+generated+successfully", http.StatusSeeOther)
}

func (s *Server) handleJoinTeam(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		token := strings.TrimSpace(r.URL.Query().Get("token"))
		user := s.getCurrentUser(r)

		data := TeamJoinData{
			EventName: "Dogfood 2026",
			Token:     token,
			User:      user,
			Error:     r.URL.Query().Get("error"),
		}

		if token == "" {
			data.Error = "Missing invitation token in request."
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_ = s.tmpl.ExecuteTemplate(w, "team_join.html", data)
			return
		}

		tokenHash := auth.HashToken(token)
		var teamID, teamName, expiresAt string
		var acceptedAt, revokedAt sql.NullString
		var creatorName sql.NullString

		err := s.db.QueryRowContext(r.Context(), `
			SELECT ti.team_id, t.name, ti.expires_at, ti.accepted_at, ti.revoked_at, u.display_name
			FROM team_invites ti
			JOIN teams t ON ti.team_id = t.id
			LEFT JOIN users u ON ti.created_by = u.id
			WHERE ti.token_hash = ?;
		`, tokenHash).Scan(&teamID, &teamName, &expiresAt, &acceptedAt, &revokedAt, &creatorName)

		if err != nil {
			data.Error = "Invalid or expired invitation."
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_ = s.tmpl.ExecuteTemplate(w, "team_join.html", data)
			return
		}

		data.TeamID = teamID
		data.TeamName = teamName
		data.ExpiresAt = expiresAt
		data.FormattedExpiry = formatDate(expiresAt)
		if creatorName.Valid {
			data.CreatorName = creatorName.String
		}

		if revokedAt.Valid {
			data.Error = "This invitation has been revoked."
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_ = s.tmpl.ExecuteTemplate(w, "team_join.html", data)
			return
		}
		if acceptedAt.Valid {
			data.Error = "This invitation has already been used."
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_ = s.tmpl.ExecuteTemplate(w, "team_join.html", data)
			return
		}
		if exp, err := time.Parse(time.RFC3339, expiresAt); err == nil && time.Now().UTC().After(exp) {
			data.Error = "This invitation has expired."
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_ = s.tmpl.ExecuteTemplate(w, "team_join.html", data)
			return
		}

		var memberCount int
		_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM team_memberships WHERE team_id = ? AND left_at IS NULL;`, teamID).Scan(&memberCount)
		data.MemberCount = memberCount

		if memberCount >= 4 {
			data.Error = "Team is at full capacity (4/4 members)."
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusConflict)
			_ = s.tmpl.ExecuteTemplate(w, "team_join.html", data)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = s.tmpl.ExecuteTemplate(w, "team_join.html", data)
		return
	}

	// POST /teams/join or POST /api/teams/join
	user := s.getCurrentUser(r)
	if user == nil {
		if !isBrowserForm(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = r.ParseForm()
		token := r.FormValue("token")
		returnTo := "/teams/join"
		if token != "" {
			returnTo += "?token=" + url.QueryEscape(token)
		}
		http.Redirect(w, r, "/login?return_to="+url.QueryEscape(returnTo)+"&error=Please+sign+in+first+to+join+team", http.StatusSeeOther)
		return
	}

	if (user.HasRole("judge") || user.HasRole("organizer")) && !user.HasRole("participant") {
		http.Error(w, "forbidden: non-participant role cannot join team", http.StatusForbidden)
		return
	}

	_ = r.ParseForm()
	token := r.FormValue("token")
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if token == "" {
		var req struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		token = req.Token
	}
	token = strings.TrimSpace(token)
	if token == "" {
		if !isBrowserForm(r) {
			http.Error(w, "missing invite token", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/dashboard?error=Missing+invite+token", http.StatusSeeOther)
		return
	}

	tokenHash := auth.HashToken(token)
	now := time.Now().UTC()
	nowUTC := now.Format(time.RFC3339)

	var inviteID, eventID, teamID, expiresAt string
	var acceptedAt, revokedAt sql.NullString
	var teamName string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT ti.id, ti.event_id, ti.team_id, t.name, ti.expires_at, ti.accepted_at, ti.revoked_at
		FROM team_invites ti
		JOIN teams t ON ti.team_id = t.id
		WHERE ti.token_hash = ?;
	`, tokenHash).Scan(&inviteID, &eventID, &teamID, &teamName, &expiresAt, &acceptedAt, &revokedAt)
	if err != nil {
		if !isBrowserForm(r) {
			http.Error(w, "invalid or non-existent invite token", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/teams/join?token="+url.QueryEscape(token)+"&error=Invalid+or+expired+invitation", http.StatusSeeOther)
		return
	}

	if revokedAt.Valid {
		if !isBrowserForm(r) {
			http.Error(w, "invite has been revoked", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/teams/join?token="+url.QueryEscape(token)+"&error=This+invitation+has+been+revoked", http.StatusSeeOther)
		return
	}
	if acceptedAt.Valid {
		if !isBrowserForm(r) {
			http.Error(w, "invite has already been used", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/teams/join?token="+url.QueryEscape(token)+"&error=This+invitation+has+already+been+used", http.StatusSeeOther)
		return
	}
	if exp, err := time.Parse(time.RFC3339, expiresAt); err == nil && now.After(exp) {
		if !isBrowserForm(r) {
			http.Error(w, "invite token has expired", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/teams/join?token="+url.QueryEscape(token)+"&error=This+invitation+has+expired", http.StatusSeeOther)
		return
	}

	// Transactional capacity limit enforcement: Max 4 members
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	var activeMembers int
	err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM team_memberships WHERE team_id = ? AND left_at IS NULL;`, teamID).Scan(&activeMembers)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if activeMembers >= 4 {
		if !isBrowserForm(r) {
			http.Error(w, "team has reached maximum capacity of 4 members", http.StatusConflict) // 409 Conflict
			return
		}
		http.Redirect(w, r, "/teams/join?token="+url.QueryEscape(token)+"&error=Team+is+at+full+capacity+(4/4+members)", http.StatusSeeOther)
		return
	}

	// Check if already an active member of this team
	var alreadyMember int
	_ = tx.QueryRowContext(r.Context(), `SELECT 1 FROM team_memberships WHERE team_id = ? AND user_id = ? AND left_at IS NULL;`, teamID, user.UserID).Scan(&alreadyMember)
	if alreadyMember == 0 {
		membershipID := newID("mem")
		_, err = tx.ExecContext(r.Context(), `INSERT INTO team_memberships (id, event_id, team_id, user_id, membership_role, joined_at) VALUES (?, ?, ?, ?, 'member', ?);`,
			membershipID, eventID, teamID, user.UserID, nowUTC)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Mark invite accepted
	_, err = tx.ExecContext(r.Context(), `UPDATE team_invites SET accepted_at = ?, accepted_by = ? WHERE id = ?;`, nowUTC, user.UserID, inviteID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_, _ = tx.ExecContext(r.Context(), `INSERT OR IGNORE INTO event_roles (event_id, user_id, role, granted_at) VALUES (?, ?, 'participant', ?);`,
		eventID, user.UserID, nowUTC)

	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if !isBrowserForm(r) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "joined", "team_id": teamID})
		return
	}
	http.Redirect(w, r, "/dashboard?msg=Joined+team+"+url.QueryEscape(teamName)+"+successfully", http.StatusSeeOther)
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	isJSON := strings.Contains(r.Header.Get("Accept"), "application/json")
	user := s.getCurrentUser(r)
	if user == nil {
		if isJSON {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "/login?error=Please+sign+in+first", http.StatusSeeOther)
		return
	}

	_ = r.ParseForm()
	teamID := r.FormValue("team_id")
	trackID := r.FormValue("track_id")
	title := strings.TrimSpace(r.FormValue("title"))
	summary := strings.TrimSpace(r.FormValue("summary"))
	repoURL := strings.TrimSpace(r.FormValue("repo_url"))
	demoURL := strings.TrimSpace(r.FormValue("demo_url"))
	action := r.FormValue("action") // "draft" or "submit"

	if title == "" {
		var req struct {
			TeamID  string `json:"team_id"`
			TrackID string `json:"track_id"`
			Title   string `json:"title"`
			Summary string `json:"summary"`
			RepoURL string `json:"repo_url"`
			DemoURL string `json:"demo_url"`
			Action  string `json:"action"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		teamID = req.TeamID
		trackID = req.TrackID
		title = req.Title
		summary = req.Summary
		repoURL = req.RepoURL
		demoURL = req.DemoURL
		action = req.Action
	}

	if title == "" || teamID == "" || trackID == "" {
		if isJSON {
			http.Error(w, "team_id, track_id, and title are required", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/dashboard?error=Please+provide+team,+track,+and+project+title", http.StatusSeeOther)
		return
	}

	// Verify membership
	var isMember int
	_ = s.db.QueryRowContext(r.Context(), `SELECT 1 FROM team_memberships WHERE team_id = ? AND user_id = ? AND left_at IS NULL;`, teamID, user.UserID).Scan(&isMember)
	if isMember != 1 && !canAdminister(user) {
		if isJSON {
			http.Error(w, "forbidden: not a member of this team", http.StatusForbidden)
			return
		}
		http.Redirect(w, r, "/dashboard?error=Forbidden:+not+a+member+of+this+team", http.StatusSeeOther)
		return
	}

	// Check deadline
	open, deadlineStr, _ := s.isSubmissionOpen(r.Context())
	if action == "submit" && !open {
		if isJSON {
			http.Error(w, "forbidden: submissions closed at "+deadlineStr, http.StatusForbidden)
			return
		}
		http.Redirect(w, r, "/dashboard?error=Submissions+are+closed", http.StatusSeeOther)
		return
	}

	var eventID string
	_ = s.db.QueryRowContext(r.Context(), "SELECT id FROM events ORDER BY created_at ASC LIMIT 1;").Scan(&eventID)

	projectID := newID("prj")
	nowUTC := time.Now().UTC().Format(time.RFC3339)

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(r.Context(), `
		INSERT INTO projects (id, event_id, team_id, track_id, created_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?);
	`, projectID, eventID, teamID, trackID, user.UserID, nowUTC)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	state := "DRAFT"
	var submittedAt sql.NullString
	if action == "submit" {
		state = "SUBMITTED"
		submittedAt = sql.NullString{String: nowUTC, Valid: true}
	}

	subID := fmt.Sprintf("sub_%s_v1", projectID)
	_, err = tx.ExecContext(r.Context(), `
		INSERT INTO submissions (id, event_id, project_id, version_no, state, title, summary, repo_url, demo_url, created_by, created_at, submitted_at)
		VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?);
	`, subID, eventID, projectID, state, title, summary, repoURL, demoURL, user.UserID, nowUTC, submittedAt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": projectID, "title": title, "state": state})
		return
	}
	http.Redirect(w, r, "/dashboard?msg=Project+created+successfully", http.StatusSeeOther)
}

func (s *Server) handleEditProject(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	projectID := r.PathValue("id")
	if projectID == "" {
		http.Error(w, "project id required", http.StatusBadRequest)
		return
	}

	// Verify team membership
	var teamID, eventID string
	err := s.db.QueryRowContext(r.Context(), `SELECT team_id, event_id FROM projects WHERE id = ?;`, projectID).Scan(&teamID, &eventID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var isMember int
	_ = s.db.QueryRowContext(r.Context(), `SELECT 1 FROM team_memberships WHERE team_id = ? AND user_id = ? AND left_at IS NULL;`, teamID, user.UserID).Scan(&isMember)
	if isMember != 1 && !canAdminister(user) {
		http.Error(w, "forbidden: not a member of this project's team", http.StatusForbidden)
		return
	}

	// Deadline check
	open, deadlineStr, _ := s.isSubmissionOpen(r.Context())
	if !open {
		http.Error(w, "forbidden: submissions closed at "+deadlineStr, http.StatusForbidden)
		return
	}

	_ = r.ParseForm()
	title := strings.TrimSpace(r.FormValue("title"))
	summary := strings.TrimSpace(r.FormValue("summary"))
	repoURL := strings.TrimSpace(r.FormValue("repo_url"))
	demoURL := strings.TrimSpace(r.FormValue("demo_url"))

	if title == "" {
		var req struct {
			Title   string `json:"title"`
			Summary string `json:"summary"`
			RepoURL string `json:"repo_url"`
			DemoURL string `json:"demo_url"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		title = req.Title
		summary = req.Summary
		repoURL = req.RepoURL
		demoURL = req.DemoURL
	}

	var maxVersion int
	_ = s.db.QueryRowContext(r.Context(), `SELECT COALESCE(MAX(version_no), 1) FROM submissions WHERE project_id = ?;`, projectID).Scan(&maxVersion)
	newVersion := maxVersion + 1
	nowUTC := time.Now().UTC().Format(time.RFC3339)
	subID := fmt.Sprintf("sub_%s_v%d", projectID, newVersion)

	_, err = s.db.ExecContext(r.Context(), `
		INSERT INTO submissions (id, event_id, project_id, version_no, state, title, summary, repo_url, demo_url, created_by, created_at)
		VALUES (?, ?, ?, ?, 'DRAFT', ?, ?, ?, ?, ?, ?);
	`, subID, eventID, projectID, newVersion, title, summary, repoURL, demoURL, user.UserID, nowUTC)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"project_id": projectID, "version_no": newVersion})
		return
	}
	http.Redirect(w, r, "/projects/"+projectID+"?msg=Draft+updated", http.StatusSeeOther)
}

func (s *Server) handleSubmitProject(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	projectID := r.PathValue("id")
	if projectID == "" {
		http.Error(w, "project id required", http.StatusBadRequest)
		return
	}

	var teamID string
	err := s.db.QueryRowContext(r.Context(), `SELECT team_id FROM projects WHERE id = ?;`, projectID).Scan(&teamID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var isMember int
	_ = s.db.QueryRowContext(r.Context(), `SELECT 1 FROM team_memberships WHERE team_id = ? AND user_id = ? AND left_at IS NULL;`, teamID, user.UserID).Scan(&isMember)
	if isMember != 1 && !canAdminister(user) {
		http.Error(w, "forbidden: not a member of this project's team", http.StatusForbidden)
		return
	}

	open, deadlineStr, _ := s.isSubmissionOpen(r.Context())
	if !open {
		http.Error(w, "forbidden: submissions closed at "+deadlineStr, http.StatusForbidden)
		return
	}

	nowUTC := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.ExecContext(r.Context(), `
		UPDATE submissions
		SET state = 'SUBMITTED', submitted_at = ?
		WHERE project_id = ? AND version_no = (SELECT MAX(version_no) FROM submissions WHERE project_id = ?);
	`, nowUTC, projectID, projectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "submitted", "project_id": projectID})
		return
	}
	http.Redirect(w, r, "/dashboard?msg=Project+submitted+to+hackathon", http.StatusSeeOther)
}

func (s *Server) handleGetProjectAPI(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	user := s.getCurrentUser(r)

	var pv ProjectView
	query := `
	SELECT p.id, p.team_id, p.track_id, t.name, s.title, s.summary, COALESCE(s.repo_url, ''), COALESCE(s.demo_url, ''), s.state, s.version_no, COALESCE(s.submitted_at, s.created_at)
	FROM projects p
	JOIN tracks t ON p.track_id = t.id
	JOIN submissions s ON p.id = s.project_id
	WHERE p.id = ?
	ORDER BY s.version_no DESC LIMIT 1;`

	err := s.db.QueryRowContext(r.Context(), query, projectID).Scan(
		&pv.ID, &pv.TeamID, &pv.TrackID, &pv.TrackName, &pv.Title, &pv.Summary, &pv.RepoURL, &pv.DemoURL, &pv.State, &pv.VersionNo, &pv.SubmittedAt,
	)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Draft authorization guard
	if pv.State == "DRAFT" {
		if user == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var isMember int
		_ = s.db.QueryRowContext(r.Context(), `SELECT 1 FROM team_memberships WHERE team_id = ? AND user_id = ? AND left_at IS NULL;`, pv.TeamID, user.UserID).Scan(&isMember)
		if isMember != 1 && !canAdminister(user) {
			http.Error(w, "forbidden: private draft", http.StatusForbidden)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(pv)
}

func (s *Server) handleCreateEvent(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !canAdminister(user) {
		http.Error(w, "forbidden: admin or organizer role required", http.StatusForbidden)
		return
	}

	_ = r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	regOpen := strings.TrimSpace(r.FormValue("registration_opens_at"))
	regClose := strings.TrimSpace(r.FormValue("registration_closes_at"))
	subOpen := strings.TrimSpace(r.FormValue("submissions_open_at"))
	subClose := strings.TrimSpace(r.FormValue("submissions_close_at"))
	jdgOpen := strings.TrimSpace(r.FormValue("judging_opens_at"))
	jdgClose := strings.TrimSpace(r.FormValue("judging_closes_at"))

	if name == "" {
		var req struct {
			Name                 string `json:"name"`
			RegistrationOpensAt  string `json:"registration_opens_at"`
			RegistrationClosesAt string `json:"registration_closes_at"`
			SubmissionsOpensAt   string `json:"submissions_open_at"`
			SubmissionsClosesAt  string `json:"submissions_close_at"`
			JudgingOpensAt       string `json:"judging_opens_at"`
			JudgingClosesAt      string `json:"judging_closes_at"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		name = strings.TrimSpace(req.Name)
		if req.RegistrationOpensAt != "" {
			regOpen = strings.TrimSpace(req.RegistrationOpensAt)
		}
		if req.RegistrationClosesAt != "" {
			regClose = strings.TrimSpace(req.RegistrationClosesAt)
		}
		if req.SubmissionsOpensAt != "" {
			subOpen = strings.TrimSpace(req.SubmissionsOpensAt)
		}
		if req.SubmissionsClosesAt != "" {
			subClose = strings.TrimSpace(req.SubmissionsClosesAt)
		}
		if req.JudgingOpensAt != "" {
			jdgOpen = strings.TrimSpace(req.JudgingOpensAt)
		}
		if req.JudgingClosesAt != "" {
			jdgClose = strings.TrimSpace(req.JudgingClosesAt)
		}
	}

	if name == "" {
		http.Error(w, "event name required", http.StatusBadRequest)
		return
	}

	if err := validateDateOrder(regOpen, regClose, "registration"); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateDateOrder(subOpen, subClose, "submission"); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateDateOrder(jdgOpen, jdgClose, "judging"); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	eventID := newID("evt")
	nowUTC := time.Now().UTC().Format(time.RFC3339)

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	var regOpenVal, regCloseVal, subOpenVal, subCloseVal, jdgOpenVal, jdgCloseVal any
	if regOpen != "" {
		regOpenVal = regOpen
	}
	if regClose != "" {
		regCloseVal = regClose
	}
	if subOpen != "" {
		subOpenVal = subOpen
	}
	if subClose != "" {
		subCloseVal = subClose
	}
	if jdgOpen != "" {
		jdgOpenVal = jdgOpen
	}
	if jdgClose != "" {
		jdgCloseVal = jdgClose
	}

	_, err = tx.ExecContext(r.Context(), `
		INSERT INTO events (id, name, registration_opens_at, registration_closes_at, submissions_open_at, submissions_close_at, judging_opens_at, judging_closes_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`, eventID, name, regOpenVal, regCloseVal, subOpenVal, subCloseVal, jdgOpenVal, jdgCloseVal, nowUTC, nowUTC)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_, _ = tx.ExecContext(r.Context(), `INSERT INTO event_roles (event_id, user_id, role, granted_at) VALUES (?, ?, 'admin', ?), (?, ?, 'organizer', ?);`,
		eventID, user.UserID, nowUTC, eventID, user.UserID, nowUTC)

	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": eventID, "name": name, "status": "created"})
		return
	}
	http.Redirect(w, r, "/dashboard?msg=Event+created", http.StatusSeeOther)
}

func (s *Server) handleUpdateEvent(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !canAdminister(user) {
		http.Error(w, "forbidden: admin or organizer role required", http.StatusForbidden)
		return
	}

	_ = r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	regOpen := strings.TrimSpace(r.FormValue("registration_opens_at"))
	regClose := strings.TrimSpace(r.FormValue("registration_closes_at"))
	subOpen := strings.TrimSpace(r.FormValue("submissions_open_at"))
	subClose := strings.TrimSpace(r.FormValue("submissions_close_at"))
	jdgOpen := strings.TrimSpace(r.FormValue("judging_opens_at"))
	jdgClose := strings.TrimSpace(r.FormValue("judging_closes_at"))

	if name == "" && regOpen == "" && regClose == "" && subOpen == "" && subClose == "" && jdgOpen == "" && jdgClose == "" {
		var req struct {
			Name                 string `json:"name"`
			RegistrationOpensAt  string `json:"registration_opens_at"`
			RegistrationClosesAt string `json:"registration_closes_at"`
			SubmissionsOpensAt   string `json:"submissions_open_at"`
			SubmissionsClosesAt  string `json:"submissions_close_at"`
			JudgingOpensAt       string `json:"judging_opens_at"`
			JudgingClosesAt      string `json:"judging_closes_at"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		name = strings.TrimSpace(req.Name)
		regOpen = strings.TrimSpace(req.RegistrationOpensAt)
		regClose = strings.TrimSpace(req.RegistrationClosesAt)
		subOpen = strings.TrimSpace(req.SubmissionsOpensAt)
		subClose = strings.TrimSpace(req.SubmissionsClosesAt)
		jdgOpen = strings.TrimSpace(req.JudgingOpensAt)
		jdgClose = strings.TrimSpace(req.JudgingClosesAt)
	}

	var eventID string
	var curName, curRegOpen, curRegClose, curSubOpen, curSubClose, curJdgOpen, curJdgClose sql.NullString
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, name, registration_opens_at, registration_closes_at, submissions_open_at, submissions_close_at, judging_opens_at, judging_closes_at
		FROM events ORDER BY created_at ASC LIMIT 1;
	`).Scan(&eventID, &curName, &curRegOpen, &curRegClose, &curSubOpen, &curSubClose, &curJdgOpen, &curJdgClose)
	if err != nil {
		http.Error(w, "event not found", http.StatusNotFound)
		return
	}

	finalName := curName.String
	if name != "" {
		finalName = name
	}
	finalRegOpen := curRegOpen.String
	if regOpen != "" {
		finalRegOpen = regOpen
	}
	finalRegClose := curRegClose.String
	if regClose != "" {
		finalRegClose = regClose
	}
	finalSubOpen := curSubOpen.String
	if subOpen != "" {
		finalSubOpen = subOpen
	}
	finalSubClose := curSubClose.String
	if subClose != "" {
		finalSubClose = subClose
	}
	finalJdgOpen := curJdgOpen.String
	if jdgOpen != "" {
		finalJdgOpen = jdgOpen
	}
	finalJdgClose := curJdgClose.String
	if jdgClose != "" {
		finalJdgClose = jdgClose
	}

	if err := validateDateOrder(finalRegOpen, finalRegClose, "registration"); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateDateOrder(finalSubOpen, finalSubClose, "submission"); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := validateDateOrder(finalJdgOpen, finalJdgClose, "judging"); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var regOpenVal, regCloseVal, subOpenVal, subCloseVal, jdgOpenVal, jdgCloseVal any
	if finalRegOpen != "" {
		regOpenVal = finalRegOpen
	}
	if finalRegClose != "" {
		regCloseVal = finalRegClose
	}
	if finalSubOpen != "" {
		subOpenVal = finalSubOpen
	}
	if finalSubClose != "" {
		subCloseVal = finalSubClose
	}
	if finalJdgOpen != "" {
		jdgOpenVal = finalJdgOpen
	}
	if finalJdgClose != "" {
		jdgCloseVal = finalJdgClose
	}

	nowUTC := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.ExecContext(r.Context(), `
		UPDATE events
		SET name = ?, registration_opens_at = ?, registration_closes_at = ?, submissions_open_at = ?, submissions_close_at = ?, judging_opens_at = ?, judging_closes_at = ?, updated_at = ?
		WHERE id = ?;
	`, finalName, regOpenVal, regCloseVal, subOpenVal, subCloseVal, jdgOpenVal, jdgCloseVal, nowUTC, eventID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "updated", "id": eventID})
		return
	}
	http.Redirect(w, r, "/dashboard?msg=Event+updated", http.StatusSeeOther)
}

func (s *Server) handleGetTracks(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	trackRows, err := s.db.QueryContext(r.Context(), `
		SELECT t.id, t.name, COUNT(p.id)
		FROM tracks t
		LEFT JOIN projects p ON t.id = p.track_id
		GROUP BY t.id, t.name
		ORDER BY t.name ASC;
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer trackRows.Close()

	var tracks []TrackCount
	for trackRows.Next() {
		var tc TrackCount
		if err := trackRows.Scan(&tc.ID, &tc.Name, &tc.Count); err == nil {
			tracks = append(tracks, tc)
		}
	}
	if tracks == nil {
		tracks = []TrackCount{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tracks)
}

func (s *Server) handleCreateTrack(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !canAdminister(user) {
		http.Error(w, "forbidden: admin or organizer role required", http.StatusForbidden)
		return
	}

	_ = r.ParseForm()
	trackName := strings.TrimSpace(r.FormValue("name"))
	eventID := strings.TrimSpace(r.FormValue("event_id"))
	if trackName == "" {
		var req struct {
			Name    string `json:"name"`
			EventID string `json:"event_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		trackName = strings.TrimSpace(req.Name)
		eventID = strings.TrimSpace(req.EventID)
	}

	if trackName == "" {
		http.Error(w, "track name required", http.StatusBadRequest)
		return
	}

	if eventID == "" {
		_ = s.db.QueryRowContext(r.Context(), "SELECT id FROM events ORDER BY created_at ASC LIMIT 1;").Scan(&eventID)
	}

	trackID := newID("trk")
	_, err := s.db.ExecContext(r.Context(), `INSERT INTO tracks (id, event_id, name, created_at) VALUES (?, ?, ?, ?);`,
		trackID, eventID, trackName, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": trackID, "name": trackName, "event_id": eventID})
		return
	}

	http.Redirect(w, r, "/dashboard?msg=Track+created", http.StatusSeeOther)
}

func (s *Server) handleGetPrizes(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT p.id, p.event_id, COALESCE(p.track_id, ''), COALESCE(t.name, 'All Tracks'), p.name, p.description, p.amount_text, p.created_at
		FROM prizes p
		LEFT JOIN tracks t ON p.track_id = t.id
		ORDER BY p.name ASC;
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var prizes []PrizeView
	for rows.Next() {
		var prz PrizeView
		if err := rows.Scan(&prz.ID, &prz.EventID, &prz.TrackID, &prz.TrackName, &prz.Name, &prz.Description, &prz.AmountText, &prz.CreatedAt); err == nil {
			prizes = append(prizes, prz)
		}
	}
	if prizes == nil {
		prizes = []PrizeView{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(prizes)
}

func (s *Server) handleCreatePrize(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !canAdminister(user) {
		http.Error(w, "forbidden: admin or organizer role required", http.StatusForbidden)
		return
	}

	_ = r.ParseForm()
	name := strings.TrimSpace(r.FormValue("name"))
	desc := strings.TrimSpace(r.FormValue("description"))
	amount := strings.TrimSpace(r.FormValue("amount_text"))
	trackID := strings.TrimSpace(r.FormValue("track_id"))
	eventID := strings.TrimSpace(r.FormValue("event_id"))

	if name == "" {
		var req struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			AmountText  string `json:"amount_text"`
			TrackID     string `json:"track_id"`
			EventID     string `json:"event_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		name = strings.TrimSpace(req.Name)
		desc = strings.TrimSpace(req.Description)
		amount = strings.TrimSpace(req.AmountText)
		trackID = strings.TrimSpace(req.TrackID)
		eventID = strings.TrimSpace(req.EventID)
	}

	if name == "" {
		http.Error(w, "prize name required", http.StatusBadRequest)
		return
	}

	if eventID == "" {
		_ = s.db.QueryRowContext(r.Context(), "SELECT id FROM events ORDER BY created_at ASC LIMIT 1;").Scan(&eventID)
	}

	var trackVal any
	if trackID != "" {
		trackVal = trackID
	}

	prizeID := newID("prz")
	nowUTC := time.Now().UTC().Format(time.RFC3339)

	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO prizes (id, event_id, track_id, name, description, amount_text, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?);
	`, prizeID, eventID, trackVal, name, desc, amount, nowUTC)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          prizeID,
			"event_id":    eventID,
			"name":        name,
			"description": desc,
			"amount_text": amount,
			"track_id":    trackID,
		})
		return
	}

	http.Redirect(w, r, "/dashboard?msg=Prize+created", http.StatusSeeOther)
}

func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	// Drain request body to ensure client TCP socket remains cleanly synchronized
	if r.Body != nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20))
		_ = r.Body.Close()
	}

	if s.db == nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	// 1. Authenticate caller
	id, err := auth.Authenticate(r.Context(), s.db, r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}
	_ = id

	// 2. Check event submission deadline
	open, deadlineStr, _ := s.isSubmissionOpen(r.Context())
	if !open {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden) // 403: 400 <= status < 500
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":    "submissions are closed for this event",
			"deadline": deadlineStr,
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "submitted"})
}

func (s *Server) handleJudgeScores(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	// 1. Authenticate caller
	id, err := auth.Authenticate(r.Context(), s.db, r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}

	// 2. Enforce Judge / Admin role
	if !id.HasRole("judge") && !canAdminister(id) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden) // 403: participant blocked
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden: only judges and admins can access judge scores"})
		return
	}

	// 3. Resolve target judge from query parameter and enforce peer isolation
	targetJudge := r.URL.Query().Get("judge")
	resolvedTarget := targetJudge
	if targetJudge == "judge_a" {
		resolvedTarget = "jdg_01"
	} else if targetJudge == "judge_b" {
		resolvedTarget = "jdg_02"
	}

	if resolvedTarget != "" && resolvedTarget != id.UserID && !canAdminister(id) {
		// Peer judge access denial
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden) // 403: peer isolation enforced
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden: judges cannot access peer judge scores"})
		return
	}

	targetUID := id.UserID
	if resolvedTarget != "" {
		targetUID = resolvedTarget
	}

	scoresList := []any{}
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT bv.id, bv.assignment_id, bv.project_id, s.title, bv.save_kind, bv.scores_json, bv.comment, bv.created_at
		FROM ballot_versions bv
		JOIN projects p ON bv.project_id = p.id
		JOIN submissions s ON p.id = s.project_id AND s.version_no = (SELECT MAX(version_no) FROM submissions WHERE project_id = p.id)
		WHERE bv.judge_user_id = ?
		ORDER BY bv.created_at DESC;
	`, targetUID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var bID, aID, pID, title, saveKind, scoresJSON, comment, createdAt string
			if err := rows.Scan(&bID, &aID, &pID, &title, &saveKind, &scoresJSON, &comment, &createdAt); err == nil {
				var parsedScores map[string]any
				_ = json.Unmarshal([]byte(scoresJSON), &parsedScores)
				scoresList = append(scoresList, map[string]any{
					"ballot_id":     bID,
					"assignment_id": aID,
					"project_id":    pID,
					"project_title": title,
					"save_kind":     saveKind,
					"scores":        parsedScores,
					"comment":       comment,
					"created_at":    createdAt,
				})
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"judge_id":   targetUID,
		"judge_name": id.DisplayName,
		"scores":     scoresList,
	})
}

// --- T2 Judging: Evaluations & Rubrics Lifecycle Handlers ---

func (s *Server) handleEvaluationPage(w http.ResponseWriter, r *http.Request) {
	assignmentID := r.PathValue("id")
	if assignmentID == "" {
		http.NotFound(w, r)
		return
	}

	user := s.getCurrentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login?return_to="+url.QueryEscape(r.URL.String()), http.StatusSeeOther)
		return
	}

	if !user.HasRole("judge") && !canAdminister(user) {
		http.Error(w, "Forbidden: Only assigned judges can view evaluation ballots", http.StatusForbidden)
		return
	}

	detail, err := s.judgingService.GetAssignmentDetail(r.Context(), assignmentID, user.UserID, canAdminister(user))
	if err != nil {
		if errors.Is(err, judging.ErrForbidden) {
			http.Error(w, "Forbidden: Peer isolation enforced — you are not assigned to evaluate this project", http.StatusForbidden)
			return
		}
		if errors.Is(err, judging.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := EvaluationData{
		EventName:    "Dogfood 2026",
		User:         user,
		Assignment:   detail,
		Rubric:       detail.Rubric,
		LatestBallot: detail.LatestBallot,
		IsSubmitted:  detail.IsSubmitted,
		Message:      r.URL.Query().Get("msg"),
		Error:        r.URL.Query().Get("error"),
	}

	var eventName string
	if err := s.db.QueryRowContext(r.Context(), "SELECT name FROM events WHERE id = ?;", detail.EventID).Scan(&eventName); err == nil {
		data.EventName = eventName
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := s.tmpl.ExecuteTemplate(w, "evaluation.html", data); err != nil {
		http.Error(w, "internal server error: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleEvaluationSubmit(w http.ResponseWriter, r *http.Request) {
	assignmentID := r.PathValue("id")
	if assignmentID == "" {
		http.NotFound(w, r)
		return
	}

	user := s.getCurrentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login?return_to="+url.QueryEscape("/evaluations/"+assignmentID), http.StatusSeeOther)
		return
	}

	if !user.HasRole("judge") && !canAdminister(user) {
		http.Error(w, "Forbidden: Only assigned judges can submit evaluations", http.StatusForbidden)
		return
	}

	_ = r.ParseForm()
	action := strings.ToLower(strings.TrimSpace(r.FormValue("action")))
	if action == "" {
		action = "draft"
	}
	comment := strings.TrimSpace(r.FormValue("comment"))

	detail, err := s.judgingService.GetAssignmentDetail(r.Context(), assignmentID, user.UserID, canAdminister(user))
	if err != nil {
		if errors.Is(err, judging.ErrForbidden) {
			http.Error(w, "Forbidden: Peer isolation enforced", http.StatusForbidden)
			return
		}
		if errors.Is(err, judging.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if detail.IsSubmitted {
		http.Redirect(w, r, "/evaluations/"+assignmentID+"?error="+url.QueryEscape("Evaluation has already been submitted and cannot be modified"), http.StatusSeeOther)
		return
	}

	scores := make(map[string]float64)
	for _, c := range detail.Rubric.Criteria {
		valStr := r.FormValue("criterion_" + c.ID)
		if valStr == "" {
			valStr = r.FormValue(c.ID)
		}
		if valStr != "" {
			if v, err := strconv.ParseFloat(valStr, 64); err == nil {
				scores[c.ID] = v
			}
		}
	}

	if action == "submit" {
		_, err := s.judgingService.SubmitBallot(r.Context(), assignmentID, user.UserID, scores, comment)
		if err != nil {
			if errors.Is(err, judging.ErrBallotSubmitted) {
				http.Redirect(w, r, "/evaluations/"+assignmentID+"?error="+url.QueryEscape("Ballot has already been submitted and is locked"), http.StatusSeeOther)
				return
			}
			http.Redirect(w, r, "/evaluations/"+assignmentID+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		s.syncResultsAfterBallot(r.Context(), detail.EventID, user.UserID)
		http.Redirect(w, r, "/evaluations/"+assignmentID+"?msg="+url.QueryEscape("Evaluation submitted successfully. Ballot is permanently locked."), http.StatusSeeOther)
		return
	}

	// Draft
	_, err = s.judgingService.SaveDraftBallot(r.Context(), assignmentID, user.UserID, scores, comment)
	if err != nil {
		if errors.Is(err, judging.ErrBallotSubmitted) {
			http.Redirect(w, r, "/evaluations/"+assignmentID+"?error="+url.QueryEscape("Ballot has already been submitted and cannot be edited"), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/evaluations/"+assignmentID+"?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/evaluations/"+assignmentID+"?msg="+url.QueryEscape("Evaluation draft saved successfully."), http.StatusSeeOther)
}

func (s *Server) handleGetBallotAPI(w http.ResponseWriter, r *http.Request) {
	assignmentID := r.PathValue("id")
	if assignmentID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
		return
	}

	id, err := auth.Authenticate(r.Context(), s.db, r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}

	detail, err := s.judgingService.GetAssignmentDetail(r.Context(), assignmentID, id.UserID, canAdminister(id))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		if errors.Is(err, judging.ErrForbidden) {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden: peer isolation enforced"})
			return
		}
		if errors.Is(err, judging.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(detail)
}

func (s *Server) handleSaveDraftBallotAPI(w http.ResponseWriter, r *http.Request) {
	assignmentID := r.PathValue("id")
	id, err := auth.Authenticate(r.Context(), s.db, r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}

	var req struct {
		Scores  map[string]float64 `json:"scores"`
		Comment string             `json:"comment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid json payload: " + err.Error()})
		return
	}

	ballot, err := s.judgingService.SaveDraftBallot(r.Context(), assignmentID, id.UserID, req.Scores, req.Comment)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		if errors.Is(err, judging.ErrBallotSubmitted) {
			w.WriteHeader(http.StatusConflict) // 409
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "ballot already submitted and cannot be modified"})
			return
		}
		if errors.Is(err, judging.ErrForbidden) {
			w.WriteHeader(http.StatusForbidden) // 403
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden: peer isolation enforced"})
			return
		}
		if errors.Is(err, judging.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound) // 404
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "assignment not found"})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(ballot)
}

func (s *Server) handleSubmitBallotAPI(w http.ResponseWriter, r *http.Request) {
	assignmentID := r.PathValue("id")
	id, err := auth.Authenticate(r.Context(), s.db, r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}

	var req struct {
		Scores  map[string]float64 `json:"scores"`
		Comment string             `json:"comment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid json payload: " + err.Error()})
		return
	}

	ballot, err := s.judgingService.SubmitBallot(r.Context(), assignmentID, id.UserID, req.Scores, req.Comment)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		if errors.Is(err, judging.ErrBallotSubmitted) {
			w.WriteHeader(http.StatusConflict) // 409
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "ballot already submitted and cannot be modified"})
			return
		}
		if errors.Is(err, judging.ErrForbidden) {
			w.WriteHeader(http.StatusForbidden) // 403
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden: peer isolation enforced"})
			return
		}
		if errors.Is(err, judging.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound) // 404
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "assignment not found"})
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	var eventID string
	_ = s.db.QueryRowContext(r.Context(), "SELECT event_id FROM assignments WHERE id = ?;", assignmentID).Scan(&eventID)
	s.syncResultsAfterBallot(r.Context(), eventID, id.UserID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(ballot)
}

func (s *Server) handleGetRubricAPI(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("event_id")
	trackID := r.URL.Query().Get("track_id")

	rubric, err := s.judgingService.GetPublishedRubric(r.Context(), eventID, trackID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		if errors.Is(err, judging.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "no published rubric found"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(rubric)
}

func (s *Server) handleCreateRubricDraftAPI(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("event_id")
	id, err := auth.Authenticate(r.Context(), s.db, r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}
	if !canAdminister(id) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden: only organizers can manage rubrics"})
		return
	}

	var req struct {
		RubricID string              `json:"rubric_id"`
		TrackID  string              `json:"track_id"`
		Criteria []judging.Criterion `json:"criteria"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid json payload: " + err.Error()})
		return
	}

	rv, err := s.judgingService.CreateRubricDraft(r.Context(), eventID, req.RubricID, req.TrackID, req.Criteria, id.UserID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(rv)
}

func (s *Server) handlePublishRubricAPI(w http.ResponseWriter, r *http.Request) {
	rubricVersionID := r.PathValue("rubric_version_id")
	id, err := auth.Authenticate(r.Context(), s.db, r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}
	if !canAdminister(id) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden: only organizers can publish rubrics"})
		return
	}

	rv, err := s.judgingService.PublishRubricVersion(r.Context(), rubricVersionID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(rv)
}

func (s *Server) handleRunAssignments(w http.ResponseWriter, r *http.Request) {
	id, err := auth.Authenticate(r.Context(), s.db, r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !canAdminister(id) {
		http.Error(w, "Forbidden: organizer role required", http.StatusForbidden)
		return
	}

	var eventID string
	_ = s.db.QueryRowContext(r.Context(), "SELECT id FROM events ORDER BY created_at ASC LIMIT 1;").Scan(&eventID)

	_, err = s.assignmentService.GenerateAssignments(r.Context(), assignment.Config{
		EventID:       eventID,
		TargetReviews: 3,
		CreatedBy:     id.UserID,
	})
	if err != nil {
		http.Redirect(w, r, "/dashboard?error="+url.QueryEscape("Assignment run failed: "+err.Error()), http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/dashboard?msg="+url.QueryEscape("Deterministic judge assignments generated successfully."), http.StatusSeeOther)
}

func (s *Server) handleListProjectsAPI(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	trackFilter := r.URL.Query().Get("track")

	query := `
	SELECT p.id, p.team_id, p.track_id, t.name, s.title, s.summary, COALESCE(s.repo_url, ''), COALESCE(s.demo_url, ''), s.state, s.version_no, COALESCE(s.submitted_at, s.created_at)
	FROM projects p
	JOIN tracks t ON p.track_id = t.id
	JOIN submissions s ON p.id = s.project_id
	WHERE s.version_no = (SELECT MAX(version_no) FROM submissions WHERE project_id = p.id)
	  AND s.state = 'SUBMITTED'`

	var args []any
	if trackFilter != "" {
		query += " AND (t.id = ? OR t.name = ?)"
		args = append(args, trackFilter, trackFilter)
	}
	query += " ORDER BY s.submitted_at DESC;"

	rows, err := s.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var projects []ProjectView
	for rows.Next() {
		var pv ProjectView
		if err := rows.Scan(&pv.ID, &pv.TeamID, &pv.TrackID, &pv.TrackName, &pv.Title, &pv.Summary, &pv.RepoURL, &pv.DemoURL, &pv.State, &pv.VersionNo, &pv.SubmittedAt); err == nil {
			pv.FormattedDate = formatDate(pv.SubmittedAt)
			projects = append(projects, pv)
		}
	}
	if projects == nil {
		projects = []ProjectView{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(projects)
}

func (s *Server) handleListTeamsAPI(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT t.id, t.name, t.created_at, u.display_name, COUNT(DISTINCT m.user_id)
		FROM teams t
		JOIN users u ON t.created_by = u.id
		LEFT JOIN team_memberships m ON t.id = m.team_id AND m.left_at IS NULL
		GROUP BY t.id, t.name, t.created_at, u.display_name
		ORDER BY t.name ASC;
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var teams []OrganizerTeamView
	for rows.Next() {
		var tv OrganizerTeamView
		if err := rows.Scan(&tv.ID, &tv.Name, &tv.CreatedAt, &tv.LeadName, &tv.MemberCount); err == nil {
			teams = append(teams, tv)
		}
	}
	if teams == nil {
		teams = []OrganizerTeamView{}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(teams)
}

// --- Results, Leaderboard, & Explain-This-Rank Handlers ---

func (s *Server) handleResultsPage(w http.ResponseWriter, r *http.Request) {
	var eventID, eventName string
	if s.db != nil {
		_ = s.db.QueryRowContext(r.Context(), "SELECT id, name FROM events ORDER BY created_at ASC LIMIT 1;").Scan(&eventID, &eventName)
	}

	user := s.getCurrentUser(r)
	isOrganizer := canAdminister(user)

	var run *results.ResultRun
	var entries []results.ResultEntry
	var draftRun *results.ResultRun
	var isDraftPreview bool
	var hasPendingBallots bool
	var pendingBallotCount int
	var hasPublishedRun bool

	previewParam := r.URL.Query().Get("preview")
	viewParam := r.URL.Query().Get("view")
	runIDParam := r.URL.Query().Get("run_id")
	wantDraft := isOrganizer && (previewParam == "draft" || viewParam == "draft")

	if s.resultsService != nil && eventID != "" {
		activeRun, activeEntries, err := s.resultsService.GetActiveResults(r.Context(), eventID)
		if err == nil && activeRun != nil {
			hasPublishedRun = true
		}

		// If organizer, check for latest DRAFT run
		if isOrganizer {
			var draftID string
			_ = s.db.QueryRowContext(r.Context(), `
				SELECT id FROM result_runs WHERE event_id = ? AND status = 'DRAFT' ORDER BY created_at DESC LIMIT 1;
			`, eventID).Scan(&draftID)
			if draftID != "" {
				if dr, err := s.resultsService.GetResultRunByID(r.Context(), draftID); err == nil && dr != nil {
					draftRun = dr
				}
			}
		}

		if runIDParam != "" && isOrganizer {
			if specifiedRun, err := s.resultsService.GetResultRunByID(r.Context(), runIDParam); err == nil && specifiedRun != nil {
				run = specifiedRun
				entries, _ = s.resultsService.ListEntriesForRun(r.Context(), runIDParam)
				if run.Status == results.StatusDraft {
					isDraftPreview = true
				}
			}
		} else if wantDraft && draftRun != nil {
			run = draftRun
			entries, _ = s.resultsService.ListEntriesForRun(r.Context(), draftRun.ID)
			isDraftPreview = true
		} else if activeRun != nil {
			run = activeRun
			entries = activeEntries
		} else if isOrganizer && draftRun != nil {
			// No published run yet, so organizer previews the draft run
			run = draftRun
			entries, _ = s.resultsService.ListEntriesForRun(r.Context(), draftRun.ID)
			isDraftPreview = true
		}

		// Check for pending uncalculated ballots if we are displaying a published run
		if run != nil && run.Status == results.StatusPublished {
			refTime := run.CreatedAt
			if run.PublishedAt != nil && *run.PublishedAt != "" {
				refTime = *run.PublishedAt
			}
			_ = s.db.QueryRowContext(r.Context(), `
				SELECT COUNT(*) FROM ballot_versions b
				JOIN assignments a ON b.assignment_id = a.id
				WHERE a.event_id = ?
				  AND b.save_kind IN ('SUBMISSION', 'SUBMITTED', 'CORRECTION')
				  AND b.created_at > ?;
			`, eventID, refTime).Scan(&pendingBallotCount)
			if pendingBallotCount > 0 {
				hasPendingBallots = true
			}
		}
	}

	var tracks []TrackCount
	if s.db != nil && eventID != "" {
		trackRows, err := s.db.QueryContext(r.Context(), `
			SELECT t.id, t.name, COUNT(p.id)
			FROM tracks t
			LEFT JOIN projects p ON t.id = p.track_id
			WHERE t.event_id = ?
			GROUP BY t.id, t.name
			ORDER BY t.name ASC;
		`, eventID)
		if err == nil {
			defer trackRows.Close()
			for trackRows.Next() {
				var tc TrackCount
				if err := trackRows.Scan(&tc.ID, &tc.Name, &tc.Count); err == nil {
					tracks = append(tracks, tc)
				}
			}
		}
	}

	data := ResultsData{
		EventName:          eventName,
		EventID:            eventID,
		Run:                run,
		Entries:            entries,
		Tracks:             tracks,
		User:               user,
		IsOrganizer:        isOrganizer,
		Message:            r.URL.Query().Get("msg"),
		Error:              r.URL.Query().Get("error"),
		DraftRun:           draftRun,
		IsDraftPreview:     isDraftPreview,
		HasPendingBallots:  hasPendingBallots,
		PendingBallotCount: pendingBallotCount,
		HasPublishedRun:    hasPublishedRun,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := s.tmpl.ExecuteTemplate(w, "results.html", data); err != nil {
		http.Error(w, "internal server error: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleExplainRankPage(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	projectID := r.PathValue("project_id")
	if runID == "" || projectID == "" {
		http.NotFound(w, r)
		return
	}

	var eventName string
	if s.db != nil {
		_ = s.db.QueryRowContext(r.Context(), "SELECT name FROM events ORDER BY created_at ASC LIMIT 1;").Scan(&eventName)
	}

	user := s.getCurrentUser(r)

	run, entry, exp, err := s.resultsService.GetProjectExplanation(r.Context(), runID, projectID)
	if err != nil {
		if errors.Is(err, results.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "failed to get explanation: "+err.Error(), http.StatusInternalServerError)
		return
	}

	data := ExplainRankData{
		EventName:   eventName,
		EventID:     run.EventID,
		Run:         run,
		Entry:       entry,
		Explanation: exp,
		User:        user,
		Message:     r.URL.Query().Get("msg"),
		Error:       r.URL.Query().Get("error"),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := s.tmpl.ExecuteTemplate(w, "explain_rank.html", data); err != nil {
		http.Error(w, "internal server error: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleComputeResults(w http.ResponseWriter, r *http.Request) {
	id, err := auth.Authenticate(r.Context(), s.db, r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}
	if !canAdminister(id) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden: organizer role required"})
		return
	}

	var eventID string
	_ = s.db.QueryRowContext(r.Context(), "SELECT id FROM events ORDER BY created_at ASC LIMIT 1;").Scan(&eventID)

	run, err := s.resultsService.ComputeResults(r.Context(), eventID, id.UserID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(run)
}

func (s *Server) handlePublishResults(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("id")
	if runID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "run id required"})
		return
	}

	id, err := auth.Authenticate(r.Context(), s.db, r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}
	if !canAdminister(id) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden: organizer role required"})
		return
	}

	run, err := s.resultsService.PublishResults(r.Context(), runID, id.UserID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(run)
}

func (s *Server) handleRecomputeAndPublishResults(w http.ResponseWriter, r *http.Request) {
	id, err := auth.Authenticate(r.Context(), s.db, r)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}
	if !canAdminister(id) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden: organizer role required"})
		return
	}

	var eventID string
	_ = s.db.QueryRowContext(r.Context(), "SELECT id FROM events ORDER BY created_at ASC LIMIT 1;").Scan(&eventID)

	newRun, err := s.resultsService.ComputeResults(r.Context(), eventID, id.UserID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	publishedRun, err := s.resultsService.PublishResults(r.Context(), newRun.ID, id.UserID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(publishedRun)
}

// syncResultsAfterBallot is triggered when a judge submits/finalizes a score.
// If official results have already been published, it automatically computes and publishes
// a new superseding run so the public leaderboard immediately reflects the new score while
// preserving full auditability and immutability (the old run is retired, not mutated).
func (s *Server) syncResultsAfterBallot(ctx context.Context, eventID, callerUserID string) {
	if s.resultsService == nil || eventID == "" {
		return
	}

	// 1. Check if there is an active published result run for this event
	var publishedID string
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM result_runs 
		WHERE event_id = ? AND status = 'PUBLISHED' 
		ORDER BY created_at DESC LIMIT 1;
	`, eventID).Scan(&publishedID)

	if err == nil && publishedID != "" {
		// An official results publication is active!
		// Automatically compute and publish a new superseding run so the public leaderboard
		// immediately reflects the new score while preserving full auditability (the old run is retired, not mutated).
		newRun, err := s.resultsService.ComputeResults(ctx, eventID, callerUserID)
		if err == nil && newRun != nil {
			_, _ = s.resultsService.PublishResults(ctx, newRun.ID, callerUserID)
		}
		return
	}

	// 2. If no published run exists, check if an unmerged draft run was previously computed
	var draftID string
	err = s.db.QueryRowContext(ctx, `
		SELECT id FROM result_runs 
		WHERE event_id = ? AND status = 'DRAFT' 
		ORDER BY created_at DESC LIMIT 1;
	`, eventID).Scan(&draftID)
	if err == nil && draftID != "" {
		_, _ = s.resultsService.ComputeResults(ctx, eventID, callerUserID)
	}
}

func (s *Server) handleGetActiveResultsAPI(w http.ResponseWriter, r *http.Request) {
	var eventID string
	_ = s.db.QueryRowContext(r.Context(), "SELECT id FROM events ORDER BY created_at ASC LIMIT 1;").Scan(&eventID)

	run, entries, err := s.resultsService.GetActiveResults(r.Context(), eventID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"run":     run,
		"entries": entries,
	})
}

func (s *Server) handleGetExplanationAPI(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	projectID := r.PathValue("project_id")

	_, _, exp, err := s.resultsService.GetProjectExplanation(r.Context(), runID, projectID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		if errors.Is(err, results.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "explanation not found"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(exp)
}

func (s *Server) handleReplayAPI(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	if runID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "run_id required"})
		return
	}

	report, err := s.resultsService.ReplayRun(r.Context(), runID, "")
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(report)
}

func (s *Server) handleReplayProjectAPI(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("run_id")
	projectID := r.PathValue("project_id")

	report, err := s.resultsService.ReplayRun(r.Context(), runID, projectID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(report)
}

// computeJudgingProgress queries real-time judging progress across judges, projects, and tracks.
func (s *Server) computeJudgingProgress(ctx context.Context, eventID string) (
	*OrganizerJudgingProgress,
	[]OrganizerJudgeProgressView,
	[]OrganizerProjectCoverageView,
	[]OrganizerTrackProgressView,
	[]OrganizerJudgeRosterView,
	error,
) {
	if s.db == nil {
		return nil, nil, nil, nil, nil, errors.New("db not initialized")
	}
	if eventID == "" {
		_ = s.db.QueryRowContext(ctx, "SELECT id FROM events ORDER BY created_at ASC LIMIT 1;").Scan(&eventID)
	}

	// 1. Fetch tracks for the event
	trackMap := make(map[string]string)
	var trackIDs []string
	tRows, err := s.db.QueryContext(ctx, "SELECT id, name FROM tracks WHERE event_id = ? ORDER BY name ASC;", eventID)
	if err == nil {
		defer tRows.Close()
		for tRows.Next() {
			var tid, tname string
			if err := tRows.Scan(&tid, &tname); err == nil {
				trackMap[tid] = tname
				trackIDs = append(trackIDs, tid)
			}
		}
	}

	// 2. Fetch judge track eligibility
	judgeTracksMap := make(map[string][]string)
	eRows, err := s.db.QueryContext(ctx, `
		SELECT jte.judge_user_id, t.name
		FROM judge_track_eligibility jte
		JOIN tracks t ON jte.track_id = t.id
		WHERE jte.event_id = ? AND jte.eligible = 1
		ORDER BY t.name ASC;
	`, eventID)
	if err == nil {
		defer eRows.Close()
		for eRows.Next() {
			var jid, tname string
			if err := eRows.Scan(&jid, &tname); err == nil {
				judgeTracksMap[jid] = append(judgeTracksMap[jid], tname)
			}
		}
	}

	// 3. Query active assignments and their latest ballot state
	type assignState struct {
		judgeID   string
		projectID string
		status    string
	}
	var assignments []assignState

	judgeAssigned := make(map[string]int)
	judgeCompleted := make(map[string]int)
	judgeDraft := make(map[string]int)
	judgeUnstarted := make(map[string]int)

	projAssigned := make(map[string]int)
	projCompleted := make(map[string]int)

	aRows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.judge_user_id, a.project_id, a.status,
		       COALESCE((SELECT b.save_kind FROM ballot_versions b WHERE b.assignment_id = a.id ORDER BY b.version_no DESC LIMIT 1), '') as latest_save_kind
		FROM assignments a
		WHERE a.event_id = ? AND a.status NOT IN ('CANCELLED', 'REASSIGNED');
	`, eventID)
	if err == nil {
		defer aRows.Close()
		for aRows.Next() {
			var aid, jid, pid, astatus, latestSave string
			if err := aRows.Scan(&aid, &jid, &pid, &astatus, &latestSave); err == nil {
				judgeAssigned[jid]++
				projAssigned[pid]++

				state := "UNSTARTED"
				if latestSave == "SUBMISSION" || latestSave == "SUBMITTED" || latestSave == "CORRECTION" || astatus == "COMPLETED" {
					state = "COMPLETED"
					judgeCompleted[jid]++
					projCompleted[pid]++
				} else if latestSave == "DRAFT" || astatus == "STARTED" {
					state = "DRAFT"
					judgeDraft[jid]++
				} else {
					judgeUnstarted[jid]++
				}
				assignments = append(assignments, assignState{
					judgeID:   jid,
					projectID: pid,
					status:    state,
				})
			}
		}
	}

	// 4. Fetch judges and build judge views
	var judgeProgress []OrganizerJudgeProgressView
	var allJudges []OrganizerJudgeRosterView
	var totalJudges, activeJudges, completedJudges int

	jRows, err := s.db.QueryContext(ctx, `
		SELECT jp.user_id, u.display_name, u.email_normalized, jp.capacity, jp.active, jp.invited_at
		FROM judge_profiles jp
		JOIN users u ON jp.user_id = u.id
		WHERE jp.event_id = ?
		ORDER BY u.display_name ASC;
	`, eventID)
	if err == nil {
		defer jRows.Close()
		for jRows.Next() {
			var jid, name, email, invitedAt string
			var cap int
			var activeInt int
			if err := jRows.Scan(&jid, &name, &email, &cap, &activeInt, &invitedAt); err == nil {
				totalJudges++
				isActive := activeInt == 1
				if isActive {
					activeJudges++
				}

				assigned := judgeAssigned[jid]
				completed := judgeCompleted[jid]
				drafts := judgeDraft[jid]
				unstarted := judgeUnstarted[jid]
				tracks := judgeTracksMap[jid]

				status := "Not Started"
				if assigned > 0 && completed == assigned {
					status = "Completed"
					completedJudges++
				} else if completed > 0 || drafts > 0 {
					status = "In Progress"
				}

				judgeProgress = append(judgeProgress, OrganizerJudgeProgressView{
					JudgeUserID:    jid,
					DisplayName:    name,
					Email:          email,
					Capacity:       cap,
					Active:         isActive,
					AssignedCount:  assigned,
					CompletedCount: completed,
					DraftCount:     drafts,
					UnstartedCount: unstarted,
					EligibleTracks: tracks,
					Status:         status,
				})

				allJudges = append(allJudges, OrganizerJudgeRosterView{
					UserID:         jid,
					DisplayName:    name,
					Email:          email,
					Capacity:       cap,
					Active:         isActive,
					InvitedAt:      formatDate(invitedAt),
					EligibleTracks: tracks,
					AssignedCount:  assigned,
					CompletedCount: completed,
				})
			}
		}
	}

	// 5. Fetch submitted projects and build coverage views
	var projectCoverage []OrganizerProjectCoverageView
	var fullyReviewedProjects int
	projectTrackMap := make(map[string]string)

	pRows, err := s.db.QueryContext(ctx, `
		SELECT p.id, s.title, COALESCE(tm.name, 'Independent'), p.track_id, t.name
		FROM projects p
		JOIN tracks t ON p.track_id = t.id
		LEFT JOIN teams tm ON p.team_id = tm.id
		JOIN submissions s ON p.id = s.project_id AND s.version_no = (SELECT MAX(version_no) FROM submissions WHERE project_id = p.id)
		WHERE p.event_id = ? AND s.state = 'SUBMITTED'
		ORDER BY t.name ASC, p.id ASC;
	`, eventID)
	if err == nil {
		defer pRows.Close()
		for pRows.Next() {
			var pid, title, teamName, trackID, trackName string
			if err := pRows.Scan(&pid, &title, &teamName, &trackID, &trackName); err == nil {
				projectTrackMap[pid] = trackID

				assigned := projAssigned[pid]
				completed := projCompleted[pid]
				targetReviews := 3
				if assigned > targetReviews {
					targetReviews = assigned
				} else if assigned > 0 && targetReviews > assigned {
					targetReviews = assigned
				}
				if targetReviews <= 0 {
					targetReviews = 1
				}

				covPct := 0.0
				if targetReviews > 0 {
					covPct = math.Min(100.0, float64(completed)/float64(targetReviews)*100.0)
					covPct = math.Round(covPct*10) / 10
				}

				status := "Pending"
				if (assigned > 0 && completed >= assigned) || (targetReviews > 0 && completed >= targetReviews) {
					status = "Fully Reviewed"
					fullyReviewedProjects++
				} else if completed > 0 {
					status = "Partially Reviewed"
				}

				projectCoverage = append(projectCoverage, OrganizerProjectCoverageView{
					ProjectID:        pid,
					Title:            title,
					TeamName:         teamName,
					TrackName:        trackName,
					TargetReviews:    targetReviews,
					AssignedReviews:  assigned,
					CompletedReviews: completed,
					CoveragePercent:  covPct,
					Status:           status,
				})
			}
		}
	}

	// 6. Build track progress views
	var trackProgress []OrganizerTrackProgressView
	for _, tid := range trackIDs {
		tname := trackMap[tid]
		var prjCount, totalAssign, compReviews int
		for _, pc := range projectCoverage {
			if projectTrackMap[pc.ProjectID] == tid {
				prjCount++
				totalAssign += pc.AssignedReviews
				compReviews += pc.CompletedReviews
			}
		}

		rate := 0.0
		if totalAssign > 0 {
			rate = math.Min(100.0, float64(compReviews)/float64(totalAssign)*100.0)
			rate = math.Round(rate*10) / 10
		}

		trackProgress = append(trackProgress, OrganizerTrackProgressView{
			TrackID:          tid,
			TrackName:        tname,
			ProjectCount:     prjCount,
			TotalAssignments: totalAssign,
			CompletedReviews: compReviews,
			CompletionRate:   rate,
		})
	}

	// 7. Aggregate overall JudgingProgress
	totalAssignments := len(assignments)
	completedAssignments := 0
	inProgressAssignments := 0
	unstartedAssignments := 0

	for _, a := range assignments {
		switch a.status {
		case "COMPLETED":
			completedAssignments++
		case "DRAFT":
			inProgressAssignments++
		case "UNSTARTED":
			unstartedAssignments++
		}
	}

	completionRate := 0.0
	if totalAssignments > 0 {
		completionRate = math.Min(100.0, float64(completedAssignments)/float64(totalAssignments)*100.0)
		completionRate = math.Round(completionRate*10) / 10
	}

	overallProgress := &OrganizerJudgingProgress{
		TotalAssignments:      totalAssignments,
		CompletedAssignments:  completedAssignments,
		InProgressAssignments: inProgressAssignments,
		UnstartedAssignments:  unstartedAssignments,
		CompletionRate:        completionRate,
		TotalJudges:           totalJudges,
		ActiveJudges:          activeJudges,
		CompletedJudges:       completedJudges,
		TotalProjects:         len(projectCoverage),
		FullyReviewedProjects: fullyReviewedProjects,
	}

	return overallProgress, judgeProgress, projectCoverage, trackProgress, allJudges, nil
}

// handleOrganizerProgressAPI returns complete judging metrics, judge progress, project coverage, and track progress.
func (s *Server) handleOrganizerProgressAPI(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if user == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}
	if !canAdminister(user) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden: organizer or admin role required"})
		return
	}

	var eventID string
	_ = s.db.QueryRowContext(r.Context(), "SELECT id FROM events ORDER BY created_at ASC LIMIT 1;").Scan(&eventID)

	prog, jProg, pCov, tProg, allJudges, err := s.computeJudgingProgress(r.Context(), eventID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"event_id": eventID,
		"progress": prog,
		"judges":   jProg,
		"projects": pCov,
		"tracks":   tProg,
		"roster":   allJudges,
	})
}

// handleOrganizerCreateJudgeAPI registers or invites a judge, setting capacity and track eligibility.
func (s *Server) handleOrganizerCreateJudgeAPI(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if user == nil {
		if !isBrowserForm(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		http.Redirect(w, r, "/login?error=Please+sign+in+first", http.StatusSeeOther)
		return
	}
	if !canAdminister(user) {
		if !isBrowserForm(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden: organizer or admin role required"})
			return
		}
		http.Redirect(w, r, "/dashboard?error=Organizer+privileges+required", http.StatusSeeOther)
		return
	}

	var eventID string
	_ = s.db.QueryRowContext(r.Context(), "SELECT id FROM events ORDER BY created_at ASC LIMIT 1;").Scan(&eventID)

	var email, displayName string
	capacity := 5
	var trackIDs []string

	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		var req struct {
			Email       string   `json:"email"`
			DisplayName string   `json:"display_name"`
			Capacity    int      `json:"capacity"`
			TrackIDs    []string `json:"track_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON payload"})
			return
		}
		email = strings.TrimSpace(req.Email)
		displayName = strings.TrimSpace(req.DisplayName)
		if req.Capacity > 0 {
			capacity = req.Capacity
		}
		trackIDs = req.TrackIDs
	} else {
		_ = r.ParseForm()
		email = strings.TrimSpace(r.FormValue("email"))
		displayName = strings.TrimSpace(r.FormValue("display_name"))
		if capStr := r.FormValue("capacity"); capStr != "" {
			if c, err := strconv.Atoi(capStr); err == nil && c > 0 {
				capacity = c
			}
		}
		if tracksVal := r.FormValue("tracks"); tracksVal != "" {
			for _, part := range strings.Split(tracksVal, ",") {
				if t := strings.TrimSpace(part); t != "" {
					trackIDs = append(trackIDs, t)
				}
			}
		}
		if formTracks := r.Form["track_ids"]; len(formTracks) > 0 {
			trackIDs = append(trackIDs, formTracks...)
		}
	}

	if email == "" {
		if !isBrowserForm(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "email is required"})
			return
		}
		http.Redirect(w, r, "/dashboard?error=Judge+email+is+required", http.StatusSeeOther)
		return
	}

	normEmail := strings.ToLower(email)
	if displayName == "" {
		parts := strings.Split(normEmail, "@")
		displayName = strings.Title(parts[0])
	}

	// Find or create user
	var targetUserID string
	err := s.db.QueryRowContext(r.Context(), "SELECT id FROM users WHERE email_normalized = ?;", normEmail).Scan(&targetUserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			targetUserID = newID("usr")
			nowUTC := time.Now().UTC().Format(time.RFC3339)
			_, err = s.db.ExecContext(r.Context(), `
				INSERT INTO users (id, email_normalized, display_name, created_at)
				VALUES (?, ?, ?, ?);
			`, targetUserID, normEmail, displayName, nowUTC)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Grant judge role in event
	nowUTC := time.Now().UTC().Format(time.RFC3339)
	_, _ = s.db.ExecContext(r.Context(), `
		INSERT OR IGNORE INTO event_roles (event_id, user_id, role, granted_at)
		VALUES (?, ?, 'judge', ?);
	`, eventID, targetUserID, nowUTC)

	// Upsert judge profile
	_, err = s.db.ExecContext(r.Context(), `
		INSERT INTO judge_profiles (event_id, user_id, capacity, active, invited_at)
		VALUES (?, ?, ?, 1, ?)
		ON CONFLICT(event_id, user_id) DO UPDATE SET capacity = excluded.capacity, active = 1;
	`, eventID, targetUserID, capacity, nowUTC)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Handle track eligibility: if no specific tracks provided, make eligible for all tracks in event
	if len(trackIDs) == 0 {
		tRows, err := s.db.QueryContext(r.Context(), "SELECT id FROM tracks WHERE event_id = ?;", eventID)
		if err == nil {
			defer tRows.Close()
			for tRows.Next() {
				var tid string
				if err := tRows.Scan(&tid); err == nil {
					trackIDs = append(trackIDs, tid)
				}
			}
		}
	}

	for _, tid := range trackIDs {
		_, _ = s.db.ExecContext(r.Context(), `
			INSERT INTO judge_track_eligibility (event_id, judge_user_id, track_id, eligible, source, reason, updated_at)
			VALUES (?, ?, ?, 1, 'organizer', 'Organizer assigned', ?)
			ON CONFLICT(event_id, judge_user_id, track_id) DO UPDATE SET eligible = 1, updated_at = excluded.updated_at;
		`, eventID, targetUserID, tid, nowUTC)
	}

	if isBrowserForm(r) {
		http.Redirect(w, r, "/dashboard?msg=Judge+"+url.QueryEscape(displayName)+"+invited+successfully", http.StatusSeeOther)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":          "created",
		"judge_user_id":   targetUserID,
		"email":           normEmail,
		"display_name":    displayName,
		"capacity":        capacity,
		"eligible_tracks": trackIDs,
	})
}

// handleOrganizerUpdateJudgeEligibilityAPI updates capacity, active status, or track eligibility for a judge.
func (s *Server) handleOrganizerUpdateJudgeEligibilityAPI(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if user == nil {
		if !isBrowserForm(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		http.Redirect(w, r, "/login?error=Please+sign+in+first", http.StatusSeeOther)
		return
	}
	if !canAdminister(user) {
		if !isBrowserForm(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
			return
		}
		http.Redirect(w, r, "/dashboard?error=Organizer+privileges+required", http.StatusSeeOther)
		return
	}

	judgeID := r.PathValue("id")
	if judgeID == "" {
		http.NotFound(w, r)
		return
	}

	var eventID string
	_ = s.db.QueryRowContext(r.Context(), "SELECT id FROM events ORDER BY created_at ASC LIMIT 1;").Scan(&eventID)

	var capacity *int
	var active *bool
	var eligibleTrackIDs []string
	hasEligibleTracks := false

	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		var req struct {
			Capacity *int      `json:"capacity"`
			Active   *bool     `json:"active"`
			Tracks   *[]string `json:"tracks"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
			capacity = req.Capacity
			active = req.Active
			if req.Tracks != nil {
				hasEligibleTracks = true
				eligibleTrackIDs = *req.Tracks
			}
		}
	} else {
		_ = r.ParseForm()
		if capStr := r.FormValue("capacity"); capStr != "" {
			if c, err := strconv.Atoi(capStr); err == nil {
				capacity = &c
			}
		}
		if actStr := r.FormValue("active"); actStr != "" {
			b := actStr == "1" || actStr == "true" || actStr == "on"
			active = &b
		}
		if formTracks := r.Form["track_ids"]; len(formTracks) > 0 {
			hasEligibleTracks = true
			eligibleTrackIDs = formTracks
		}
	}

	nowUTC := time.Now().UTC().Format(time.RFC3339)

	// Update judge_profiles
	if capacity != nil && *capacity >= 0 {
		_, _ = s.db.ExecContext(r.Context(), `
			UPDATE judge_profiles SET capacity = ? WHERE event_id = ? AND user_id = ?;
		`, *capacity, eventID, judgeID)
	}
	if active != nil {
		activeInt := 0
		if *active {
			activeInt = 1
		}
		_, _ = s.db.ExecContext(r.Context(), `
			UPDATE judge_profiles SET active = ? WHERE event_id = ? AND user_id = ?;
		`, activeInt, eventID, judgeID)
	}

	// Update eligibility if provided
	if hasEligibleTracks {
		_, _ = s.db.ExecContext(r.Context(), `
			UPDATE judge_track_eligibility SET eligible = 0, updated_at = ? WHERE event_id = ? AND judge_user_id = ?;
		`, nowUTC, eventID, judgeID)

		for _, tid := range eligibleTrackIDs {
			_, _ = s.db.ExecContext(r.Context(), `
				INSERT INTO judge_track_eligibility (event_id, judge_user_id, track_id, eligible, source, reason, updated_at)
				VALUES (?, ?, ?, 1, 'organizer', 'Organizer updated eligibility', ?)
				ON CONFLICT(event_id, judge_user_id, track_id) DO UPDATE SET eligible = 1, updated_at = excluded.updated_at;
			`, eventID, judgeID, tid, nowUTC)
		}
	}

	if isBrowserForm(r) {
		http.Redirect(w, r, "/dashboard?msg=Judge+updated+successfully", http.StatusSeeOther)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":        "updated",
		"judge_user_id": judgeID,
	})
}

// handleOrganizerCreateRubricAPI authors and publishes a new rubric version with customized weights.
func (s *Server) handleOrganizerCreateRubricAPI(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if user == nil {
		if !isBrowserForm(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		http.Redirect(w, r, "/login?error=Please+sign+in+first", http.StatusSeeOther)
		return
	}
	if !canAdminister(user) {
		if !isBrowserForm(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden: organizer role required"})
			return
		}
		http.Redirect(w, r, "/dashboard?error=Organizer+privileges+required", http.StatusSeeOther)
		return
	}

	var eventID string
	_ = s.db.QueryRowContext(r.Context(), "SELECT id FROM events ORDER BY created_at ASC LIMIT 1;").Scan(&eventID)

	var rubricID, trackID string
	var criteria []judging.Criterion

	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		var req struct {
			RubricID string              `json:"rubric_id"`
			TrackID  string              `json:"track_id"`
			Criteria []judging.Criterion `json:"criteria"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid JSON payload: " + err.Error()})
			return
		}
		rubricID = strings.TrimSpace(req.RubricID)
		trackID = strings.TrimSpace(req.TrackID)
		criteria = req.Criteria
	} else {
		_ = r.ParseForm()
		rubricID = strings.TrimSpace(r.FormValue("rubric_id"))
		trackID = strings.TrimSpace(r.FormValue("track_id"))

		if criteriaJSON := r.FormValue("criteria_json"); criteriaJSON != "" {
			if err := json.Unmarshal([]byte(criteriaJSON), &criteria); err != nil {
				http.Redirect(w, r, "/dashboard?error=Invalid+criteria+JSON:+"+url.QueryEscape(err.Error()), http.StatusSeeOther)
				return
			}
		} else {
			critIDs := r.Form["crit_id"]
			critNames := r.Form["crit_name"]
			critDescs := r.Form["crit_desc"]
			critMins := r.Form["crit_min"]
			critMaxs := r.Form["crit_max"]
			critWeights := r.Form["crit_weight"]

			for i := range critIDs {
				id := strings.TrimSpace(critIDs[i])
				if id == "" {
					continue
				}
				name := id
				if i < len(critNames) && strings.TrimSpace(critNames[i]) != "" {
					name = strings.TrimSpace(critNames[i])
				}
				desc := ""
				if i < len(critDescs) {
					desc = strings.TrimSpace(critDescs[i])
				}
				minVal := 0.0
				if i < len(critMins) {
					minVal, _ = strconv.ParseFloat(critMins[i], 64)
				}
				maxVal := 5.0
				if i < len(critMaxs) {
					if mv, err := strconv.ParseFloat(critMaxs[i], 64); err == nil && mv > 0 {
						maxVal = mv
					}
				}
				weight := 1.0
				if i < len(critWeights) {
					if w, err := strconv.ParseFloat(critWeights[i], 64); err == nil {
						weight = w
					}
				}
				criteria = append(criteria, judging.Criterion{
					ID:          id,
					Name:        name,
					Description: desc,
					MinScore:    minVal,
					MaxScore:    maxVal,
					Weight:      weight,
					Required:    true,
				})
			}
		}
	}

	if rubricID == "" {
		rubricID = "default"
	}

	rubric, err := s.judgingService.CreateAndPublishRubric(r.Context(), eventID, rubricID, trackID, criteria, user.UserID)
	if err != nil {
		if !isBrowserForm(r) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		http.Redirect(w, r, "/dashboard?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}

	if isBrowserForm(r) {
		http.Redirect(w, r, "/dashboard?msg=Rubric+version+v"+strconv.Itoa(rubric.VersionNo)+"+published+successfully", http.StatusSeeOther)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(rubric)
}
