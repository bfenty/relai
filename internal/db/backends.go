package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// BackendProvider describes the upstream wire protocol. Existing hosts default
// to OpenAI-compatible because that is the protocol Relai has always proxied.
type BackendProvider string

const (
	BackendProviderOpenAICompatible BackendProvider = "openai-compatible"
	BackendProviderOpenAI           BackendProvider = "openai"
	BackendProviderAnthropic        BackendProvider = "anthropic"
)

// BackendOrigin distinguishes the local fleet from paid/remote fallback hosts.
type BackendOrigin string

const (
	BackendOriginLocal    BackendOrigin = "local"
	BackendOriginExternal BackendOrigin = "external"
)

// Backend represents an LLM backend machine.
type Backend struct {
	ID              int64           `json:"id"`
	Name            string          `json:"name"`
	BaseURL         string          `json:"base_url"`
	Provider        BackendProvider `json:"provider"`
	Origin          BackendOrigin   `json:"origin"`
	APIKey          string          `json:"api_key,omitempty"`
	HasAPIKey       bool            `json:"has_api_key"`
	Weight          int             `json:"weight"`
	MaxConcurrent   int             `json:"max_concurrent"`
	Status          string          `json:"status"`
	Color           string          `json:"color"`
	LastLatencyMs   int             `json:"last_latency_ms"`
	ArchivedAt      *time.Time      `json:"archived_at,omitempty"`
	LastHealthCheck *time.Time      `json:"last_health_check,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// BackendInput is used for creating/updating backends.
type BackendInput struct {
	Name          string          `json:"name"`
	BaseURL       string          `json:"base_url"`
	Provider      BackendProvider `json:"provider,omitempty"`
	Origin        BackendOrigin   `json:"origin,omitempty"`
	APIKey        string          `json:"api_key,omitempty"`
	Weight        int             `json:"weight"`
	MaxConcurrent int             `json:"max_concurrent"`
	Status        string          `json:"status,omitempty"`
	Color         string          `json:"color,omitempty"`
}

// BackendRepo handles CRUD operations for backends.
type BackendRepo struct {
	db *sql.DB
}

func newBackendRepo(db *sql.DB) *BackendRepo {
	return &BackendRepo{db: db}
}

// Create inserts a new backend.
func (r *BackendRepo) Create(input BackendInput) (*Backend, error) {
	if input.Weight <= 0 {
		input.Weight = 1
	}
	if input.Status == "" {
		input.Status = "active"
	}

	if input.MaxConcurrent < 1 {
		input.MaxConcurrent = 1
	}
	input.Provider = normalizeBackendProvider(input.Provider)
	input.Origin = normalizeBackendOrigin(input.Origin)

	result, err := r.db.Exec(`
		INSERT INTO backends (name, base_url, provider, origin, api_key, weight, max_concurrent, status, color)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.Name, input.BaseURL, input.Provider, input.Origin, input.APIKey, input.Weight, input.MaxConcurrent, input.Status, input.Color)
	if err != nil {
		return nil, err
	}

	id, _ := result.LastInsertId()
	return r.Get(id)
}

