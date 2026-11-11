package db

import "database/sql"

// AdminCredential holds a hashed admin password.
type AdminCredential struct {
	ID           int64
	Username     string
	PasswordHash string
}

// AdminCredRepo manages admin login credentials.
type AdminCredRepo struct{ conn *sql.DB }

func newAdminCredRepo(conn *sql.DB) *AdminCredRepo { return &AdminCredRepo{conn: conn} }

// Get retrieves credentials for the given username; returns nil if not found.
func (r *AdminCredRepo) Get(username string) (*AdminCredential, error) {
	c := &AdminCredential{}
	err := r.conn.QueryRow(`
		SELECT id, username, password_hash FROM admin_credentials WHERE username = ?
	`, username).Scan(&c.ID, &c.Username, &c.PasswordHash)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

// Upsert inserts or replaces the password hash for the given username.
func (r *AdminCredRepo) Upsert(username, passwordHash string) error {
	_, err := r.conn.Exec(`
		INSERT INTO admin_credentials (username, password_hash)
		VALUES (?, ?)
		ON CONFLICT(username) DO UPDATE SET
			password_hash = excluded.password_hash,
			updated_at    = CURRENT_TIMESTAMP
	`, username, passwordHash)
	return err
}

// HasAny returns true if at least one admin credential exists.
func (r *AdminCredRepo) HasAny() (bool, error) {
	var count int
	err := r.conn.QueryRow(`SELECT COUNT(*) FROM admin_credentials`).Scan(&count)
	return count > 0, err
}
