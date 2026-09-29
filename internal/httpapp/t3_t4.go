package httpapp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	mrand "math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// votingRateLimiter tracks recent actions per key within a sliding time window.
type votingRateLimiter struct {
	mu      sync.Mutex
	window  time.Duration
	maxHits int
	hits    map[string][]time.Time
}

func newVotingRateLimiter(window time.Duration, maxHits int) *votingRateLimiter {
	return &votingRateLimiter{
		window:  window,
		maxHits: maxHits,
		hits:    make(map[string][]time.Time),
	}
}

// Allow returns true if the key is permitted to proceed, or false with retry-after duration.
func (rl *votingRateLimiter) Allow(key string) (bool, time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	timestamps := rl.hits[key]
	var valid []time.Time
	for _, t := range timestamps {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= rl.maxHits {
		oldest := valid[0]
		retryAfter := oldest.Add(rl.window).Sub(now)
		if retryAfter <= 0 {
			retryAfter = time.Second
		}
		rl.hits[key] = valid
		return false, retryAfter
	}

	valid = append(valid, now)
	rl.hits[key] = valid
	return true, 0
}

// clientIP extracts the client IP address from proxy headers or RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

// shuffleProjects shuffles the project slice deterministically using a seed, or based on time.
func shuffleProjects(projects []ProjectView, seedStr string) []ProjectView {
	if len(projects) <= 1 {
		return projects
	}
	shuffled := make([]ProjectView, len(projects))
	copy(shuffled, projects)

	var seed int64
	if seedStr != "" {
		if parsed, err := strconv.ParseInt(seedStr, 10, 64); err == nil {
			seed = parsed
		} else {
			h := sha256.Sum256([]byte(seedStr))
			seed = int64(h[0]) | int64(h[1])<<8 | int64(h[2])<<16 | int64(h[3])<<24
		}
	} else {
		seed = time.Now().UnixNano()
	}

	rng := mrand.New(mrand.NewSource(seed))
	rng.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})
	return shuffled
}

func (s *Server) getEventID(ctx context.Context) (string, error) {
	var eventID string
	err := s.db.QueryRowContext(ctx, "SELECT id FROM events ORDER BY created_at ASC LIMIT 1;").Scan(&eventID)
	if err != nil {
		return "", err
	}
	return eventID, nil
}

func (s *Server) getCommunitySettings(ctx context.Context, eventID string) (votingOpen bool, resultsHidden bool, err error) {
	votingOpen = true
	resultsHidden = true
	var vOpen, rHidden int
	err = s.db.QueryRowContext(ctx, `
		SELECT voting_open, results_hidden 
		FROM event_community_settings 
		WHERE event_id = ?;
	`, eventID).Scan(&vOpen, &rHidden)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return true, true, nil
		}
		return true, true, err
	}
	return vOpen == 1, rHidden == 1, nil
}

