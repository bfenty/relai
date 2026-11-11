package db

import (
	"database/sql"
	"time"
)

// User represents an end-user who owns API keys.
type User struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// UserInput is used for creating/updating users.
type UserInput struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	IsActive *bool  `json:"is_active,omitempty"`
}

// UserWithStats includes aggregated usage across all of the user's API keys.
type UserWithStats struct {
	User
	KeyCount      int     `json:"key_count"`
	TotalRequests int64   `json:"total_requests"`
	TotalCost     float64 `json:"total_cost"`
}

// UserRepo handles CRUD for users.
type UserRepo struct {
	db *sql.DB
}

func newUserRepo(db *sql.DB) *UserRepo {
	return &UserRepo{db: db}
}

func (r *UserRepo) Create(input UserInput) (*User, error) {
	active := true
	if input.IsActive != nil {
		active = *input.IsActive
	}
	res, err := r.db.Exec(
		`INSERT INTO users (name, email, is_active) VALUES (?, ?, ?)`,
		input.Name, input.Email, active)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return r.Get(id)
}

func (r *UserRepo) Get(id int64) (*User, error) {
	u := &User{}
	err := r.db.QueryRow(
		`SELECT id, name, email, is_active, created_at, updated_at FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Name, &u.Email, &u.IsActive, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// List returns all users with aggregated token/cost stats summed from their keys.
func (r *UserRepo) List() ([]UserWithStats, error) {
	rows, err := r.db.Query(`
		SELECT u.id, u.name, u.email, u.is_active, u.created_at, u.updated_at,
		       COUNT(k.id),
		       COALESCE(SUM(k.total_requests), 0),
		       COALESCE(SUM(k.total_cost), 0)
		FROM users u
		LEFT JOIN api_keys k ON k.user_id = u.id
		GROUP BY u.id
		ORDER BY u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]UserWithStats, 0)
	for rows.Next() {
		var u UserWithStats
		err := rows.Scan(
			&u.ID, &u.Name, &u.Email, &u.IsActive, &u.CreatedAt, &u.UpdatedAt,
			&u.KeyCount, &u.TotalRequests, &u.TotalCost)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (r *UserRepo) Update(id int64, input UserInput) (*User, error) {
	active := true
	if input.IsActive != nil {
		active = *input.IsActive
	}
	_, err := r.db.Exec(
		`UPDATE users SET name=?, email=?, is_active=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		input.Name, input.Email, active, id)
	if err != nil {
		return nil, err
	}
	return r.Get(id)
}

func (r *UserRepo) Delete(id int64) error {
	_, err := r.db.Exec("DELETE FROM users WHERE id=?", id)
	return err
}
