package db

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func setupTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := New(":memory:")
	if err != nil {
		t.Fatalf("failed to create test DB: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestBackendCRUD(t *testing.T) {
	d := setupTestDB(t)

	// Create
	backend, err := d.Backends.Create(BackendInput{
		Name:    "test-backend",
		BaseURL: "http://localhost:8080",
		APIKey:  "test-key",
		Weight:  2,
	})
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}
	if backend.Name != "test-backend" {
		t.Errorf("expected name test-backend, got %s", backend.Name)
	}
	if backend.Weight != 2 {
		t.Errorf("expected weight 2, got %d", backend.Weight)
	}

	// Read
	got, err := d.Backends.Get(backend.ID)
	if err != nil {
		t.Fatalf("failed to get backend: %v", err)
	}
	if got.Name != "test-backend" {
		t.Errorf("expected name test-backend, got %s", got.Name)
	}

	// Update
	_, err = d.Backends.Update(backend.ID, BackendInput{
		Name:    "updated-backend",
		BaseURL: "http://localhost:8081",
		Weight:  5,
	})
	if err != nil {
		t.Fatalf("failed to update backend: %v", err)
	}

	got, err = d.Backends.Get(backend.ID)
	if err != nil {
		t.Fatalf("failed to get updated backend: %v", err)
	}
	if got.Name != "updated-backend" {
		t.Errorf("expected name updated-backend, got %s", got.Name)
	}

	// List
	backends, err := d.Backends.List()
	if err != nil {
		t.Fatalf("failed to list backends: %v", err)
	}
	if len(backends) != 1 {
		t.Errorf("expected 1 backend, got %d", len(backends))
	}

	// Delete
	err = d.Backends.Delete(backend.ID)
	if err != nil {
		t.Fatalf("failed to delete backend: %v", err)
	}

	backends, err = d.Backends.List()
	if err != nil {
		t.Fatalf("failed to list backends after delete: %v", err)
	}
	if len(backends) != 0 {
		t.Errorf("expected 0 backends after delete, got %d", len(backends))
	}
}

func TestAPIKeyCRUD(t *testing.T) {
	d := setupTestDB(t)

	// Create
	key, plainKey, err := d.APIKeys.Create(APIKeyInput{
		Name:         "test-key",
		RateLimitRPM: 100,
	})
	if err != nil {
		t.Fatalf("failed to create API key: %v", err)
	}
	if key.Name != "test-key" {
		t.Errorf("expected name test-key, got %s", key.Name)
	}
	if key.RateLimitRPM != 100 {
		t.Errorf("expected rate limit 100, got %d", key.RateLimitRPM)
	}
	if plainKey[:5] != "rlai_" {
		t.Errorf("expected key prefix rlai_, got %s", plainKey[:5])
	}

	// Validate
	validated, err := d.APIKeys.Validate(plainKey)
	if err != nil {
		t.Fatalf("failed to validate API key: %v", err)
	}
	if validated.ID != key.ID {
		t.Errorf("expected key ID %d, got %d", key.ID, validated.ID)
	}

	// Invalid key should fail
	_, err = d.APIKeys.Validate("rlai_invalid")
	if err == nil {
		t.Error("expected error for invalid key, got nil")
	}

	// Rate limit
	allowed, err := d.APIKeys.CheckRateLimit(key.ID)
	if err != nil {
		t.Fatalf("failed to check rate limit: %v", err)
	}
	if !allowed {
		t.Error("expected rate limit to be allowed")
	}

	// Update usage
	err = d.APIKeys.UpdateUsage(key.ID, 0.01)
	if err != nil {
		t.Fatalf("failed to update usage: %v", err)
	}

	got, err := d.APIKeys.Get(key.ID)
	if err != nil {
		t.Fatalf("failed to get API key: %v", err)
	}
	if got.TotalCost != 0.01 {
		t.Errorf("expected total cost 0.01, got %f", got.TotalCost)
	}
	if got.TotalRequests != 1 {
		t.Errorf("expected 1 request, got %d", got.TotalRequests)
	}
}

