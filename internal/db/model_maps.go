package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/bfenty/relai/internal/compress"
	"strings"
	"time"
)

// ModelMode is the endpoint family a mapping can serve.
type ModelMode string

const (
	ModelModeChat   ModelMode = "chat"
	ModelModeEmbed  ModelMode = "embed"
	ModelModeRerank ModelMode = "rerank"
)

// ModelMap maps a local model name to a remote model on a backend,
// optionally overriding the backend's base URL (e.g. a different port).
type ModelMap struct {
	ID                   int64       `json:"id"`
	BackendID            int64       `json:"backend_id"`
	RemoteModel          string      `json:"remote_model"`
	LocalModel           string      `json:"local_model"`
	BaseURL              string      `json:"base_url,omitempty"`
	Status               string      `json:"status"`
	LastLatencyMs        int         `json:"last_latency_ms"`
	LastHealthCheck      *time.Time  `json:"last_health_check,omitempty"`
	Params               string      `json:"params"`
	Compression          string      `json:"compression"`
	InputCostPerMillion  float64     `json:"input_cost_per_million"`
	OutputCostPerMillion float64     `json:"output_cost_per_million"`
	ModelType            string      `json:"model_type"` // "chat", "embedding", or "rerank"
	Modes                []ModelMode `json:"modes"`
}

// ModelMapInput is used for creating/updating model mappings.
type ModelMapInput struct {
	BackendID            int64       `json:"backend_id"`
	RemoteModel          string      `json:"remote_model"`
	LocalModel           string      `json:"local_model"`
	BaseURL              string      `json:"base_url,omitempty"`
	Params               string      `json:"params"`
	Compression          string      `json:"compression"`
	InputCostPerMillion  float64     `json:"input_cost_per_million"`
	OutputCostPerMillion float64     `json:"output_cost_per_million"`
	ModelType            string      `json:"model_type"`
	Modes                []ModelMode `json:"modes,omitempty"`
	Capabilities         []ModelMode `json:"capabilities,omitempty"`
}

// ModelMapRepo handles CRUD operations for model mappings.
type ModelMapRepo struct {
	db *sql.DB
}

func newModelMapRepo(db *sql.DB) *ModelMapRepo {
	return &ModelMapRepo{db: db}
}

const modelMapColumns = `id, backend_id, remote_model, local_model, base_url, status, last_latency_ms, last_health_check, params, input_cost_per_million, output_cost_per_million, model_type, modes, compression`

func scanModelMap(row interface{ Scan(...any) error }) (*ModelMap, error) {
	m := &ModelMap{}
	var modesStr string
	err := row.Scan(&m.ID, &m.BackendID, &m.RemoteModel, &m.LocalModel,
		&m.BaseURL, &m.Status, &m.LastLatencyMs, &m.LastHealthCheck,
		&m.Params, &m.InputCostPerMillion, &m.OutputCostPerMillion, &m.ModelType, &modesStr, &m.Compression)
	m.Modes = parseModes(modesStr, m.ModelType)
	return m, err
}