func (s *Server) logVoteAudit(ctx context.Context, eventID, userID, projectID, action, ip, userAgent string) error {
	auditID := newID("aud")
	nowUTC := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO vote_audit_events (id, event_id, user_id, project_id, action, ip_address, user_agent, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?);
	`, auditID, eventID, userID, projectID, action, ip, userAgent, nowUTC)
	return err
}

// -------------------------------------------------------------------------
// T3.1 - T3.3 Community Voting Endpoints & Anti-Cheat
// -------------------------------------------------------------------------

// handleVoteProjectAPI handles POST /api/v1/projects/{id}/vote
func (s *Server) handleVoteProjectAPI(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if user == nil {
		http.Error(w, `{"error": "unauthorized: authentication required to vote"}`, http.StatusUnauthorized)
		return
	}

	projectID := r.PathValue("id")
	if projectID == "" {
		http.Error(w, `{"error": "missing project id"}`, http.StatusBadRequest)
		return
	}

	// Rate limiting by user ID and client IP
	key := fmt.Sprintf("%s:%s", user.UserID, clientIP(r))
	if s.rateLimiter != nil {
		allowed, retryAfter := s.rateLimiter.Allow(key)
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":             "rate limit exceeded: too many voting requests",
				"retry_after_secs": int(retryAfter.Seconds()),
			})
			return
		}
	}

	// Verify project exists and fetch event_id
	var eventID string
	err := s.db.QueryRowContext(r.Context(), "SELECT event_id FROM projects WHERE id = ?;", projectID).Scan(&eventID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, `{"error": "project not found"}`, http.StatusNotFound)
			return
		}
		http.Error(w, `{"error": "internal database error"}`, http.StatusInternalServerError)
		return
	}

	// Check if community voting window is open
	vOpen, _, err := s.getCommunitySettings(r.Context(), eventID)
	if err == nil && !vOpen {
		http.Error(w, `{"error": "community voting is currently closed"}`, http.StatusForbidden)
		return
	}

	// Duplicate vote protection (check if user already voted for this project)
	var existingCount int
	err = s.db.QueryRowContext(r.Context(), `
		SELECT COUNT(*) FROM project_votes WHERE event_id = ? AND project_id = ? AND user_id = ?;
	`, eventID, projectID, user.UserID).Scan(&existingCount)
	if err != nil {
		http.Error(w, `{"error": "database error"}`, http.StatusInternalServerError)
		return
	}
	if existingCount > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":      "duplicate vote: you have already voted for this project",
			"project_id": projectID,
		})
		return
	}

	// Cast vote
	voteID := newID("vote")
	nowUTC := time.Now().UTC().Format(time.RFC3339)
	_, err = s.db.ExecContext(r.Context(), `
		INSERT INTO project_votes (id, event_id, project_id, user_id, created_at)
		VALUES (?, ?, ?, ?, ?);
	`, voteID, eventID, projectID, user.UserID, nowUTC)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":      "duplicate vote constraint: vote already exists",
				"project_id": projectID,
			})
			return
		}
		http.Error(w, `{"error": "failed to record vote"}`, http.StatusInternalServerError)
		return
	}

	// Audit trail record
	_ = s.logVoteAudit(r.Context(), eventID, user.UserID, projectID, "VOTE_CAST", clientIP(r), r.UserAgent())

	// Dispatch webhook
	s.dispatchWebhook(r.Context(), eventID, "vote.cast", map[string]any{
		"event":      "vote.cast",
		"project_id": projectID,
		"user_id":    user.UserID,
		"timestamp":  nowUTC,
	})

	if strings.Contains(r.Header.Get("Accept"), "text/html") || (!strings.Contains(r.Header.Get("Accept"), "application/json") && strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded")) {
		http.Redirect(w, r, fmt.Sprintf("/projects/%s", projectID), http.StatusSeeOther)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":     "ok",
		"message":    "vote successfully recorded",
		"vote_id":    voteID,
		"project_id": projectID,
	})
}

// handleRetractVoteAPI handles DELETE /api/v1/projects/{id}/vote
func (s *Server) handleRetractVoteAPI(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if user == nil {
		http.Error(w, `{"error": "unauthorized: authentication required to retract vote"}`, http.StatusUnauthorized)
		return
	}

	// Rate limiting by user ID and client IP
	key := fmt.Sprintf("%s:%s", user.UserID, clientIP(r))
	if s.rateLimiter != nil {
		allowed, retryAfter := s.rateLimiter.Allow(key)
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":             "rate limit exceeded: too many voting requests",
				"retry_after_secs": int(retryAfter.Seconds()),
			})
			return
		}
	}

	projectID := r.PathValue("id")
	if projectID == "" {
		http.Error(w, `{"error": "missing project id"}`, http.StatusBadRequest)
		return
	}

	var eventID string
	err := s.db.QueryRowContext(r.Context(), "SELECT event_id FROM projects WHERE id = ?;", projectID).Scan(&eventID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, `{"error": "project not found"}`, http.StatusNotFound)
			return
		}
		http.Error(w, `{"error": "database error"}`, http.StatusInternalServerError)
		return
	}

	res, err := s.db.ExecContext(r.Context(), `
		DELETE FROM project_votes WHERE project_id = ? AND user_id = ?;
	`, projectID, user.UserID)
	if err != nil {
		http.Error(w, `{"error": "failed to retract vote"}`, http.StatusInternalServerError)
		return
	}

	rows, _ := res.RowsAffected()
	if rows == 0 {
		http.Error(w, `{"error": "no vote found for this project to retract"}`, http.StatusNotFound)
		return
	}

	_ = s.logVoteAudit(r.Context(), eventID, user.UserID, projectID, "VOTE_RETRACTED", clientIP(r), r.UserAgent())

	if strings.Contains(r.Header.Get("Accept"), "text/html") || (!strings.Contains(r.Header.Get("Accept"), "application/json") && strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded")) {
		http.Redirect(w, r, fmt.Sprintf("/projects/%s", projectID), http.StatusSeeOther)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":     "ok",
		"message":    "vote retracted",
		"project_id": projectID,
	})
}

// handleGetProjectVotesAPI handles GET /api/v1/projects/{id}/votes
// Respects T3 hidden results: counts hidden from non-organizers while voting is open
func (s *Server) handleGetProjectVotesAPI(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if projectID == "" {
		http.Error(w, `{"error": "missing project id"}`, http.StatusBadRequest)
		return
	}

	var eventID string
	err := s.db.QueryRowContext(r.Context(), "SELECT event_id FROM projects WHERE id = ?;", projectID).Scan(&eventID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, `{"error": "project not found"}`, http.StatusNotFound)
			return
		}
		http.Error(w, `{"error": "database error"}`, http.StatusInternalServerError)
		return
	}

	user := s.getCurrentUser(r)
	vOpen, rHidden, _ := s.getCommunitySettings(r.Context(), eventID)

	w.Header().Set("Content-Type", "application/json")

	// If results are hidden and voting is still open, non-organizers cannot see the total
	if rHidden && vOpen && !canAdminister(user) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"project_id": projectID,
			"hidden":     true,
			"message":    "community vote counts are hidden until voting closes",
		})
		return
	}

	var voteCount int
	err = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM project_votes WHERE project_id = ?;", projectID).Scan(&voteCount)
	if err != nil {
		http.Error(w, `{"error": "failed to query vote count"}`, http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"project_id": projectID,
		"hidden":     false,
		"vote_count": voteCount,
	})
}

// handleCommunityLeaderboardAPI handles GET /api/v1/community/leaderboard
func (s *Server) handleCommunityLeaderboardAPI(w http.ResponseWriter, r *http.Request) {
	eventID, err := s.getEventID(r.Context())
	if err != nil {
		http.Error(w, `{"error": "no event found"}`, http.StatusNotFound)
		return
	}

	user := s.getCurrentUser(r)
	vOpen, rHidden, _ := s.getCommunitySettings(r.Context(), eventID)

	w.Header().Set("Content-Type", "application/json")

	if rHidden && vOpen && !canAdminister(user) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"hidden":  true,
			"message": "community leaderboard is hidden until the voting window closes",
			"entries": []any{},
		})
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT p.id, s.title, p.team_id, COUNT(v.id) as vote_count
		FROM projects p
		JOIN submissions s ON p.id = s.project_id AND s.state = 'SUBMITTED'
		LEFT JOIN project_votes v ON p.id = v.project_id
		WHERE p.event_id = ?
		GROUP BY p.id, s.title, p.team_id
		ORDER BY vote_count DESC, p.id ASC;
	`, eventID)
	if err != nil {
		http.Error(w, `{"error": "failed to fetch community leaderboard"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type leaderboardEntry struct {
		Rank      int    `json:"rank"`
		ProjectID string `json:"project_id"`
		Title     string `json:"title"`
		TeamID    string `json:"team_id"`
		Votes     int    `json:"votes"`
	}

	var entries []leaderboardEntry
	rank := 1
	for rows.Next() {
		var e leaderboardEntry
		if err := rows.Scan(&e.ProjectID, &e.Title, &e.TeamID, &e.Votes); err == nil {
			e.Rank = rank
			entries = append(entries, e)
			rank++
		}
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"hidden":  false,
		"event_id": eventID,
		"entries": entries,
	})
}

// -------------------------------------------------------------------------
// T3.4 Comments Endpoints
// -------------------------------------------------------------------------

type CommentView struct {
	ID         string `json:"id"`
	ProjectID  string `json:"project_id"`
	UserID     string `json:"user_id"`
	AuthorName string `json:"author_name"`
	Content    string `json:"content"`
	CreatedAt  string `json:"created_at"`
}

// handleGetProjectCommentsAPI handles GET /api/v1/projects/{id}/comments
func (s *Server) handleGetProjectCommentsAPI(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if projectID == "" {
		http.Error(w, `{"error": "missing project id"}`, http.StatusBadRequest)
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, project_id, user_id, author_name, content, created_at
		FROM project_comments
		WHERE project_id = ?
		ORDER BY created_at ASC;
	`, projectID)
	if err != nil {
		http.Error(w, `{"error": "failed to query comments"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var comments []CommentView
	for rows.Next() {
		var c CommentView
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.UserID, &c.AuthorName, &c.Content, &c.CreatedAt); err == nil {
			comments = append(comments, c)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"project_id": projectID,
		"comments":   comments,
		"count":      len(comments),
	})
}

// handleCreateProjectCommentAPI handles POST /api/v1/projects/{id}/comments
func (s *Server) handleCreateProjectCommentAPI(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if user == nil {
		http.Error(w, `{"error": "unauthorized: authentication required to comment"}`, http.StatusUnauthorized)
		return
	}

	projectID := r.PathValue("id")
	if projectID == "" {
		http.Error(w, `{"error": "missing project id"}`, http.StatusBadRequest)
		return
	}

	// Rate limit check
	key := fmt.Sprintf("cmt:%s:%s", user.UserID, clientIP(r))
	if s.rateLimiter != nil {
		allowed, retryAfter := s.rateLimiter.Allow(key)
		if !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": "rate limit exceeded: too many comments posted",
			})
			return
		}
	}

	// Verify project exists
	var eventID string
	err := s.db.QueryRowContext(r.Context(), "SELECT event_id FROM projects WHERE id = ?;", projectID).Scan(&eventID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, `{"error": "project not found"}`, http.StatusNotFound)
			return
		}
		http.Error(w, `{"error": "database error"}`, http.StatusInternalServerError)
		return
	}

	// Extract comment content from JSON body or Form
	var content string
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		var req struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error": "invalid json body"}`, http.StatusBadRequest)
			return
		}
		content = req.Content
	} else {
		_ = r.ParseForm()
		content = r.FormValue("content")
	}

	content = strings.TrimSpace(content)
	if content == "" {
		http.Error(w, `{"error": "comment content cannot be empty"}`, http.StatusBadRequest)
		return
	}
	if len(content) > 2000 {
		http.Error(w, `{"error": "comment content exceeds maximum length of 2000 characters"}`, http.StatusBadRequest)
		return
	}

	authorName := user.DisplayName
	if authorName == "" {
		authorName = user.Email
	}

	commentID := newID("cmt")
	nowUTC := time.Now().UTC().Format(time.RFC3339)

	_, err = s.db.ExecContext(r.Context(), `
		INSERT INTO project_comments (id, event_id, project_id, user_id, author_name, content, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?);
	`, commentID, eventID, projectID, user.UserID, authorName, content, nowUTC)
	if err != nil {
		http.Error(w, `{"error": "failed to record comment"}`, http.StatusInternalServerError)
		return
	}

	if strings.Contains(r.Header.Get("Accept"), "text/html") || (!strings.Contains(r.Header.Get("Accept"), "application/json") && strings.Contains(contentType, "application/x-www-form-urlencoded")) {
		http.Redirect(w, r, fmt.Sprintf("/projects/%s#comments", projectID), http.StatusSeeOther)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(CommentView{
		ID:         commentID,
		ProjectID:  projectID,
		UserID:     user.UserID,
		AuthorName: authorName,
		Content:    content,
		CreatedAt:  nowUTC,
	})
}

