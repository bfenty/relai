package db

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const KeyPrefix = "rlai_"

var DefaultAPIKeyScopes = []string{"inference", "models:read", "embeddings", "rerank"}

// APIKey represents an API key record.
type APIKey struct {
	ID                 int64      `json:"id"`
	KeyPrefix          string     `json:"key_prefix"`
	Name               string     `json:"name"`
	Owner              string     `json:"owner"`
	UserID             *int64     `json:"user_id,omitempty"`
	Scopes             []string   `json:"scopes"`
	ExpiresAt          *time.Time `json:"expires_at,omitempty"`
	RateLimitRPM       int        `json:"rate_limit_rpm"`
	RateLimitRemaining int        `json:"rate_limit_remaining"`
	RateLimitResetAt   *time.Time `json:"rate_limit_reset_at,omitempty"`
	AllowedBackends    []int64    `json:"allowed_backends"`
	AllowedModels      []string   `json:"allowed_models"`
	IsActive           bool       `json:"is_active"`
	// Priority marks interactive traffic: >0 injects X-Halogen-Priority on
	// upstream requests so Halogen's admission reserve can place these ahead
	// of bulk queues. 0 = ordinary.
	Priority      int        `json:"priority"`
	LastUsedAt    *time.Time `json:"last_used_at,omitempty"`
	TotalCost     float64    `json:"total_cost"`
	TotalRequests int        `json:"total_requests"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	IsTest        bool       `json:"is_test"`
}

// APIKeyInput is used for creating/updating API keys.
type APIKeyInput struct {
	Name            string     `json:"name"`
	Owner           string     `json:"owner"`
	Scopes          []string   `json:"scopes,omitempty"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	RateLimitRPM    int        `json:"rate_limit_rpm"`
	UserID          *int64     `json:"user_id,omitempty"`
	AllowedBackends []int64    `json:"allowed_backends,omitempty"`
	AllowedModels   []string   `json:"allowed_models,omitempty"`
	IsActive        *bool      `json:"is_active,omitempty"`
	Priority        *int       `json:"priority,omitempty"`
	IsTest          bool       `json:"is_test"`
}

// APIKeyRepo handles CRUD operations for API keys.
type APIKeyRepo struct {
	db *sql.DB

	// validCache maps sha256(plain key) → key ID for keys that already passed
	// a bcrypt check, so the hot path avoids a bcrypt compare per stored key
	// on every request. Entries are verified against the DB on each hit
	// (active/expiry), so revocation still takes effect immediately.
	cacheMu    sync.RWMutex
	validCache map[[32]byte]int64
}

func newAPIKeyRepo(db *sql.DB) *APIKeyRepo {
	return &APIKeyRepo{db: db, validCache: make(map[[32]byte]int64)}
}

// priorityOf dereferences the optional priority input; nil means ordinary (0).
func priorityOf(p *int) int {
	if p == nil || *p < 0 {
		return 0
	}
	return *p
}

// GenerateKey creates a new random API key string.
func GenerateKey() string {
	bytes := make([]byte, 24)
	rand.Read(bytes)
	return KeyPrefix + hex.EncodeToString(bytes)
}

