package db

import (
	"database/sql"
	"github.com/bfenty/relai/internal/compress"
	"time"
)

// Router is a named virtual model that load-balances across member model mappings.
type Router struct {
	ID                  int64  `json:"id"`
	Name                string `json:"name"`
	Strategy            string `json:"strategy"`
	ModelType           string `json:"model_type"` // "chat", "embedding", or "rerank"
	CacheEnabled        bool   `json:"cache_enabled"`
	CacheTTLSeconds     int    `json:"cache_ttl_seconds"`
	QueueTimeoutSeconds int    `json:"queue_timeout_seconds"`
	Compression         string `json:"compression"`

	// Adaptive thinking (strategy adaptive_thinking). SidecarURL empty means
	// the classifier is disabled for this router: requests pass through with
	// current behavior, exactly like every pre-existing router.
	ThinkingSidecarURL        string  `json:"thinking_sidecar_url"`
	ThinkingTimeoutMs         int     `json:"thinking_timeout_ms"` // 0 -> default 50
	ThinkingDefaultLevel      string  `json:"thinking_default_level"`
	ThinkingConfidenceFloor   float64 `json:"thinking_confidence_floor"`
	ThinkingMode              string  `json:"thinking_mode"` // "shadow" (default) | "enforce"
	ThinkingParamTemplateJSON string  `json:"thinking_param_template"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RouterInput is used for creating/updating routers.
type RouterInput struct {
	Name                string `json:"name"`
	Strategy            string `json:"strategy"`
	ModelType           string `json:"model_type"`
	CacheEnabled        bool   `json:"cache_enabled"`
	CacheTTLSeconds     int    `json:"cache_ttl_seconds"`
	QueueTimeoutSeconds int    `json:"queue_timeout_seconds"`
	Compression         string `json:"compression"`

	ThinkingSidecarURL        string  `json:"thinking_sidecar_url"`
	ThinkingTimeoutMs         int     `json:"thinking_timeout_ms"`
	ThinkingDefaultLevel      string  `json:"thinking_default_level"`
	ThinkingConfidenceFloor   float64 `json:"thinking_confidence_floor"`
	ThinkingMode              string  `json:"thinking_mode"`
	ThinkingParamTemplateJSON string  `json:"thinking_param_template"`
}

// normalizeThinkingMode defaults new routers to shadow mode: classify and log,
// never inject. Enforce is an explicit opt-in per router.
func normalizeThinkingMode(m string) string {
	if m == "enforce" {
		return "enforce"
	}
	return "shadow"
}

// normalizeRouterModelType maps a router's type to one of the valid values,
// defaulting to "chat".
func normalizeRouterModelType(t string) string {
	switch t {
	case "embedding", "embed", "rerank", "chat":
		if t == "embed" {
			return "embedding"
		}
		return t
	default:
		return "chat"
	}
}

const routerColumns = `id, name, strategy, model_type, cache_enabled, cache_ttl_seconds, queue_timeout_seconds, compression,
		COALESCE(thinking_sidecar_url,''), COALESCE(thinking_timeout_ms,0), COALESCE(thinking_default_level,''),
		COALESCE(thinking_confidence_floor,0), COALESCE(thinking_mode,''), COALESCE(thinking_param_template,''),
		created_at, updated_at`

// RouterMember links a router to a model mapping.
type RouterMember struct {
	ID         int64 `json:"id"`
	RouterID   int64 `json:"router_id"`
	ModelMapID int64 `json:"model_map_id"`
	Priority   int   `json:"priority"`
}

// RouterRepo handles CRUD for routers and their members.
type RouterRepo struct {
	db *sql.DB
}

func newRouterRepo(db *sql.DB) *RouterRepo {
	return &RouterRepo{db: db}
}

func (r *RouterRepo) Create(input RouterInput) (*Router, error) {
	if input.Strategy == "" {
		input.Strategy = "sticky"
	}
	if input.CacheTTLSeconds == 0 {
		input.CacheTTLSeconds = 3600
	}
	if input.QueueTimeoutSeconds == 0 {
		input.QueueTimeoutSeconds = 60
	}
	res, err := r.db.Exec(`
		INSERT INTO routers (name, strategy, model_type, cache_enabled, cache_ttl_seconds, queue_timeout_seconds, compression,
			thinking_sidecar_url, thinking_timeout_ms, thinking_default_level, thinking_confidence_floor, thinking_mode, thinking_param_template)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.Name, input.Strategy, normalizeRouterModelType(input.ModelType), input.CacheEnabled,
		input.CacheTTLSeconds, input.QueueTimeoutSeconds, string(compress.ParseMode(input.Compression)),
		input.ThinkingSidecarURL, input.ThinkingTimeoutMs, input.ThinkingDefaultLevel,
		input.ThinkingConfidenceFloor, normalizeThinkingMode(input.ThinkingMode), input.ThinkingParamTemplateJSON)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return r.Get(id)
}

func (r *RouterRepo) Get(id int64) (*Router, error) {
	row := r.db.QueryRow(`
		SELECT `+routerColumns+`
		FROM routers WHERE id = ?`, id)
	return scanRouter(row)
}