// handleVoteAuditLogAPI handles GET /api/v1/community/audit (Organizer / Admin only)
func (s *Server) handleVoteAuditLogAPI(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if !canAdminister(user) {
		http.Error(w, `{"error": "forbidden: only organizers and admins can view the voting audit trail"}`, http.StatusForbidden)
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, event_id, user_id, project_id, action, ip_address, user_agent, created_at
		FROM vote_audit_events
		ORDER BY created_at DESC
		LIMIT 200;
	`)
	if err != nil {
		http.Error(w, `{"error": "failed to query audit log"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type auditEntry struct {
		ID        string `json:"id"`
		EventID   string `json:"event_id"`
		UserID    string `json:"user_id"`
		ProjectID string `json:"project_id"`
		Action    string `json:"action"`
		IP        string `json:"ip_address"`
		UserAgent string `json:"user_agent"`
		CreatedAt string `json:"created_at"`
	}

	var events []auditEntry
	for rows.Next() {
		var a auditEntry
		if err := rows.Scan(&a.ID, &a.EventID, &a.UserID, &a.ProjectID, &a.Action, &a.IP, &a.UserAgent, &a.CreatedAt); err == nil {
			events = append(events, a)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"audit_events": events,
		"count":        len(events),
	})
}

// -------------------------------------------------------------------------
// T4.3 Verifiable Certificates & Judge Records
// -------------------------------------------------------------------------

type CertificateData struct {
	Status          string `json:"status"`
	ProjectID       string `json:"project_id"`
	ProjectTitle    string `json:"project_title"`
	TeamID          string `json:"team_id"`
	EventID         string `json:"event_id"`
	EventName       string `json:"event_name"`
	DigestSHA256    string `json:"digest_sha256"`
	VerificationURL string `json:"verification_url"`
	IssuedAtUTC     string `json:"issued_at_utc"`
}

func computeCertificateDigest(projectID, eventID, title, teamID string) string {
	payload := fmt.Sprintf("CERT:%s:%s:%s:%s", projectID, eventID, title, teamID)
	h := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(h[:])
}

// handleProjectCertificateAPI handles GET /api/v1/projects/{id}/certificate
func (s *Server) handleProjectCertificateAPI(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if projectID == "" {
		http.Error(w, `{"error": "missing project id"}`, http.StatusBadRequest)
		return
	}

	var title, teamID, eventID, eventName, submittedAt string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT s.title, p.team_id, p.event_id, e.name, s.submitted_at
		FROM projects p
		JOIN submissions s ON p.id = s.project_id AND s.state = 'SUBMITTED'
		JOIN events e ON p.event_id = e.id
		WHERE p.id = ?;
	`, projectID).Scan(&title, &teamID, &eventID, &eventName, &submittedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, `{"error": "submitted project not found"}`, http.StatusNotFound)
			return
		}
		http.Error(w, `{"error": "database error"}`, http.StatusInternalServerError)
		return
	}

	digest := computeCertificateDigest(projectID, eventID, title, teamID)

	cert := CertificateData{
		Status:          "ISSUED",
		ProjectID:       projectID,
		ProjectTitle:    title,
		TeamID:          teamID,
		EventID:         eventID,
		EventName:       eventName,
		DigestSHA256:    digest,
		VerificationURL: fmt.Sprintf("/api/v1/projects/%s/certificate/verify?digest=%s", projectID, digest),
		IssuedAtUTC:     submittedAt,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cert)
}

// handleVerifyCertificateAPI handles GET /api/v1/projects/{id}/certificate/verify
func (s *Server) handleVerifyCertificateAPI(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	digestQuery := r.URL.Query().Get("digest")
	if projectID == "" || digestQuery == "" {
		http.Error(w, `{"error": "missing project id or digest query parameter"}`, http.StatusBadRequest)
		return
	}

	var title, teamID, eventID string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT s.title, p.team_id, p.event_id
		FROM projects p
		JOIN submissions s ON p.id = s.project_id AND s.state = 'SUBMITTED'
		WHERE p.id = ?;
	`, projectID).Scan(&title, &teamID, &eventID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, `{"error": "project not found"}`, http.StatusNotFound)
			return
		}
		http.Error(w, `{"error": "database error"}`, http.StatusInternalServerError)
		return
	}

	expectedDigest := computeCertificateDigest(projectID, eventID, title, teamID)
	isValid := subtleConstantTimeCompare(digestQuery, expectedDigest)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"project_id": projectID,
		"verified":   isValid,
		"digest":     digestQuery,
		"status":     ifThenElse(isValid, "VALID", "INVALID"),
	})
}