// Create inserts a new API key.
func (r *APIKeyRepo) Create(input APIKeyInput) (*APIKey, string, error) {
	plainKey := GenerateKey()
	hash, err := bcrypt.GenerateFromPassword([]byte(plainKey), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", fmt.Errorf("hash key: %w", err)
	}

	allowedBackends, _ := json.Marshal(input.AllowedBackends)
	allowedModels, _ := json.Marshal(input.AllowedModels)
	scopes := input.Scopes
	if len(scopes) == 0 {
		scopes = DefaultAPIKeyScopes
	}
	scopesJSON, _ := json.Marshal(scopes)

	rateLimit := input.RateLimitRPM
	if rateLimit <= 0 {
		rateLimit = 60
	}

	var userIDVal interface{}
	if input.UserID != nil {
		userIDVal = *input.UserID
	}

	result, err := r.db.Exec(`
		INSERT INTO api_keys (key_hash, key_prefix, name, rate_limit_rpm, rate_limit_remaining,
			allowed_backends, allowed_models, user_id, owner, scopes, expires_at, is_test, priority)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(hash), KeyPrefix, input.Name, rateLimit, rateLimit,
		string(allowedBackends), string(allowedModels), userIDVal, input.Owner, string(scopesJSON), input.ExpiresAt, input.IsTest, priorityOf(input.Priority))
	if err != nil {
		return nil, "", err
	}

	id, _ := result.LastInsertId()
	key, err := r.Get(id)
	return key, plainKey, err
}

// Get returns an API key by ID.
func (r *APIKeyRepo) Get(id int64) (*APIKey, error) {
	row := r.db.QueryRow(`
		SELECT id, key_prefix, name, owner, scopes, expires_at, rate_limit_rpm, rate_limit_remaining,
		       rate_limit_reset_at, allowed_backends, allowed_models, is_active,
		       last_used_at, total_cost, total_requests, created_at, updated_at, user_id, is_test, priority
		FROM api_keys WHERE id = ?`, id)
	return scanAPIKey(row, false)
}

// List returns all API keys.
func (r *APIKeyRepo) List() ([]APIKey, error) {
	return r.listWhere("", nil)
}

// ListByUser returns all API keys belonging to a user.
func (r *APIKeyRepo) ListByUser(userID int64) ([]APIKey, error) {
	return r.listWhere("WHERE user_id = ?", userID)
}

func (r *APIKeyRepo) listWhere(where string, arg any) ([]APIKey, error) {
	rows, err := r.db.Query(`
		SELECT id, key_prefix, name, owner, scopes, expires_at, rate_limit_rpm, rate_limit_remaining,
		       rate_limit_reset_at, allowed_backends, allowed_models, is_active,
		       last_used_at, total_cost, total_requests, created_at, updated_at, user_id, is_test, priority
		FROM api_keys `+where+` ORDER BY id`, func() []any {
		if arg != nil {
			return []any{arg}
		}
		return nil
	}()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	keys := make([]APIKey, 0)
	for rows.Next() {
		k, err := scanAPIKey(rows, false)
		if err != nil {
			return nil, err
		}
		keys = append(keys, *k)
	}
	return keys, rows.Err()
}

// Update modifies an existing API key.
func (r *APIKeyRepo) Update(id int64, input APIKeyInput) (*APIKey, error) {
	existing, err := r.Get(id)
	if err != nil {
		return nil, err
	}
	if input.Name == "" {
		input.Name = existing.Name
	}
	if input.Owner == "" {
		input.Owner = existing.Owner
	}
	if input.Scopes == nil {
		input.Scopes = existing.Scopes
	}
	if input.ExpiresAt == nil {
		input.ExpiresAt = existing.ExpiresAt
	}
	if input.RateLimitRPM <= 0 {
		input.RateLimitRPM = existing.RateLimitRPM
	}
	if input.AllowedBackends == nil {
		input.AllowedBackends = existing.AllowedBackends
	}
	if input.AllowedModels == nil {
		input.AllowedModels = existing.AllowedModels
	}
	if input.UserID == nil {
		input.UserID = existing.UserID
	}
	if input.IsActive == nil {
		input.IsActive = &existing.IsActive
	}
	if input.Priority == nil {
		input.Priority = &existing.Priority
	}

	allowedBackends, _ := json.Marshal(input.AllowedBackends)
	allowedModels, _ := json.Marshal(input.AllowedModels)
	scopes := input.Scopes
	if len(scopes) == 0 {
		scopes = DefaultAPIKeyScopes
	}
	scopesJSON, _ := json.Marshal(scopes)

	var userIDVal interface{}
	if input.UserID != nil {
		userIDVal = *input.UserID
	}

	_, err = r.db.Exec(`
		UPDATE api_keys SET name=?, owner=?, scopes=?, expires_at=?, rate_limit_rpm=?, allowed_backends=?, allowed_models=?,
			rate_limit_remaining=?, user_id=?, is_active=?, priority=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		input.Name, input.Owner, string(scopesJSON), input.ExpiresAt, input.RateLimitRPM, string(allowedBackends),
		string(allowedModels), input.RateLimitRPM, userIDVal, *input.IsActive, priorityOf(input.Priority), id)
	if err != nil {
		return nil, err
	}
	return r.Get(id)
}

