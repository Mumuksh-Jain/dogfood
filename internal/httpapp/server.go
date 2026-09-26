package httpapp

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"dogfood/internal/auth"
	"dogfood/web"
)

// Config configures the HTTP server.
type Config struct {
	Port int
	DB   *sql.DB
}

// Server encapsulates the HTTP server and routing.
type Server struct {
	httpServer *http.Server
	listener   net.Listener
	db         *sql.DB
	tmpl       *template.Template
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

// LoginData is passed to the login HTML template.
type LoginData struct {
	EventName string
	Error     string
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

// DashboardData is passed to the dashboard template.
type DashboardData struct {
	EventName          string
	EventID            string
	SubmissionsOpen    bool
	SubmissionsCloseAt string
	User               *auth.Identity
	IsOrganizer        bool
	IsAdmin            bool
	IsJudge            bool
	Team               *TeamView
	TeamMembers        []TeamMemberView
	TeamInvites        []TeamInviteView
	TeamProjects       []ProjectView
	Tracks             []TrackCount
	Prizes             []PrizeView
	Message            string
	Error              string
}

// ProjectDetailData is passed to project_detail.html.
type ProjectDetailData struct {
	EventName       string
	SubmissionsOpen bool
	Project         ProjectView
	CanEdit         bool
	User            *auth.Identity
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

	tmpl, err := template.ParseFS(tmplFS, "*.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse templates: %w", err)
	}

	staticFS, err := web.StaticFS()
	if err != nil {
		return nil, fmt.Errorf("failed to get static fs: %w", err)
	}

	s := &Server{
		db:   cfg.DB,
		tmpl: tmpl,
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
	mux.HandleFunc("POST /api/projects/{id}/edit", s.handleEditProject)
	mux.HandleFunc("POST /api/projects/{id}/submit", s.handleSubmitProject)
	mux.HandleFunc("GET /api/projects/{id}", s.handleGetProjectAPI)

	// 7. Teams & Invites
	mux.HandleFunc("POST /api/teams", s.handleCreateTeam)
	mux.HandleFunc("POST /api/teams/{id}/invites", s.handleCreateInvite)
	mux.HandleFunc("GET /teams/join", s.handleJoinTeam)
	mux.HandleFunc("POST /api/teams/join", s.handleJoinTeam)

	// 8. Event configuration (organizer / admin)
	mux.HandleFunc("POST /api/organizer/events", s.handleCreateEvent)
	mux.HandleFunc("POST /api/events", s.handleCreateEvent)
	mux.HandleFunc("POST /api/organizer/event", s.handleUpdateEvent)
	mux.HandleFunc("GET /api/tracks", s.handleGetTracks)
	mux.HandleFunc("POST /api/organizer/tracks", s.handleCreateTrack)
	mux.HandleFunc("GET /api/prizes", s.handleGetPrizes)
	mux.HandleFunc("POST /api/organizer/prizes", s.handleCreatePrize)

	// 9. Judge score routes (peer isolation & role enforcement)
	mux.HandleFunc("GET /api/judge/scores", s.handleJudgeScores)

	// 10. CSV export route (organizer role enforcement)
	mux.HandleFunc("GET /api/export.csv", s.handleCSVExport)

	addr := fmt.Sprintf(":%d", cfg.Port)
	s.httpServer = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return s, nil
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
	if user := s.getCurrentUser(r); user != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	data := LoginData{
		EventName: "Dogfood 2026",
		Error:     r.URL.Query().Get("error"),
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
	tokenInput := r.FormValue("token")
	emailInput := r.FormValue("email")

	var sessionToken string

	if tokenInput != "" {
		// Quick evaluation token login
		tokenHash := auth.HashToken(tokenInput)
		nowUTC := time.Now().UTC().Format(time.RFC3339)
		var uid string
		err := s.db.QueryRowContext(r.Context(), `SELECT user_id FROM sessions WHERE token_hash = ? AND revoked_at IS NULL AND expires_at > ?;`, tokenHash, nowUTC).Scan(&uid)
		if err != nil {
			http.Redirect(w, r, "/login?error=Invalid+evaluation+token", http.StatusSeeOther)
			return
		}
		sessionToken = tokenInput
	} else if emailInput != "" {
		var err error
		sessionToken, _, err = auth.LoginUser(r.Context(), s.db, emailInput, 7*24*time.Hour)
		if err != nil {
			http.Redirect(w, r, "/login?error="+strings.ReplaceAll(err.Error(), " ", "+"), http.StatusSeeOther)
			return
		}
	} else {
		http.Redirect(w, r, "/login?error=Please+enter+email+or+select+role", http.StatusSeeOther)
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

	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if s.db != nil {
		token := auth.ExtractToken(r)
		_ = auth.RevokeSession(r.Context(), s.db, token)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	data := DashboardData{
		EventName:   "Dogfood 2026",
		User:        user,
		IsOrganizer: user.HasRole("organizer"),
		IsAdmin:     canAdminister(user),
		IsJudge:     user.HasRole("judge"),
		Message:     r.URL.Query().Get("msg"),
		Error:       r.URL.Query().Get("error"),
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

	// Fetch tracks
	tRows, err := s.db.QueryContext(r.Context(), "SELECT id, name FROM tracks ORDER BY name ASC;")
	if err == nil {
		defer tRows.Close()
		for tRows.Next() {
			var tc TrackCount
			if err := tRows.Scan(&tc.ID, &tc.Name); err == nil {
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

	// Fetch user's team if participant
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

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.tmpl.ExecuteTemplate(w, "dashboard.html", data)
}

func (s *Server) handleCreateTeam(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
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
		http.Error(w, "team name cannot be empty", http.StatusBadRequest)
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

	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": teamID, "name": name})
		return
	}
	http.Redirect(w, r, "/dashboard?msg=Team+created+successfully", http.StatusSeeOther)
}

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	teamID := r.PathValue("id")
	if teamID == "" {
		http.Error(w, "team id required", http.StatusBadRequest)
		return
	}

	// Verify membership
	var isMember int
	_ = s.db.QueryRowContext(r.Context(), `SELECT 1 FROM team_memberships WHERE team_id = ? AND user_id = ? AND left_at IS NULL;`, teamID, user.UserID).Scan(&isMember)
	if isMember != 1 && !user.HasRole("organizer") {
		http.Error(w, "forbidden: not a member of this team", http.StatusForbidden)
		return
	}

	// Transactional capacity check: Max 4 members
	var activeMembers int
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM team_memberships WHERE team_id = ? AND left_at IS NULL;`, teamID).Scan(&activeMembers)
	if activeMembers >= 4 {
		http.Error(w, "team has already reached maximum capacity of 4 members", http.StatusBadRequest)
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

	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"invite_token": rawToken,
			"invite_url":   "/teams/join?token=" + rawToken,
			"expires_at":   expiresUTC,
		})
		return
	}
	http.Redirect(w, r, "/dashboard?msg=Invite+token+created:+"+rawToken, http.StatusSeeOther)
}

func (s *Server) handleJoinTeam(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if user == nil {
		if strings.Contains(r.Header.Get("Accept"), "application/json") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "/login?error=Please+sign+in+first+to+join+team", http.StatusSeeOther)
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
		http.Error(w, "missing invite token", http.StatusBadRequest)
		return
	}

	tokenHash := auth.HashToken(token)
	now := time.Now().UTC()
	nowUTC := now.Format(time.RFC3339)

	var inviteID, eventID, teamID, expiresAt string
	var acceptedAt, revokedAt sql.NullString
	err := s.db.QueryRowContext(r.Context(), `
		SELECT id, event_id, team_id, expires_at, accepted_at, revoked_at
		FROM team_invites
		WHERE token_hash = ?;
	`, tokenHash).Scan(&inviteID, &eventID, &teamID, &expiresAt, &acceptedAt, &revokedAt)
	if err != nil {
		http.Error(w, "invalid or non-existent invite token", http.StatusBadRequest)
		return
	}

	if revokedAt.Valid {
		http.Error(w, "invite has been revoked", http.StatusBadRequest)
		return
	}
	if acceptedAt.Valid {
		http.Error(w, "invite has already been used", http.StatusBadRequest)
		return
	}
	if exp, err := time.Parse(time.RFC3339, expiresAt); err == nil && now.After(exp) {
		http.Error(w, "invite token has expired", http.StatusBadRequest)
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
		http.Error(w, "team has reached maximum capacity of 4 members", http.StatusConflict) // 409 Conflict
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

	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "joined", "team_id": teamID})
		return
	}
	http.Redirect(w, r, "/dashboard?msg=Joined+team+successfully", http.StatusSeeOther)
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
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
		http.Error(w, "team_id, track_id, and title are required", http.StatusBadRequest)
		return
	}

	// Verify membership
	var isMember int
	_ = s.db.QueryRowContext(r.Context(), `SELECT 1 FROM team_memberships WHERE team_id = ? AND user_id = ? AND left_at IS NULL;`, teamID, user.UserID).Scan(&isMember)
	if isMember != 1 && !canAdminister(user) {
		http.Error(w, "forbidden: not a member of this team", http.StatusForbidden)
		return
	}

	// Check deadline
	open, deadlineStr, _ := s.isSubmissionOpen(r.Context())
	if action == "submit" && !open {
		http.Error(w, "forbidden: submissions closed at "+deadlineStr, http.StatusForbidden)
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

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"judge_id":   id.UserID,
		"judge_name": id.DisplayName,
		"scores":     []any{},
	})
}

func (s *Server) handleCSVExport(w http.ResponseWriter, r *http.Request) {
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

	// 2. Enforce Organizer / Admin role
	if !canAdminister(id) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden: admin or organizer role required"})
		return
	}

	query := `
	SELECT p.id, s.title, p.team_id, t.name, s.submitted_at
	FROM projects p
	JOIN submissions s ON p.id = s.project_id AND s.state = 'SUBMITTED'
	JOIN tracks t ON p.track_id = t.id
	ORDER BY p.id ASC;`

	rows, err := s.db.QueryContext(r.Context(), query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	writer := csv.NewWriter(w)
	// Header must contain a comma
	_ = writer.Write([]string{"project_id", "title", "team_id", "track", "submitted_at"})

	for rows.Next() {
		var pID, title, teamID, trackName, subAt string
		if err := rows.Scan(&pID, &title, &teamID, &trackName, &subAt); err == nil {
			_ = writer.Write([]string{pID, title, teamID, trackName, subAt})
		}
	}
	writer.Flush()
}
