package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const tokenRefreshBuffer = 60 * time.Second

// Session represents an OAuth2 session stored in SQLite.
type Session struct {
	ID           string
	AccessToken  string
	RefreshToken string
	TokenExpiry  time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// IsExpired returns true if the access token has expired or is about to expire.
func (s *Session) IsExpired() bool {
	return time.Now().After(s.TokenExpiry.Add(-tokenRefreshBuffer))
}

// SessionStore manages OAuth2 sessions in SQLite.
type SessionStore struct {
	db *sql.DB
}

// NewSessionStore opens the SQLite database and creates the sessions table.
func NewSessionStore(dbPath string) (*SessionStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open session db: %w", err)
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		access_token TEXT NOT NULL,
		refresh_token TEXT NOT NULL,
		token_expiry DATETIME NOT NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create sessions table: %w", err)
	}

	return &SessionStore{db: db}, nil
}

// Close closes the database connection.
func (s *SessionStore) Close() error {
	return s.db.Close()
}

// Create inserts a new session and returns its ID.
func (s *SessionStore) Create(accessToken, refreshToken string, tokenExpiry time.Time) (string, error) {
	id, err := generateSessionID()
	if err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}

	now := time.Now().UTC()
	_, err = s.db.Exec(
		`INSERT INTO sessions (id, access_token, refresh_token, token_expiry, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, accessToken, refreshToken, tokenExpiry.UTC(), now, now,
	)
	if err != nil {
		return "", fmt.Errorf("insert session: %w", err)
	}

	return id, nil
}

// Get retrieves a session by ID.
func (s *SessionStore) Get(id string) (*Session, error) {
	var sess Session
	err := s.db.QueryRow(
		`SELECT id, access_token, refresh_token, token_expiry, created_at, updated_at FROM sessions WHERE id = ?`,
		id,
	).Scan(&sess.ID, &sess.AccessToken, &sess.RefreshToken, &sess.TokenExpiry, &sess.CreatedAt, &sess.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query session: %w", err)
	}
	return &sess, nil
}

// Update updates the tokens and expiry for an existing session.
func (s *SessionStore) Update(id, accessToken, refreshToken string, tokenExpiry time.Time) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET access_token = ?, refresh_token = ?, token_expiry = ?, updated_at = ? WHERE id = ?`,
		accessToken, refreshToken, tokenExpiry.UTC(), time.Now().UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("update session: %w", err)
	}
	return nil
}

// Delete removes a session by ID.
func (s *SessionStore) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// generateSessionID creates a cryptographically random 32-byte hex string.
func generateSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