// Get returns a backend by ID.
func (r *BackendRepo) Get(id int64) (*Backend, error) {
	b := &Backend{}
	err := r.db.QueryRow(`
		SELECT id, name, base_url, provider, origin, api_key, weight, max_concurrent, status, color,
		       last_latency_ms, last_health_check, created_at, updated_at
		FROM backends WHERE id = ?`, id).Scan(
		&b.ID, &b.Name, &b.BaseURL, &b.Provider, &b.Origin, &b.APIKey, &b.Weight, &b.MaxConcurrent, &b.Status, &b.Color,
		&b.LastLatencyMs, &b.LastHealthCheck, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// List returns all backends.
// List returns hosts that are still in use. Archived hosts are retired from the UI
// and from routing but their rows are kept so historical request_logs keep naming
// the host that served them.
func (r *BackendRepo) List() ([]Backend, error) {
	return r.list(" WHERE archived_at IS NULL")
}

// ListIncludingArchived returns every host, retired ones included.
func (r *BackendRepo) ListIncludingArchived() ([]Backend, error) {
	return r.list("")
}

func (r *BackendRepo) list(where string) ([]Backend, error) {
	rows, err := r.db.Query(`
		SELECT id, name, base_url, provider, origin, api_key, weight, max_concurrent, status, color,
		       last_latency_ms, last_health_check, created_at, updated_at, archived_at
		FROM backends` + where + ` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	backends := make([]Backend, 0)
	for rows.Next() {
		var b Backend
		err := rows.Scan(&b.ID, &b.Name, &b.BaseURL, &b.Provider, &b.Origin, &b.APIKey, &b.Weight, &b.MaxConcurrent, &b.Status, &b.Color,
			&b.LastLatencyMs, &b.LastHealthCheck, &b.CreatedAt, &b.UpdatedAt, &b.ArchivedAt)
		if err != nil {
			return nil, err
		}
		backends = append(backends, b)
	}
	return backends, rows.Err()
}

// Update modifies an existing backend. An empty APIKey preserves the stored
// key so redacted admin responses can be echoed back without wiping it.
func (r *BackendRepo) Update(id int64, input BackendInput) (*Backend, error) {
	if input.MaxConcurrent < 1 {
		input.MaxConcurrent = 1
	}
	// Empty values mean an older client did not know about classification, so
	// preserve the stored value exactly as we do for API keys and status.
	if input.Provider != "" {
		input.Provider = normalizeBackendProvider(input.Provider)
	}
	if input.Origin != "" {
		input.Origin = normalizeBackendOrigin(input.Origin)
	}
	// status is omitempty in BackendInput: an update that does not carry it (e.g. the
	// host edit form, which has no status field) must preserve the stored value rather
	// than blanking it. A blank status drops the host out of ListActive() and silently
	// removes it from routing. Same CASE idiom as api_key above.
	_, err := r.db.Exec(`
		UPDATE backends SET name=?, base_url=?,
			provider = CASE WHEN ? = '' THEN provider ELSE ? END,
			origin = CASE WHEN ? = '' THEN origin ELSE ? END,
			api_key = CASE WHEN ? = '' THEN api_key ELSE ? END,
			weight=?, max_concurrent=?,
			status = CASE WHEN ? = '' THEN status ELSE ? END,
			color=?, updated_at=CURRENT_TIMESTAMP
		WHERE id=?`,
		input.Name, input.BaseURL, input.Provider, input.Provider, input.Origin, input.Origin,
		input.APIKey, input.APIKey, input.Weight, input.MaxConcurrent,
		input.Status, input.Status, input.Color, id)
	if err != nil {
		return nil, err
	}
	return r.Get(id)
}

// Archive retires a host: it disappears from the hosts list and from routing, but
// its row survives so request_logs keep naming the host that served them. This is
// what "delete" does by default - a stale host should stop cluttering the UI without
// taking its traffic history with it.
func (r *BackendRepo) Archive(id int64) error {
	res, err := r.db.Exec(
		`UPDATE backends SET archived_at = CURRENT_TIMESTAMP, status = 'paused', updated_at = CURRENT_TIMESTAMP
		 WHERE id = ? AND archived_at IS NULL`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("backend %d not found or already archived", id)
	}
	return nil
}

// Unarchive returns a retired host to the pool, paused so it does not start taking
// traffic until it has been checked.
func (r *BackendRepo) Unarchive(id int64) error {
	_, err := r.db.Exec(
		`UPDATE backends SET archived_at = NULL, status = 'paused', updated_at = CURRENT_TIMESTAMP
		 WHERE id = ?`, id)
	return err
}

// Delete permanently removes a backend and everything that depends on it.
//
// request_logs.backend_id references backends(id) with no ON DELETE action, so with
// foreign keys enforced this fails outright for any host that has served traffic.
// Callers who genuinely want the row gone accept losing the host attribution on those
// logs, so the reference is cleared rather than the log rows removed: each log keeps
// its model, tokens, cost and latency. Prefer Archive unless the history is unwanted.
//
// model_mappings already cascades, so its rows go with the backend.
func (r *BackendRepo) Delete(id int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("UPDATE request_logs SET backend_id = NULL WHERE backend_id = ?", id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM backends WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateHealth updates the health check status for a backend.
func (r *BackendRepo) UpdateHealth(id int64, status string, latencyMs int) error {
	now := time.Now()
	_, err := r.db.Exec(`
		UPDATE backends SET status=?, last_latency_ms=?, last_health_check=?, updated_at=?
		WHERE id=?`, status, latencyMs, now, now, id)
	return err
}

// ListActive returns only active backends.
func (r *BackendRepo) ListActive() ([]Backend, error) {
	rows, err := r.db.Query(`
		SELECT id, name, base_url, provider, origin, api_key, weight, max_concurrent, status, color,
		       last_latency_ms, last_health_check, created_at, updated_at
		FROM backends WHERE status = 'active' AND archived_at IS NULL ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	backends := make([]Backend, 0)
	for rows.Next() {
		var b Backend
		err := rows.Scan(&b.ID, &b.Name, &b.BaseURL, &b.Provider, &b.Origin, &b.APIKey, &b.Weight, &b.MaxConcurrent, &b.Status, &b.Color,
			&b.LastLatencyMs, &b.LastHealthCheck, &b.CreatedAt, &b.UpdatedAt)
		if err != nil {
			return nil, err
		}
		backends = append(backends, b)
	}
	return backends, rows.Err()
}

// GetModels returns the model mappings for a backend.
func (r *BackendRepo) GetModels(backendID int64) ([]ModelMap, error) {
	rows, err := r.db.Query(`SELECT `+modelMapColumns+` FROM model_mappings WHERE backend_id = ? ORDER BY local_model`, backendID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	models := make([]ModelMap, 0)
	for rows.Next() {
		m, err := scanModelMap(rows)
		if err != nil {
			return nil, err
		}
		models = append(models, *m)
	}
	return models, rows.Err()
}

// GetByName returns a backend by name, or nil if not found.
func (r *BackendRepo) GetByName(name string) (*Backend, error) {
	b := &Backend{}
	err := r.db.QueryRow(`
		SELECT id, name, base_url, provider, origin, api_key, weight, max_concurrent, status, color,
		       last_latency_ms, last_health_check, created_at, updated_at
		FROM backends WHERE name = ?`, name).Scan(
		&b.ID, &b.Name, &b.BaseURL, &b.Provider, &b.Origin, &b.APIKey, &b.Weight, &b.MaxConcurrent, &b.Status, &b.Color,
		&b.LastLatencyMs, &b.LastHealthCheck, &b.CreatedAt, &b.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return b, err
}

// UpsertByName creates or updates a backend matched by name.
// If the incoming APIKey is empty, the existing key is preserved.
func (r *BackendRepo) UpsertByName(b Backend) (*Backend, error) {
	weight := b.Weight
	if weight <= 0 {
		weight = 1
	}
	status := b.Status
	if status == "" {
		status = "active"
	}
	maxConcurrent := b.MaxConcurrent
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	provider := normalizeBackendProvider(b.Provider)
	origin := normalizeBackendOrigin(b.Origin)
	_, err := r.db.Exec(`
		INSERT INTO backends (name, base_url, provider, origin, api_key, weight, max_concurrent, status, color)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			base_url       = excluded.base_url,
			provider       = excluded.provider,
			origin         = excluded.origin,
			weight         = excluded.weight,
			max_concurrent = excluded.max_concurrent,
			status         = excluded.status,
			color          = excluded.color,
			api_key        = CASE WHEN excluded.api_key = '' THEN backends.api_key ELSE excluded.api_key END,
			updated_at     = CURRENT_TIMESTAMP`,
		b.Name, b.BaseURL, provider, origin, b.APIKey, weight, maxConcurrent, status, b.Color)
	if err != nil {
		return nil, err
	}
	return r.GetByName(b.Name)
}

func normalizeBackendProvider(provider BackendProvider) BackendProvider {
	switch provider {
	case BackendProviderOpenAI, BackendProviderAnthropic, BackendProviderOpenAICompatible:
		return provider
	default:
		return BackendProviderOpenAICompatible
	}
}

func normalizeBackendOrigin(origin BackendOrigin) BackendOrigin {
	switch origin {
	case BackendOriginExternal, BackendOriginLocal:
		return origin
	default:
		return BackendOriginLocal
	}
}

// ParseConditions parses JSON conditions from a routing rule.
func ParseConditions(data string) map[string]interface{} {
	var result map[string]interface{}
	if data != "" && data != "{}" {
		json.Unmarshal([]byte(data), &result)
	}
	return result
}