// Delete removes an API key.
func (r *APIKeyRepo) Delete(id int64) error {
	_, err := r.db.Exec("DELETE FROM api_keys WHERE id=?", id)
	return err
}

// Validate checks if a plain API key is valid and returns the key record.
func (r *APIKeyRepo) Validate(plainKey string) (*APIKey, error) {
	fingerprint := sha256.Sum256([]byte(plainKey))

	r.cacheMu.RLock()
	id, ok := r.validCache[fingerprint]
	r.cacheMu.RUnlock()
	if ok {
		k, err := r.Get(id)
		if err == nil && k.IsActive && (k.ExpiresAt == nil || k.ExpiresAt.After(time.Now())) {
			return k, nil
		}
		r.cacheMu.Lock()
		delete(r.validCache, fingerprint)
		r.cacheMu.Unlock()
	}

	k, err := r.validateByHash(plainKey)
	if err != nil {
		return nil, err
	}
	r.cacheMu.Lock()
	r.validCache[fingerprint] = k.ID
	r.cacheMu.Unlock()
	return k, nil
}

func (r *APIKeyRepo) validateByHash(plainKey string) (*APIKey, error) {
	rows, err := r.db.Query(`
		SELECT id, key_hash, key_prefix, name, owner, scopes, expires_at, rate_limit_rpm, rate_limit_remaining,
		       rate_limit_reset_at, allowed_backends, allowed_models, is_active,
		       last_used_at, total_cost, total_requests, created_at, updated_at, user_id, is_test, priority
		FROM api_keys`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		k, keyHash, err := scanAPIKeyWithHash(rows)
		if err != nil {
			return nil, err
		}

		if !k.IsActive {
			continue
		}
		if k.ExpiresAt != nil && !k.ExpiresAt.After(time.Now()) {
			continue
		}

		if err := bcrypt.CompareHashAndPassword([]byte(keyHash), []byte(plainKey)); err == nil {
			return k, nil
		}
	}
	return nil, fmt.Errorf("invalid API key")
}

// CheckRateLimit checks and decrements the rate limit for a key.
func (r *APIKeyRepo) CheckRateLimit(id int64) (bool, error) {
	now := time.Now()
	var remaining int
	var resetAt sql.NullTime
	var limit int

	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	err = tx.QueryRow("SELECT rate_limit_remaining, rate_limit_reset_at, rate_limit_rpm FROM api_keys WHERE id=?", id).Scan(
		&remaining, &resetAt, &limit)
	if err != nil {
		return false, err
	}

	// Reset if the window has expired
	if resetAt.Valid && now.After(resetAt.Time) {
		remaining = limit
	}

	if remaining <= 0 {
		// Set new reset window
		newReset := now.Add(time.Minute)
		_, err := tx.Exec("UPDATE api_keys SET rate_limit_reset_at=? WHERE id=?", newReset, id)
		if err != nil {
			return false, err
		}
		err = tx.Commit()
		return false, err
	}

	// Decrement and update
	newRemaining := remaining - 1
	if !resetAt.Valid || resetAt.Time.Before(now) {
		newReset := now.Add(time.Minute)
		_, err := tx.Exec("UPDATE api_keys SET rate_limit_remaining=?, rate_limit_reset_at=?, last_used_at=CURRENT_TIMESTAMP WHERE id=?",
			newRemaining, newReset, id)
		if err != nil {
			return true, err
		}
		return true, tx.Commit()
	}

	_, err = tx.Exec("UPDATE api_keys SET rate_limit_remaining=?, last_used_at=CURRENT_TIMESTAMP WHERE id=?",
		newRemaining, id)
	if err != nil {
		return true, err
	}
	return true, tx.Commit()
}