// handleProjectCertificateHTML handles GET /projects/{id}/certificate
func (s *Server) handleProjectCertificateHTML(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if projectID == "" {
		http.NotFound(w, r)
		return
	}

	var title, teamID, eventID, eventName, submittedAt string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT s.title, p.team_id, p.event_id, e.name, s.submitted_at
		FROM projects p
		JOIN submissions s ON p.id = s.project_id AND s.state = 'SUBMITTED'
		JOIN events e ON p.event_id = e.id
		WHERE p.id = ?;
	`, projectID).Scan(&title, &teamID, &eventID, &eventName, &submittedAt)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	digest := computeCertificateDigest(projectID, eventID, title, teamID)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<title>Certificate of Completion: %s</title>
<link rel="stylesheet" href="/static/app.css">
<style>
.cert-card { max-width: 720px; margin: 3rem auto; padding: 2.5rem; border: 4px double #4338ca; border-radius: 12px; background: #fafafa; text-align: center; }
.cert-title { font-size: 2rem; color: #1e1b4b; margin-bottom: 0.5rem; text-transform: uppercase; letter-spacing: 2px; }
.cert-project { font-size: 1.6rem; color: #4338ca; font-weight: bold; margin: 1.5rem 0; }
.cert-hash { font-family: monospace; font-size: 0.85rem; background: #e0e7ff; padding: 0.5rem 1rem; border-radius: 6px; word-break: break-all; margin: 1.5rem 0; }
</style>
</head>
<body>
<div class="cert-card">
  <div class="cert-title">Certificate of Completion</div>
  <p>This certifies that the project</p>
  <div class="cert-project">%s</div>
  <p>developed by Team <strong>%s</strong> was officially submitted to <strong>%s</strong>.</p>
  <p>Cryptographic Integrity Seal:</p>
  <div class="cert-hash">%s</div>
  <p><small>Issued: %s &bull; Verifiable via <code>/api/v1/projects/%s/certificate/verify</code></small></p>
  <div style="margin-top: 2rem;"><a href="/projects/%s" class="btn btn-outline">&larr; Back to Project</a></div>
</div>
</body>
</html>`,
		html.EscapeString(title),
		html.EscapeString(title),
		html.EscapeString(teamID),
		html.EscapeString(eventName),
		digest,
		html.EscapeString(submittedAt),
		html.EscapeString(projectID),
		html.EscapeString(projectID),
	)
}

