package db

import (
	"database/sql"
	"time"
)

const maxLoginAttempts = 5

// LoginAttemptRepo tracks per-IP login failures for brute-force protection.
type LoginAttemptRepo struct{ conn *sql.DB }

func newLoginAttemptRepo(conn *sql.DB) *LoginAttemptRepo { return &LoginAttemptRepo{conn: conn} }

// IsLocked returns true if the IP is currently locked out.
func (r *LoginAttemptRepo) IsLocked(ip string) (bool, error) {
	var lockedUntil sql.NullString
	err := r.conn.QueryRow(`SELECT locked_until FROM login_attempts WHERE ip = ?`, ip).Scan(&lockedUntil)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !lockedUntil.Valid {
		return false, nil
	}
	t, err := time.Parse("2006-01-02 15:04:05", lockedUntil.String)
	if err != nil {
		return false, nil
	}
	return time.Now().UTC().Before(t), nil
}

// RecordFailure increments the failure count for an IP and locks it if the threshold is reached.
func (r *LoginAttemptRepo) RecordFailure(ip string) error {
	_, err := r.conn.Exec(`
		INSERT INTO login_attempts (ip, attempts, last_attempt)
		VALUES (?, 1, datetime('now'))
		ON CONFLICT(ip) DO UPDATE SET
			attempts     = attempts + 1,
			last_attempt = datetime('now'),
			locked_until = CASE
				WHEN attempts + 1 >= ? THEN datetime('now', '+900 seconds')
				ELSE locked_until
			END
	`, ip, maxLoginAttempts)
	return err
}

// Reset clears the failure record for an IP after a successful login.
func (r *LoginAttemptRepo) Reset(ip string) error {
	_, err := r.conn.Exec(`DELETE FROM login_attempts WHERE ip = ?`, ip)
	return err
}