// UpdateUsage updates the usage stats for an API key.
func (r *APIKeyRepo) UpdateUsage(id int64, cost float64) error {
	_, err := r.db.Exec(`
		UPDATE api_keys SET total_cost = total_cost + ?, total_requests = total_requests + 1,
			last_used_at = CURRENT_TIMESTAMP, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		cost, id)
	return err
}

// ToggleActive toggles the active status of an API key.
func (r *APIKeyRepo) ToggleActive(id int64, isActive bool) error {
	_, err := r.db.Exec("UPDATE api_keys SET is_active=?, updated_at=CURRENT_TIMESTAMP WHERE id=?",
		isActive, id)
	return err
}

// CreateTestKey creates a test API key with 2-hour expiry and all scopes.
func (r *APIKeyRepo) CreateTestKey(owner string) (*APIKey, string, error) {
	expiresAt := time.Now().Add(2 * time.Hour)
	input := APIKeyInput{
		Name:         "Test Key — " + time.Now().Format("2006-01-02 15:04:05"),
		Owner:        owner,
		Scopes:       DefaultAPIKeyScopes,
		ExpiresAt:    &expiresAt,
		RateLimitRPM: 500,
		IsTest:       true,
	}
	return r.Create(input)
}

// CleanupExpiredTestKeys deletes expired test keys and logs the action.
func (r *APIKeyRepo) CleanupExpiredTestKeys() (int, error) {
	now := time.Now()
	result, err := r.db.Exec(`
		DELETE FROM api_keys WHERE is_test = 1 AND expires_at IS NOT NULL AND expires_at <= ?`, now)
	if err != nil {
		return 0, err
	}
	deleted, _ := result.RowsAffected()
	return int(deleted), nil
}

func (k *APIKey) HasScope(scope string) bool {
	if k == nil {
		return false
	}
	if k.ID == 0 {
		return true
	}
	for _, s := range k.Scopes {
		if s == scope || s == "admin" {
			return true
		}
	}
	return false
}

type scanner interface {
	Scan(dest ...any) error
}

func scanAPIKey(s scanner, withHash bool) (*APIKey, error) {
	k, _, err := scanAPIKeyInternal(s, withHash)
	return k, err
}

func scanAPIKeyWithHash(s scanner) (*APIKey, string, error) {
	return scanAPIKeyInternal(s, true)
}

func scanAPIKeyInternal(s scanner, withHash bool) (*APIKey, string, error) {
	var keyHash string
	var allowedBackendsStr, allowedModelsStr, scopesStr string
	var userID sql.NullInt64
	var expiresAt sql.NullTime
	k := &APIKey{}

	var err error
	if withHash {
		err = s.Scan(
			&k.ID, &keyHash, &k.KeyPrefix, &k.Name, &k.Owner, &scopesStr, &expiresAt, &k.RateLimitRPM, &k.RateLimitRemaining,
			&k.RateLimitResetAt, &allowedBackendsStr, &allowedModelsStr, &k.IsActive,
			&k.LastUsedAt, &k.TotalCost, &k.TotalRequests, &k.CreatedAt, &k.UpdatedAt, &userID, &k.IsTest, &k.Priority)
	} else {
		err = s.Scan(
			&k.ID, &k.KeyPrefix, &k.Name, &k.Owner, &scopesStr, &expiresAt, &k.RateLimitRPM, &k.RateLimitRemaining,
			&k.RateLimitResetAt, &allowedBackendsStr, &allowedModelsStr, &k.IsActive,
			&k.LastUsedAt, &k.TotalCost, &k.TotalRequests, &k.CreatedAt, &k.UpdatedAt, &userID, &k.IsTest, &k.Priority)
	}
	if err != nil {
		return nil, "", err
	}
	json.Unmarshal([]byte(allowedBackendsStr), &k.AllowedBackends)
	json.Unmarshal([]byte(allowedModelsStr), &k.AllowedModels)
	json.Unmarshal([]byte(scopesStr), &k.Scopes)
	if len(k.Scopes) == 0 {
		k.Scopes = DefaultAPIKeyScopes
	}
	if expiresAt.Valid {
		k.ExpiresAt = &expiresAt.Time
	}
	if userID.Valid {
		k.UserID = &userID.Int64
	}
	return k, keyHash, nil
}
