package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Identity represents the authenticated user context.
type Identity struct {
	UserID      string
	Email       string
	DisplayName string
	Roles       map[string]bool
}

// HasRole checks if the identity possesses a given event role.
func (id *Identity) HasRole(role string) bool {
	if id == nil {
		return false
	}
	return id.Roles[role]
}

// HashToken computes the SHA-256 hex string of a session token.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// ExtractToken retrieves the session token from Cookie or Authorization header.
func ExtractToken(r *http.Request) string {
	// 1. Check Cookie header
	if cookie, err := r.Cookie("session"); err == nil && cookie.Value != "" {
		return cookie.Value
	}

	// 2. Check Cookie raw header if multiple or differently formatted
	rawCookie := r.Header.Get("Cookie")
	if strings.HasPrefix(rawCookie, "session=") {
		parts := strings.Split(rawCookie, ";")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "session=") {
				return strings.TrimPrefix(part, "session=")
			}
		}
	}

	// 3. Check Authorization header: Bearer <token>
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	}

	return ""
}

// Authenticate extracts session token, queries active session from SQLite, and returns Identity.
func Authenticate(ctx context.Context, db *sql.DB, r *http.Request) (*Identity, error) {
	token := ExtractToken(r)
	if token == "" {
		return nil, errors.New("no authentication token provided")
	}

	tokenHash := HashToken(token)
	nowUTC := time.Now().UTC().Format(time.RFC3339)

	var userID, email, displayName string
	querySession := `
	SELECT s.user_id, u.email_normalized, u.display_name
	FROM sessions s
	JOIN users u ON s.user_id = u.id
	WHERE s.token_hash = ? AND s.revoked_at IS NULL AND s.expires_at > ?;`

	err := db.QueryRowContext(ctx, querySession, tokenHash, nowUTC).Scan(&userID, &email, &displayName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("invalid or expired session")
		}
		return nil, err
	}

	// Fetch active event roles
	queryRoles := `SELECT role FROM event_roles WHERE user_id = ? AND revoked_at IS NULL;`
	rows, err := db.QueryContext(ctx, queryRoles, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	roles := make(map[string]bool)
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err == nil {
			roles[role] = true
		}
	}

	return &Identity{
		UserID:      userID,
		Email:       email,
		DisplayName: displayName,
		Roles:       roles,
	}, nil
}

// CreateSession generates a random session token, hashes it, stores it in sessions, and returns the raw token.
func CreateSession(ctx context.Context, db *sql.DB, userID string, duration time.Duration) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random token: %w", err)
	}
	token := hex.EncodeToString(b)
	tokenHash := HashToken(token)

	sessionID := fmt.Sprintf("ses_%x", b[:8])
	now := time.Now().UTC()
	nowUTC := now.Format(time.RFC3339)
	expiresUTC := now.Add(duration).Format(time.RFC3339)

	query := `INSERT INTO sessions (id, user_id, token_hash, created_at, expires_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?);`
	_, err := db.ExecContext(ctx, query, sessionID, userID, tokenHash, nowUTC, expiresUTC, nowUTC)
	if err != nil {
		return "", fmt.Errorf("failed to persist session: %w", err)
	}
	return token, nil
}

// RevokeSession revokes the current session by its raw token.
func RevokeSession(ctx context.Context, db *sql.DB, token string) error {
	if token == "" {
		return nil
	}
	tokenHash := HashToken(token)
	nowUTC := time.Now().UTC().Format(time.RFC3339)
	query := `UPDATE sessions SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL;`
	_, err := db.ExecContext(ctx, query, nowUTC, tokenHash)
	return err
}

// LoginUser authenticates a user by normalized email and creates an active session.
func LoginUser(ctx context.Context, db *sql.DB, email string, duration time.Duration) (string, *Identity, error) {
	normEmail := strings.ToLower(strings.TrimSpace(email))
	if normEmail == "" {
		return "", nil, errors.New("email cannot be empty")
	}

	var userID, displayName string
	err := db.QueryRowContext(ctx, `SELECT id, display_name FROM users WHERE email_normalized = ? AND disabled_at IS NULL;`, normEmail).Scan(&userID, &displayName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil, errors.New("user not found")
		}
		return "", nil, err
	}

	token, err := CreateSession(ctx, db, userID, duration)
	if err != nil {
		return "", nil, err
	}

	rows, err := db.QueryContext(ctx, `SELECT role FROM event_roles WHERE user_id = ? AND revoked_at IS NULL;`, userID)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()

	roles := make(map[string]bool)
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err == nil {
			roles[role] = true
		}
	}

	id := &Identity{
		UserID:      userID,
		Email:       normEmail,
		DisplayName: displayName,
		Roles:       roles,
	}
	return token, id, nil
}