func (r *RouterRepo) GetByName(name string) (*Router, error) {
	row := r.db.QueryRow(`
		SELECT `+routerColumns+`
		FROM routers WHERE name = ?`, name)
	rt, err := scanRouter(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return rt, err
}

func (r *RouterRepo) List() ([]Router, error) {
	rows, err := r.db.Query(`
		SELECT ` + routerColumns + `
		FROM routers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Router, 0)
	for rows.Next() {
		rt, err := scanRouter(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *rt)
	}
	return result, rows.Err()
}

func (r *RouterRepo) Update(id int64, input RouterInput) (*Router, error) {
	_, err := r.db.Exec(`
		UPDATE routers
		SET name=?, strategy=?, model_type=?, cache_enabled=?, cache_ttl_seconds=?, queue_timeout_seconds=?, compression=?,
			thinking_sidecar_url=?, thinking_timeout_ms=?, thinking_default_level=?, thinking_confidence_floor=?, thinking_mode=?, thinking_param_template=?,
			updated_at=CURRENT_TIMESTAMP
		WHERE id=?`,
		input.Name, input.Strategy, normalizeRouterModelType(input.ModelType), input.CacheEnabled,
		input.CacheTTLSeconds, input.QueueTimeoutSeconds, string(compress.ParseMode(input.Compression)),
		input.ThinkingSidecarURL, input.ThinkingTimeoutMs, input.ThinkingDefaultLevel,
		input.ThinkingConfidenceFloor, normalizeThinkingMode(input.ThinkingMode), input.ThinkingParamTemplateJSON, id)
	if err != nil {
		return nil, err
	}
	return r.Get(id)
}

func (r *RouterRepo) Delete(id int64) error {
	_, err := r.db.Exec("DELETE FROM routers WHERE id=?", id)
	return err
}

// UpsertByName creates or updates a router matched by name.
func (r *RouterRepo) UpsertByName(router Router) (*Router, error) {
	strategy := router.Strategy
	if strategy == "" {
		strategy = "sticky"
	}
	cacheTTL := router.CacheTTLSeconds
	if cacheTTL == 0 {
		cacheTTL = 3600
	}
	queueTimeout := router.QueueTimeoutSeconds
	if queueTimeout == 0 {
		queueTimeout = 60
	}
	_, err := r.db.Exec(`
		INSERT INTO routers (name, strategy, model_type, cache_enabled, cache_ttl_seconds, queue_timeout_seconds, compression)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			strategy             = excluded.strategy,
			model_type           = excluded.model_type,
			compression          = excluded.compression,
			cache_enabled        = excluded.cache_enabled,
			cache_ttl_seconds    = excluded.cache_ttl_seconds,
			queue_timeout_seconds = excluded.queue_timeout_seconds,
			updated_at           = CURRENT_TIMESTAMP`,
		router.Name, strategy, normalizeRouterModelType(router.ModelType), router.CacheEnabled,
		cacheTTL, queueTimeout, string(compress.ParseMode(router.Compression)))
	if err != nil {
		return nil, err
	}
	return r.GetByName(router.Name)
}

// Member management

func (r *RouterRepo) AddMember(routerID, modelMapID int64, priority int) (*RouterMember, error) {
	res, err := r.db.Exec(`INSERT INTO router_members (router_id, model_map_id, priority) VALUES (?, ?, ?)`,
		routerID, modelMapID, priority)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	var m RouterMember
	row := r.db.QueryRow(`SELECT id, router_id, model_map_id, priority FROM router_members WHERE id=?`, id)
	return &m, row.Scan(&m.ID, &m.RouterID, &m.ModelMapID, &m.Priority)
}

func (r *RouterRepo) RemoveMember(id int64) error {
	_, err := r.db.Exec("DELETE FROM router_members WHERE id=?", id)
	return err
}

func (r *RouterRepo) GetMembers(routerID int64) ([]RouterMember, error) {
	rows, err := r.db.Query(`
		SELECT id, router_id, model_map_id, priority FROM router_members
		WHERE router_id=? ORDER BY priority DESC, id`, routerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]RouterMember, 0)
	for rows.Next() {
		var m RouterMember
		if err := rows.Scan(&m.ID, &m.RouterID, &m.ModelMapID, &m.Priority); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}

// AllMembers returns every router_member row — used by the health monitor.
func (r *RouterRepo) AllMembers() ([]RouterMember, error) {
	rows, err := r.db.Query(`SELECT id, router_id, model_map_id, priority FROM router_members`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]RouterMember, 0)
	for rows.Next() {
		var m RouterMember
		if err := rows.Scan(&m.ID, &m.RouterID, &m.ModelMapID, &m.Priority); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}

func scanRouter(row interface{ Scan(...any) error }) (*Router, error) {
	rt := &Router{}
	err := row.Scan(&rt.ID, &rt.Name, &rt.Strategy, &rt.ModelType, &rt.CacheEnabled,
		&rt.CacheTTLSeconds, &rt.QueueTimeoutSeconds, &rt.Compression,
		&rt.ThinkingSidecarURL, &rt.ThinkingTimeoutMs, &rt.ThinkingDefaultLevel,
		&rt.ThinkingConfidenceFloor, &rt.ThinkingMode, &rt.ThinkingParamTemplateJSON,
		&rt.CreatedAt, &rt.UpdatedAt)
	return rt, err
}