// Create inserts a new model mapping.
func (r *ModelMapRepo) Create(input ModelMapInput) (*ModelMap, error) {
	params, err := normalizeParams(input.Params)
	if err != nil {
		return nil, err
	}
	modelType := input.ModelType
	if modelType == "" {
		modelType = "chat"
	}
	modes, err := normalizeModes(input.modeInput(), modelType)
	if err != nil {
		return nil, err
	}
	modelType = normalizeModelType(modelType, modes)
	modesJSON, _ := json.Marshal(modes)
	result, err := r.db.Exec(`
		INSERT INTO model_mappings (backend_id, remote_model, local_model, base_url, params, input_cost_per_million, output_cost_per_million, model_type, modes, compression)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.BackendID, input.RemoteModel, input.LocalModel, input.BaseURL,
		params, input.InputCostPerMillion, input.OutputCostPerMillion, modelType, string(modesJSON),
		string(compress.ParseMode(input.Compression)))
	if err != nil {
		return nil, err
	}
	id, _ := result.LastInsertId()
	return r.Get(id)
}

// Get returns a model mapping by ID.
func (r *ModelMapRepo) Get(id int64) (*ModelMap, error) {
	row := r.db.QueryRow(`SELECT `+modelMapColumns+` FROM model_mappings WHERE id = ?`, id)
	return scanModelMap(row)
}

// Update modifies an existing model mapping.
func (r *ModelMapRepo) Update(id int64, input ModelMapInput) (*ModelMap, error) {
	params, err := normalizeParams(input.Params)
	if err != nil {
		return nil, err
	}
	modelType := input.ModelType
	if modelType == "" {
		modelType = "chat"
	}
	modes, err := normalizeModes(input.modeInput(), modelType)
	if err != nil {
		return nil, err
	}
	modelType = normalizeModelType(modelType, modes)
	modesJSON, _ := json.Marshal(modes)
	_, err = r.db.Exec(`
		UPDATE model_mappings SET remote_model=?, local_model=?, base_url=?, params=?, input_cost_per_million=?, output_cost_per_million=?, model_type=?, modes=?, compression=? WHERE id=?`,
		input.RemoteModel, input.LocalModel, input.BaseURL,
		params, input.InputCostPerMillion, input.OutputCostPerMillion, modelType, string(modesJSON),
		string(compress.ParseMode(input.Compression)), id)
	if err != nil {
		return nil, err
	}
	return r.Get(id)
}

// UpdateHealth records the result of a health check.
func (r *ModelMapRepo) UpdateHealth(id int64, status string, latencyMs int) error {
	now := time.Now()
	_, err := r.db.Exec(`
		UPDATE model_mappings SET status=?, last_latency_ms=?, last_health_check=? WHERE id=?`,
		status, latencyMs, now, id)
	return err
}

// Delete removes a model mapping.
func (r *ModelMapRepo) Delete(id int64) error {
	_, err := r.db.Exec("DELETE FROM model_mappings WHERE id=?", id)
	return err
}

// List returns all model mappings.
func (r *ModelMapRepo) List() ([]ModelMap, error) {
	rows, err := r.db.Query(`SELECT ` + modelMapColumns + ` FROM model_mappings ORDER BY backend_id, local_model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	maps := make([]ModelMap, 0)
	for rows.Next() {
		m, err := scanModelMap(rows)
		if err != nil {
			return nil, err
		}
		maps = append(maps, *m)
	}
	return maps, rows.Err()
}

// ResolveModel finds which backends support a given local model name.
func (r *ModelMapRepo) ResolveModel(localModel string) ([]ModelMap, error) {
	rows, err := r.db.Query(
		`SELECT `+modelMapColumns+` FROM model_mappings WHERE local_model = ?`, localModel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	maps := make([]ModelMap, 0)
	for rows.Next() {
		m, err := scanModelMap(rows)
		if err != nil {
			return nil, err
		}
		maps = append(maps, *m)
	}
	return maps, rows.Err()
}

// UpsertByKey creates or updates a model mapping matched by local_model.
func (r *ModelMapRepo) UpsertByKey(mm ModelMap) (*ModelMap, error) {
	params, err := normalizeParams(mm.Params)
	if err != nil {
		return nil, err
	}
	modelType := mm.ModelType
	if modelType == "" {
		modelType = "chat"
	}
	modes, err := normalizeModes(mm.Modes, modelType)
	if err != nil {
		return nil, err
	}
	modelType = normalizeModelType(modelType, modes)
	modesJSON, _ := json.Marshal(modes)
	_, err = r.db.Exec(`
		INSERT INTO model_mappings (backend_id, remote_model, local_model, base_url, params, input_cost_per_million, output_cost_per_million, model_type, modes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(local_model) DO UPDATE SET
			backend_id               = excluded.backend_id,
			remote_model             = excluded.remote_model,
			local_model             = excluded.local_model,
			base_url                = excluded.base_url,
			params                  = excluded.params,
			input_cost_per_million  = excluded.input_cost_per_million,
			output_cost_per_million = excluded.output_cost_per_million,
			model_type              = excluded.model_type,
			modes                   = excluded.modes`,
		mm.BackendID, mm.RemoteModel, mm.LocalModel, mm.BaseURL,
		params, mm.InputCostPerMillion, mm.OutputCostPerMillion, modelType, string(modesJSON))
	if err != nil {
		return nil, err
	}
	row := r.db.QueryRow(`SELECT `+modelMapColumns+` FROM model_mappings WHERE local_model = ?`, mm.LocalModel)
	return scanModelMap(row)
}

// ListAllModels returns all unique local model names.
func (r *ModelMapRepo) ListAllModels() ([]string, error) {
	rows, err := r.db.Query(`SELECT DISTINCT local_model FROM model_mappings ORDER BY local_model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	models := make([]string, 0)
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		models = append(models, m)
	}
	return models, rows.Err()
}

// ListByType returns all model mappings for a given model_type.
func (r *ModelMapRepo) ListByType(modelType string) ([]ModelMap, error) {
	rows, err := r.db.Query(`SELECT `+modelMapColumns+` FROM model_mappings WHERE model_type = ? ORDER BY local_model`, modelType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	maps := make([]ModelMap, 0)
	for rows.Next() {
		m, err := scanModelMap(rows)
		if err != nil {
			return nil, err
		}
		maps = append(maps, *m)
	}
	return maps, rows.Err()
}

func (input ModelMapInput) modeInput() []ModelMode {
	if len(input.Modes) > 0 {
		return input.Modes
	}
	return input.Capabilities
}

func normalizeModes(modes []ModelMode, fallbackModelType string) ([]ModelMode, error) {
	if len(modes) == 0 {
		return []ModelMode{modeFromModelType(fallbackModelType)}, nil
	}

	seen := map[ModelMode]bool{}
	normalized := make([]ModelMode, 0, len(modes))
	for _, mode := range modes {
		if !validModelMode(mode) {
			return nil, fmt.Errorf("invalid model mode %q", mode)
		}
		if seen[mode] {
			continue
		}
		seen[mode] = true
		normalized = append(normalized, mode)
	}
	return normalized, nil
}

func normalizeParams(params string) (string, error) {
	params = strings.TrimSpace(params)
	if params == "" {
		return "{}", nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(params), &decoded); err != nil {
		return "", fmt.Errorf("params must be valid JSON object: %w", err)
	}
	normalized, err := json.Marshal(decoded)
	if err != nil {
		return "", err
	}
	return string(normalized), nil
}

func parseModes(data, fallbackModelType string) []ModelMode {
	var modes []ModelMode
	if err := json.Unmarshal([]byte(data), &modes); err != nil {
		return []ModelMode{modeFromModelType(fallbackModelType)}
	}
	normalized, err := normalizeModes(modes, fallbackModelType)
	if err != nil {
		return []ModelMode{modeFromModelType(fallbackModelType)}
	}
	return normalized
}

func normalizeModelType(modelType string, modes []ModelMode) string {
	if modelType != "" {
		return modelType
	}
	if len(modes) == 0 {
		return "chat"
	}
	return modelTypeFromMode(modes[0])
}

func modeFromModelType(modelType string) ModelMode {
	switch modelType {
	case "embedding", "embed":
		return ModelModeEmbed
	case "rerank":
		return ModelModeRerank
	default:
		return ModelModeChat
	}
}

func modelTypeFromMode(mode ModelMode) string {
	switch mode {
	case ModelModeEmbed:
		return "embedding"
	case ModelModeRerank:
		return "rerank"
	default:
		return "chat"
	}
}

func validModelMode(mode ModelMode) bool {
	switch mode {
	case ModelModeChat, ModelModeEmbed, ModelModeRerank:
		return true
	default:
		return false
	}
}