func TestAPIKeyScopesAndExpiration(t *testing.T) {
	d := setupTestDB(t)

	key, plainKey, err := d.APIKeys.Create(APIKeyInput{
		Name:      "scoped-key",
		Owner:     "alice",
		Scopes:    []string{"inference"},
		ExpiresAt: ptrTime(time.Now().Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("failed to create API key: %v", err)
	}
	if key.Owner != "alice" {
		t.Errorf("expected owner alice, got %q", key.Owner)
	}
	if !key.HasScope("inference") {
		t.Error("expected inference scope")
	}
	if key.HasScope("rerank") {
		t.Error("did not expect rerank scope")
	}

	validated, err := d.APIKeys.Validate(plainKey)
	if err != nil {
		t.Fatalf("expected unexpired key to validate: %v", err)
	}
	if validated.ExpiresAt == nil {
		t.Fatal("expected expires_at to round trip")
	}

	_, expiredPlainKey, err := d.APIKeys.Create(APIKeyInput{
		Name:      "expired-key",
		ExpiresAt: ptrTime(time.Now().Add(-time.Hour)),
	})
	if err != nil {
		t.Fatalf("failed to create expired key: %v", err)
	}
	if _, err := d.APIKeys.Validate(expiredPlainKey); err == nil {
		t.Fatal("expected expired key to be rejected")
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func TestRoutingRuleCRUD(t *testing.T) {
	d := setupTestDB(t)

	// Create
	rule, err := d.Routing.Create(RoutingRuleInput{
		Name:     "test-rule",
		Strategy: "model_based",
		Priority: 1,
		Conditions: map[string]interface{}{
			"models": []interface{}{"gpt-4", "claude-*"},
		},
	})
	if err != nil {
		t.Fatalf("failed to create routing rule: %v", err)
	}
	if rule.Name != "test-rule" {
		t.Errorf("expected name test-rule, got %s", rule.Name)
	}
	if rule.Strategy != "model_based" {
		t.Errorf("expected strategy model_based, got %s", rule.Strategy)
	}

	// List
	rules, err := d.Routing.List()
	if err != nil {
		t.Fatalf("failed to list routing rules: %v", err)
	}
	if len(rules) != 1 {
		t.Errorf("expected 1 rule, got %d", len(rules))
	}

	// Delete
	err = d.Routing.Delete(rule.ID)
	if err != nil {
		t.Fatalf("failed to delete routing rule: %v", err)
	}

	rules, err = d.Routing.List()
	if err != nil {
		t.Fatalf("failed to list routing rules after delete: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("expected 0 rules after delete, got %d", len(rules))
	}
}

func TestModelMapCRUD(t *testing.T) {
	d := setupTestDB(t)

	// Create backend
	backend, err := d.Backends.Create(BackendInput{
		Name:    "test-backend",
		BaseURL: "http://localhost:8080",
	})
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}

	// Create model mapping
	mapping, err := d.ModelMaps.Create(ModelMapInput{
		BackendID:   backend.ID,
		RemoteModel: "gpt-4-0613",
		LocalModel:  "gpt-4",
	})
	if err != nil {
		t.Fatalf("failed to create model mapping: %v", err)
	}
	if mapping.LocalModel != "gpt-4" {
		t.Errorf("expected local model gpt-4, got %s", mapping.LocalModel)
	}
	if len(mapping.Modes) != 1 || mapping.Modes[0] != ModelModeChat {
		t.Errorf("expected default mode chat, got %#v", mapping.Modes)
	}

	// Resolve model
	mappings, err := d.ModelMaps.ResolveModel("gpt-4")
	if err != nil {
		t.Fatalf("failed to resolve model: %v", err)
	}
	if len(mappings) != 1 {
		t.Errorf("expected 1 mapping, got %d", len(mappings))
	}
	if mappings[0].RemoteModel != "gpt-4-0613" {
		t.Errorf("expected remote model gpt-4-0613, got %s", mappings[0].RemoteModel)
	}
}

func TestModelMapAllowsSameRemoteModelWithDifferentLocalNames(t *testing.T) {
	d := setupTestDB(t)

	backend, err := d.Backends.Create(BackendInput{
		Name:    "qwen-backend",
		BaseURL: "http://localhost:8080",
	})
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}

	withoutThinking, err := d.ModelMaps.Create(ModelMapInput{
		BackendID:   backend.ID,
		RemoteModel: "qwen3.6",
		LocalModel:  "qwen3.6",
		Params:      `{"chat_template_kwargs":{"enable_thinking":false}}`,
	})
	if err != nil {
		t.Fatalf("failed to create non-thinking mapping: %v", err)
	}
	withThinking, err := d.ModelMaps.Create(ModelMapInput{
		BackendID:   backend.ID,
		RemoteModel: "qwen3.6",
		LocalModel:  "qwen3.6-thinking",
		Params:      `{"chat_template_kwargs":{"enable_thinking":true}}`,
	})
	if err != nil {
		t.Fatalf("failed to create thinking mapping: %v", err)
	}
	if withoutThinking.ID == withThinking.ID {
		t.Fatalf("expected distinct mappings, got %d", withoutThinking.ID)
	}

	mappings, err := d.ModelMaps.List()
	if err != nil {
		t.Fatalf("failed to list mappings: %v", err)
	}
	if len(mappings) != 2 {
		t.Fatalf("expected 2 mappings, got %d", len(mappings))
	}

	if _, err := d.ModelMaps.Create(ModelMapInput{
		BackendID:   backend.ID,
		RemoteModel: "qwen3.6-other",
		LocalModel:  "qwen3.6-thinking",
	}); err == nil {
		t.Fatal("expected duplicate local model name to fail")
	}
}

func TestMigrateModelMappingsDropsStaleRebuildTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relai.db")
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	_, err = conn.Exec(`
		CREATE TABLE backends (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			base_url TEXT NOT NULL,
			api_key TEXT DEFAULT '',
			weight INTEGER DEFAULT 1,
			status TEXT DEFAULT 'active',
			last_latency_ms INTEGER DEFAULT 0,
			last_health_check TIMESTAMP,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO backends (id, name, base_url) VALUES
			(1, 'one', 'http://one'),
			(2, 'two', 'http://two');
		CREATE TABLE model_mappings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			backend_id INTEGER NOT NULL REFERENCES backends(id) ON DELETE CASCADE,
			remote_model TEXT NOT NULL,
			local_model TEXT NOT NULL,
			UNIQUE(backend_id, remote_model)
		);
		INSERT INTO model_mappings (id, backend_id, remote_model, local_model) VALUES
			(1, 1, 'remote-one', 'shared'),
			(2, 2, 'remote-two', 'shared');
		CREATE TABLE model_mappings_new (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			backend_id INTEGER NOT NULL,
			remote_model TEXT NOT NULL,
			local_model TEXT NOT NULL,
			UNIQUE(local_model)
		);
	`)
	if err != nil {
		conn.Close()
		t.Fatalf("seed legacy schema: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	d, err := New(path)
	if err != nil {
		t.Fatalf("migrate legacy db: %v", err)
	}
	defer d.Close()

	mappings, err := d.ModelMaps.ResolveModel("shared")
	if err != nil {
		t.Fatalf("resolve original model: %v", err)
	}
	if len(mappings) != 1 {
		t.Fatalf("expected one mapping to keep original local name, got %d", len(mappings))
	}
	renamed, err := d.ModelMaps.ResolveModel("shared-2")
	if err != nil {
		t.Fatalf("resolve renamed duplicate: %v", err)
	}
	if len(renamed) != 1 {
		t.Fatalf("expected one duplicate to be renamed, got %d", len(renamed))
	}

	// The old database had no provider/origin columns. The migration must add
	// metadata without changing its backend IDs or its local routing behavior.
	legacyBackend, err := d.Backends.Get(1)
	if err != nil {
		t.Fatalf("load migrated legacy backend: %v", err)
	}
	if legacyBackend.Provider != BackendProviderOpenAICompatible || legacyBackend.Origin != BackendOriginLocal {
		t.Fatalf("legacy backend classification = %q/%q, want openai-compatible/local", legacyBackend.Provider, legacyBackend.Origin)
	}
}

func TestModelMapParamsMustBeJSONObject(t *testing.T) {
	d := setupTestDB(t)

	backend, err := d.Backends.Create(BackendInput{
		Name:    "test-backend",
		BaseURL: "http://localhost:8080",
	})
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}

	if _, err := d.ModelMaps.Create(ModelMapInput{
		BackendID:   backend.ID,
		RemoteModel: "qwen3.6",
		LocalModel:  "qwen3.6-bad",
		Params:      `["not", "an", "object"]`,
	}); err == nil {
		t.Fatal("expected non-object params to fail")
	}
}

func TestModelMapModes(t *testing.T) {
	d := setupTestDB(t)

	backend, err := d.Backends.Create(BackendInput{
		Name:    "test-backend",
		BaseURL: "http://localhost:8080",
	})
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}

	mapping, err := d.ModelMaps.Create(ModelMapInput{
		BackendID:   backend.ID,
		RemoteModel: "multi-remote",
		LocalModel:  "multi-local",
		Modes:       []ModelMode{ModelModeChat, ModelModeEmbed, ModelModeEmbed, ModelModeRerank},
	})
	if err != nil {
		t.Fatalf("failed to create model mapping: %v", err)
	}
	if got, want := mapping.Modes, []ModelMode{ModelModeChat, ModelModeEmbed, ModelModeRerank}; !sameModes(got, want) {
		t.Fatalf("modes = %#v, want %#v", got, want)
	}

	_, err = d.ModelMaps.Create(ModelMapInput{
		BackendID:   backend.ID,
		RemoteModel: "bad-remote",
		LocalModel:  "bad-local",
		Modes:       []ModelMode{"invalid"},
	})
	if err == nil {
		t.Fatal("expected invalid mode to fail")
	}
}

func sameModes(got, want []ModelMode) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