// handleJudgeVerifiableRecordAPI handles GET /api/v1/judges/{id}/record
// Role isolation check: Only the judge themselves or an organizer/admin can view this record.
func (s *Server) handleJudgeVerifiableRecordAPI(w http.ResponseWriter, r *http.Request) {
	judgeID := r.PathValue("id")
	if judgeID == "" {
		http.Error(w, `{"error": "missing judge id"}`, http.StatusBadRequest)
		return
	}

	user := s.getCurrentUser(r)
	if user == nil {
		http.Error(w, `{"error": "unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// T2 Judge isolation invariant: peer judges must NOT inspect another judge's evaluation records
	if user.UserID != judgeID && !canAdminister(user) {
		http.Error(w, `{"error": "forbidden: peer judges cannot view other judge evaluation records"}`, http.StatusForbidden)
		return
	}

	// Query judge's submitted ballots
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT b.id, b.project_id, b.version_no, b.created_at
		FROM ballot_versions b
		WHERE b.judge_user_id = ? AND b.save_kind = 'SUBMIT'
		ORDER BY b.created_at ASC;
	`, judgeID)
	if err != nil {
		http.Error(w, `{"error": "database error"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type ballotRecord struct {
		BallotID    string `json:"ballot_id"`
		ProjectID   string `json:"project_id"`
		Version     int    `json:"version_no"`
		SubmittedAt string `json:"submitted_at"`
	}

	var ballots []ballotRecord
	hasher := sha256.New()
	for rows.Next() {
		var b ballotRecord
		if err := rows.Scan(&b.BallotID, &b.ProjectID, &b.Version, &b.SubmittedAt); err == nil {
			ballots = append(ballots, b)
			hasher.Write([]byte(fmt.Sprintf("%s:%s:%d|", b.BallotID, b.ProjectID, b.Version)))
		}
	}

	seal := hex.EncodeToString(hasher.Sum(nil))

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"judge_user_id":       judgeID,
		"completed_ballots":   len(ballots),
		"evaluation_records":  ballots,
		"integrity_seal_sha": seal,
		"verified":            true,
	})
}

// -------------------------------------------------------------------------
// T4.2 Webhook Subscriptions & Asynchronous Dispatcher
// -------------------------------------------------------------------------

// handleListWebhooksAPI handles GET /api/v1/webhooks
func (s *Server) handleListWebhooksAPI(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if !canAdminister(user) {
		http.Error(w, `{"error": "forbidden: organizer privileges required"}`, http.StatusForbidden)
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id, event_id, target_url, event_type, secret, is_active, created_at
		FROM webhook_subscriptions
		ORDER BY created_at DESC;
	`)
	if err != nil {
		http.Error(w, `{"error": "failed to query webhooks"}`, http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type webhookSub struct {
		ID        string `json:"id"`
		EventID   string `json:"event_id"`
		TargetURL string `json:"target_url"`
		EventType string `json:"event_type"`
		IsActive  bool   `json:"is_active"`
		CreatedAt string `json:"created_at"`
	}

	var subs []webhookSub
	for rows.Next() {
		var sub webhookSub
		var secret string
		var activeInt int
		if err := rows.Scan(&sub.ID, &sub.EventID, &sub.TargetURL, &sub.EventType, &secret, &activeInt, &sub.CreatedAt); err == nil {
			sub.IsActive = (activeInt == 1)
			subs = append(subs, sub)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"subscriptions": subs,
		"count":         len(subs),
	})
}

// handleCreateWebhookAPI handles POST /api/v1/webhooks
func (s *Server) handleCreateWebhookAPI(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if !canAdminister(user) {
		http.Error(w, `{"error": "forbidden: organizer privileges required"}`, http.StatusForbidden)
		return
	}

	var req struct {
		TargetURL string `json:"target_url"`
		EventType string `json:"event_type"`
		Secret    string `json:"secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid json payload"}`, http.StatusBadRequest)
		return
	}

	req.TargetURL = strings.TrimSpace(req.TargetURL)
	if req.TargetURL == "" || (!strings.HasPrefix(req.TargetURL, "http://") && !strings.HasPrefix(req.TargetURL, "https://")) {
		http.Error(w, `{"error": "target_url must be a valid http or https URL"}`, http.StatusBadRequest)
		return
	}

	if req.EventType == "" {
		req.EventType = "*"
	}

	eventID, err := s.getEventID(r.Context())
	if err != nil {
		http.Error(w, `{"error": "no event configured"}`, http.StatusBadRequest)
		return
	}

	subID := newID("wh")
	nowUTC := time.Now().UTC().Format(time.RFC3339)

	_, err = s.db.ExecContext(r.Context(), `
		INSERT INTO webhook_subscriptions (id, event_id, target_url, event_type, secret, is_active, created_at)
		VALUES (?, ?, ?, ?, ?, 1, ?);
	`, subID, eventID, req.TargetURL, req.EventType, req.Secret, nowUTC)
	if err != nil {
		http.Error(w, `{"error": "failed to register webhook"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":         subID,
		"target_url": req.TargetURL,
		"event_type": req.EventType,
		"created_at": nowUTC,
		"status":     "active",
	})
}

// handleDeleteWebhookAPI handles DELETE /api/v1/webhooks/{id}
func (s *Server) handleDeleteWebhookAPI(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if !canAdminister(user) {
		http.Error(w, `{"error": "forbidden: organizer privileges required"}`, http.StatusForbidden)
		return
	}

	subID := r.PathValue("id")
	if subID == "" {
		http.Error(w, `{"error": "missing webhook id"}`, http.StatusBadRequest)
		return
	}

	res, err := s.db.ExecContext(r.Context(), "DELETE FROM webhook_subscriptions WHERE id = ?;", subID)
	if err != nil {
		http.Error(w, `{"error": "failed to delete webhook"}`, http.StatusInternalServerError)
		return
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		http.Error(w, `{"error": "webhook subscription not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":      subID,
		"status":  "deleted",
		"message": "webhook subscription removed",
	})
}

// dispatchWebhook delivers events asynchronously with HMAC signature
func (s *Server) dispatchWebhook(ctx context.Context, eventID, eventType string, payload any) {
	if s.db == nil {
		return
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT target_url, secret
		FROM webhook_subscriptions
		WHERE event_id = ? AND is_active = 1 AND (event_type = ? OR event_type = '*');
	`, eventID, eventType)
	if err != nil {
		return
	}
	defer rows.Close()

	type target struct {
		url    string
		secret string
	}
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.url, &t.secret); err == nil {
			targets = append(targets, t)
		}
	}

	if len(targets) == 0 {
		return
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}

	for _, t := range targets {
		go func(targetURL, secret string) {
			req, err := http.NewRequestWithContext(context.Background(), "POST", targetURL, bytes.NewReader(bodyBytes))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", "Dogfood-Webhooks/1.0")

			if secret != "" {
				mac := hmac.New(sha256.New, []byte(secret))
				mac.Write(bodyBytes)
				sig := hex.EncodeToString(mac.Sum(nil))
				req.Header.Set("X-Dogfood-Signature", sig)
			}

			resp, err := client.Do(req)
			if err == nil {
				_ = resp.Body.Close()
			}
		}(t.url, t.secret)
	}
}

// -------------------------------------------------------------------------
// T4.5 Embeddable Gallery & Card Widgets
// -------------------------------------------------------------------------

// handleEmbedGalleryHTML handles GET /projects/embed
func (s *Server) handleEmbedGalleryHTML(w http.ResponseWriter, r *http.Request) {
	// Frame ancestors allowed for clean embedding
	w.Header().Set("Content-Security-Policy", "frame-ancestors *")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT p.id, s.title, s.summary, t.name, COALESCE(s.repo_url, '')
		FROM projects p
		JOIN submissions s ON p.id = s.project_id AND s.state = 'SUBMITTED'
		JOIN tracks t ON p.track_id = t.id
		ORDER BY p.id ASC;
	`)
	if err != nil {
		http.Error(w, "error loading projects", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type embedItem struct {
		ID      string
		Title   string
		Summary string
		Track   string
		Repo    string
	}
	var items []embedItem
	for rows.Next() {
		var it embedItem
		if err := rows.Scan(&it.ID, &it.Title, &it.Summary, &it.Track, &it.Repo); err == nil {
			items = append(items, it)
		}
	}

	fmt.Fprint(w, `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; margin: 0; padding: 12px; background: transparent; }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(240px, 1fr)); gap: 12px; }
.card { border: 1px solid #e2e8f0; border-radius: 8px; padding: 12px; background: #fff; box-shadow: 0 1px 3px rgba(0,0,0,0.05); }
.card h4 { margin: 0 0 6px 0; font-size: 15px; }
.card h4 a { color: #2563eb; text-decoration: none; }
.card p { margin: 0; font-size: 12px; color: #64748b; line-height: 1.4; }
.tag { display: inline-block; font-size: 10px; background: #f1f5f9; color: #475569; padding: 2px 6px; border-radius: 4px; margin-top: 8px; }
</style>
</head>
<body>
<div class="grid">`)
	for _, it := range items {
		fmt.Fprintf(w, `<div class="card">
<h4><a href="/projects/%s" target="_blank">%s</a></h4>
<p>%s</p>
<span class="tag">%s</span>
</div>`, html.EscapeString(it.ID), html.EscapeString(it.Title), html.EscapeString(it.Summary), html.EscapeString(it.Track))
	}
	fmt.Fprint(w, `</div></body></html>`)
}

// handleEmbedProjectHTML handles GET /projects/{id}/embed
func (s *Server) handleEmbedProjectHTML(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if projectID == "" {
		http.NotFound(w, r)
		return
	}

	var title, summary, track, repo string
	err := s.db.QueryRowContext(r.Context(), `
		SELECT s.title, s.summary, t.name, COALESCE(s.repo_url, '')
		FROM projects p
		JOIN submissions s ON p.id = s.project_id AND s.state = 'SUBMITTED'
		JOIN tracks t ON p.track_id = t.id
		WHERE p.id = ?;
	`, projectID).Scan(&title, &summary, &track, &repo)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Security-Policy", "frame-ancestors *")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; margin: 0; padding: 8px; background: transparent; }
.card { border: 1px solid #cbd5e1; border-radius: 8px; padding: 14px; background: #fff; max-width: 400px; box-shadow: 0 2px 4px rgba(0,0,0,0.06); }
.card h3 { margin: 0 0 6px 0; font-size: 16px; }
.card h3 a { color: #1d4ed8; text-decoration: none; }
.card p { margin: 0 0 8px 0; font-size: 13px; color: #475569; }
.tag { font-size: 11px; background: #e0f2fe; color: #0369a1; padding: 2px 8px; border-radius: 4px; }
</style>
</head>
<body>
<div class="card">
<h3><a href="/projects/%s" target="_blank">%s</a></h3>
<p>%s</p>
<span class="tag">%s</span>
</div>
</body>
</html>`, html.EscapeString(projectID), html.EscapeString(title), html.EscapeString(summary), html.EscapeString(track))
}

// -------------------------------------------------------------------------
// T4.6 Bulk JSON Export
// -------------------------------------------------------------------------

// handleBulkJSONExportAPI handles GET /api/v1/export.json and GET /api/v1/export/all.json
func (s *Server) handleBulkJSONExportAPI(w http.ResponseWriter, r *http.Request) {
	user := s.getCurrentUser(r)
	if !canAdminister(user) {
		http.Error(w, `{"error": "forbidden: only organizers and admins can export full platform JSON"}`, http.StatusForbidden)
		return
	}

	eventID, err := s.getEventID(r.Context())
	if err != nil {
		http.Error(w, `{"error": "no event found"}`, http.StatusNotFound)
		return
	}

	// 1. Event
	var evt struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		SubClose  string `json:"submissions_close_at"`
		JdgClose  string `json:"judging_closes_at"`
	}
	_ = s.db.QueryRowContext(r.Context(), "SELECT id, name, submissions_close_at, judging_closes_at FROM events WHERE id = ?;", eventID).
		Scan(&evt.ID, &evt.Name, &evt.SubClose, &evt.JdgClose)

	// 2. Tracks
	trackRows, _ := s.db.QueryContext(r.Context(), "SELECT id, name FROM tracks WHERE event_id = ? ORDER BY name ASC;", eventID)
	type trk struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	var tracks []trk
	if trackRows != nil {
		for trackRows.Next() {
			var t trk
			if err := trackRows.Scan(&t.ID, &t.Name); err == nil {
				tracks = append(tracks, t)
			}
		}
		trackRows.Close()
	}

	// 3. Projects
	projRows, _ := s.db.QueryContext(r.Context(), `
		SELECT p.id, p.team_id, p.track_id, s.title, s.summary, COALESCE(s.repo_url, ''), s.submitted_at
		FROM projects p
		JOIN submissions s ON p.id = s.project_id AND s.state = 'SUBMITTED'
		WHERE p.event_id = ?
		ORDER BY p.id ASC;
	`, eventID)
	type prj struct {
		ID          string `json:"id"`
		TeamID      string `json:"team_id"`
		TrackID     string `json:"track_id"`
		Title       string `json:"title"`
		Summary     string `json:"summary"`
		RepoURL     string `json:"repo_url"`
		SubmittedAt string `json:"submitted_at"`
	}
	var projects []prj
	if projRows != nil {
		for projRows.Next() {
			var p prj
			if err := projRows.Scan(&p.ID, &p.TeamID, &p.TrackID, &p.Title, &p.Summary, &p.RepoURL, &p.SubmittedAt); err == nil {
				projects = append(projects, p)
			}
		}
		projRows.Close()
	}

	// 4. Latest Published Result Run
	var runID string
	_ = s.db.QueryRowContext(r.Context(), "SELECT id FROM result_runs WHERE event_id = ? AND status = 'PUBLISHED' ORDER BY created_at DESC LIMIT 1;", eventID).Scan(&runID)

	type resEntry struct {
		ProjectID       string  `json:"project_id"`
		Rank            int     `json:"rank"`
		NormalizedScore float64 `json:"normalized_score"`
		FinalScore      float64 `json:"final_score"`
	}
	var resultsList []resEntry
	if runID != "" {
		resRows, _ := s.db.QueryContext(r.Context(), `
			SELECT project_id, rank, normalized_score, final_score
			FROM result_entries
			WHERE result_run_id = ?
			ORDER BY rank ASC;
		`, runID)
		if resRows != nil {
			for resRows.Next() {
				var re resEntry
				if err := resRows.Scan(&re.ProjectID, &re.Rank, &re.NormalizedScore, &re.FinalScore); err == nil {
					resultsList = append(resultsList, re)
				}
			}
			resRows.Close()
		}
	}

	// 5. Total community votes
	var totalVotes int
	_ = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM project_votes WHERE event_id = ?;", eventID).Scan(&totalVotes)

	bundle := map[string]any{
		"export_type":          "DOGFOOD_FULL_JSON_BUNDLE",
		"event":                evt,
		"tracks":               tracks,
		"projects":             projects,
		"published_run_id":     runID,
		"published_results":    resultsList,
		"total_community_votes": totalVotes,
		"exported_at_utc":      time.Now().UTC().Format(time.RFC3339),
	}

	bundleBytes, _ := json.Marshal(bundle)
	manifestHash := sha256.Sum256(bundleBytes)
	bundle["manifest_sha256"] = hex.EncodeToString(manifestHash[:])

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="dogfood_export_%s.json"`, eventID))
	_ = json.NewEncoder(w).Encode(bundle)
}

func subtleConstantTimeCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var res byte
	for i := 0; i < len(a); i++ {
		res |= a[i] ^ b[i]
	}
	return res == 0
}

func ifThenElse[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}
