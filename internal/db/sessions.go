package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// SessionRepo manages admin GUI sessions.
type SessionRepo struct{ conn *sql.DB }

func newSessionRepo(conn *sql.DB) *SessionRepo { return &SessionRepo{conn: conn} }

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Create stores a new session (token is the raw value; only its hash is stored).
func (r *SessionRepo) Create(token, ip string, ttl time.Duration) error {
	_, err := r.conn.Exec(`
		INSERT INTO admin_sessions (token_hash, ip_address, expires_at)
		VALUES (?, ?, datetime('now', ?))
	`, hashToken(token), ip, fmt.Sprintf("+%d seconds", int(ttl.Seconds())))
	return err
}

// Validate checks whether a session token is valid and, if so, extends its expiry (sliding window).
func (r *SessionRepo) Validate(token string) (bool, error) {
	var id int64
	err := r.conn.QueryRow(`
		SELECT id FROM admin_sessions
		WHERE token_hash = ? AND expires_at > datetime('now')
	`, hashToken(token)).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	r.conn.Exec(`
		UPDATE admin_sessions
		SET last_seen_at = datetime('now'), expires_at = datetime('now', '+86400 seconds')
		WHERE id = ?
	`, id)
	return true, nil
}

// Delete removes a session by its raw token.
func (r *SessionRepo) Delete(token string) error {
	_, err := r.conn.Exec(`DELETE FROM admin_sessions WHERE token_hash = ?`, hashToken(token))
	return err
}

// CleanupExpired removes all expired sessions.
func (r *SessionRepo) CleanupExpired() error {
	_, err := r.conn.Exec(`DELETE FROM admin_sessions WHERE expires_at <= datetime('now')`)
	return err
}
