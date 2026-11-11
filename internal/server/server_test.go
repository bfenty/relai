package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/bfenty/relai/internal/config"
	"github.com/bfenty/relai/internal/db"
)

func setupServerTestDB(t *testing.T) *db.DB {
	t.Helper()

	d, err := db.New(":memory:")
	if err != nil {
		t.Fatalf("failed to create test DB: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func setupTestServer(t *testing.T, d *db.DB) *Server {
	t.Helper()

	srv := New(&config.Config{AdminKey: "admin"}, d)
	srv.SetupRoutes()
	return srv
}

func setupTestServerWithConfig(t *testing.T, d *db.DB, cfg *config.Config) *Server {
	t.Helper()

	srv := New(cfg, d)
	srv.SetupRoutes()
	return srv
}

func TestReadyReportsStorageOnlyByDefault(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServer(t, d)

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var got readinessResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode readiness response: %v", err)
	}
	if !got.Ready {
		t.Fatalf("expected ready response, got %#v", got)
	}
	if !got.Checks["database"].OK {
		t.Fatalf("database check not ok: %#v", got.Checks["database"])
	}
	if !got.Checks["request_logs"].OK {
		t.Fatalf("request_logs check not ok: %#v", got.Checks["request_logs"])
	}
}

func TestReadyCanRequireActiveModelTargets(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServerWithConfig(t, d, &config.Config{AdminKey: "admin", ReadyRequireModels: true})

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	var got readinessResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("failed to decode readiness response: %v", err)
	}
	if got.Ready {
		t.Fatalf("expected not ready response, got %#v", got)
	}
	if got.Checks["model_targets"].OK {
		t.Fatalf("expected model target check to fail: %#v", got.Checks["model_targets"])
	}
}

func TestReadyStrictPassesWithActiveModelTarget(t *testing.T) {
	d := setupServerTestDB(t)
	backend, err := d.Backends.Create(db.BackendInput{Name: "ready-backend", BaseURL: "http://localhost:9999"})
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}
	if _, err := d.ModelMaps.Create(db.ModelMapInput{
		BackendID:   backend.ID,
		LocalModel:  "local-model",
		RemoteModel: "remote-model",
	}); err != nil {
		t.Fatalf("failed to create model mapping: %v", err)
	}
	srv := setupTestServerWithConfig(t, d, &config.Config{AdminKey: "admin", ReadyRequireModels: true})

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestV2RoutesRequireAuth(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServer(t, d)

	for _, path := range []string{"/v2/embed", "/v2/rerank"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, nil)
			rec := httptest.NewRecorder()

			srv.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestV2RoutesRequireModel(t *testing.T) {
	d := setupServerTestDB(t)
	_, plainKey, err := d.APIKeys.Create(db.APIKeyInput{Name: "test-key"})
	if err != nil {
		t.Fatalf("failed to create API key: %v", err)
	}
	srv := setupTestServer(t, d)

	for _, path := range []string{"/v2/embed", "/v2/rerank"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, body(`{"input":"hello"}`))
			req.Header.Set("Authorization", "Bearer "+plainKey)
			rec := httptest.NewRecorder()

			srv.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestProxyRoutesEnforceAPIKeyScopes(t *testing.T) {
	d := setupServerTestDB(t)
	_, plainKey, err := d.APIKeys.Create(db.APIKeyInput{
		Name:   "chat-only",
		Scopes: []string{"inference"},
	})
	if err != nil {
		t.Fatalf("failed to create API key: %v", err)
	}
	srv := setupTestServer(t, d)

	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/v1/embeddings", `{"model":"local-model","input":"hello"}`},
		{http.MethodPost, "/v2/rerank", `{"model":"local-model","query":"hello","documents":["hello"]}`},
		{http.MethodGet, "/v1/models", ``},
	} {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, body(tc.body))
			req.Header.Set("Authorization", "Bearer "+plainKey)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			srv.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusForbidden, rec.Body.String())
			}
		})
	}
}

func TestV2RoutesProxyToBackend(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		mode       db.ModelMode
		request    string
		wantFields map[string]any
	}{
		{
			name:    "embed",
			path:    "/v2/embed",
			mode:    db.ModelModeEmbed,
			request: `{"model":"local-model","input":["alpha","beta"],"truncate":"END"}`,
			wantFields: map[string]any{
				"input":    []any{"alpha", "beta"},
				"truncate": "END",
			},
		},
		{
			name:    "rerank",
			path:    "/v2/rerank",
			mode:    db.ModelModeRerank,
			request: `{"model":"local-model","query":"alpha","documents":["alpha doc","beta doc"],"top_n":1}`,
			wantFields: map[string]any{
				"query":     "alpha",
				"documents": []any{"alpha doc", "beta doc"},
				"top_n":     float64(1),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			var gotBody map[string]any
			backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("failed to read backend request body: %v", err)
				}
				if err := json.Unmarshal(data, &gotBody); err != nil {
					t.Errorf("backend request body is invalid JSON: %v", err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"object":"ok","usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
			}))
			t.Cleanup(backendServer.Close)

			d := setupServerTestDB(t)
			_, plainKey, err := d.APIKeys.Create(db.APIKeyInput{Name: "test-key"})
			if err != nil {
				t.Fatalf("failed to create API key: %v", err)
			}
			backend, err := d.Backends.Create(db.BackendInput{
				Name:    "test-backend",
				BaseURL: backendServer.URL,
			})
			if err != nil {
				t.Fatalf("failed to create backend: %v", err)
			}
			_, err = d.ModelMaps.Create(db.ModelMapInput{
				BackendID:   backend.ID,
				LocalModel:  "local-model",
				RemoteModel: "remote-model",
				Modes:       []db.ModelMode{tt.mode},
			})
			if err != nil {
				t.Fatalf("failed to create model mapping: %v", err)
			}

			srv := setupTestServer(t, d)
			req := httptest.NewRequest(http.MethodPost, tt.path, body(tt.request))
			req.Header.Set("Authorization", "Bearer "+plainKey)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			srv.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusOK, rec.Body.String())
			}
			if gotPath != tt.path {
				t.Fatalf("backend path = %q, want %q", gotPath, tt.path)
			}
			if gotBody["model"] != "remote-model" {
				t.Fatalf("backend model = %#v, want remote-model", gotBody["model"])
			}
			for key, want := range tt.wantFields {
				if !jsonValueEqual(gotBody[key], want) {
					t.Errorf("backend field %s = %#v, want %#v", key, gotBody[key], want)
				}
			}
		})
	}
}

func TestProxyTracksActiveConnections(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseBackend := make(chan struct{})

	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-releaseBackend
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"ok","usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	t.Cleanup(backendServer.Close)

	d := setupServerTestDB(t)
	_, plainKey, err := d.APIKeys.Create(db.APIKeyInput{Name: "test-key"})
	if err != nil {
		t.Fatalf("failed to create API key: %v", err)
	}
	backend, err := d.Backends.Create(db.BackendInput{
		Name:    "test-backend",
		BaseURL: backendServer.URL,
	})
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}
	_, err = d.ModelMaps.Create(db.ModelMapInput{
		BackendID:   backend.ID,
		LocalModel:  "local-model",
		RemoteModel: "remote-model",
	})
	if err != nil {
		t.Fatalf("failed to create model mapping: %v", err)
	}

	srv := setupTestServer(t, d)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body(`{"model":"local-model","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+plainKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		srv.Router().ServeHTTP(rec, req)
	}()

	<-requestStarted
	if got := srv.registry.ActiveConnections(backend.ID); got != 1 {
		close(releaseBackend)
		wg.Wait()
		t.Fatalf("active connections while request in flight = %d, want 1", got)
	}

	close(releaseBackend)
	wg.Wait()

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := srv.registry.ActiveConnections(backend.ID); got != 0 {
		t.Fatalf("active connections after request = %d, want 0", got)
	}
}

func TestProxyLogsUsageAndUpdatesAPIKey(t *testing.T) {
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"chat.completion","usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`))
	}))
	t.Cleanup(backendServer.Close)

	d := setupServerTestDB(t)
	apiKey, plainKey, err := d.APIKeys.Create(db.APIKeyInput{Name: "test-key"})
	if err != nil {
		t.Fatalf("failed to create API key: %v", err)
	}
	backend, err := d.Backends.Create(db.BackendInput{
		Name:    "test-backend",
		BaseURL: backendServer.URL,
	})
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}
	_, err = d.ModelMaps.Create(db.ModelMapInput{
		BackendID:            backend.ID,
		LocalModel:           "local-model",
		RemoteModel:          "remote-model",
		InputCostPerMillion:  2,
		OutputCostPerMillion: 4,
	})
	if err != nil {
		t.Fatalf("failed to create model mapping: %v", err)
	}

	srv := setupTestServer(t, d)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body(`{"model":"local-model","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+plainKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	logs, total, err := d.Logs.List(db.LogFilter{Limit: 10})
	if err != nil {
		t.Fatalf("failed to list logs: %v", err)
	}
	if total != 1 || len(logs) != 1 {
		t.Fatalf("logs total=%d len=%d, want 1", total, len(logs))
	}
	log := logs[0]
	if log.APIKeyID == nil || *log.APIKeyID != apiKey.ID {
		t.Fatalf("log API key id = %#v, want %d", log.APIKeyID, apiKey.ID)
	}
	if log.BackendID == nil || *log.BackendID != backend.ID {
		t.Fatalf("log backend id = %#v, want %d", log.BackendID, backend.ID)
	}
	if log.Model != "local-model" {
		t.Fatalf("log model = %q, want local-model", log.Model)
	}
	if log.StatusCode != http.StatusOK {
		t.Fatalf("log status = %d, want %d", log.StatusCode, http.StatusOK)
	}
	if log.RequestTokens != 11 || log.ResponseTokens != 7 || log.TotalTokens != 18 {
		t.Fatalf("logged tokens = %d/%d/%d, want 11/7/18", log.RequestTokens, log.ResponseTokens, log.TotalTokens)
	}
	if log.Cost <= 0 {
		t.Fatalf("log cost = %f, want > 0", log.Cost)
	}

	updatedKey, err := d.APIKeys.Get(apiKey.ID)
	if err != nil {
		t.Fatalf("failed to get API key: %v", err)
	}
	if updatedKey.TotalRequests != 1 {
		t.Fatalf("API key total requests = %d, want 1", updatedKey.TotalRequests)
	}
	if updatedKey.TotalCost != log.Cost {
		t.Fatalf("API key total cost = %f, want %f", updatedKey.TotalCost, log.Cost)
	}
}

func TestProxyLogsBackendErrorStatus(t *testing.T) {
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"message":"backend exploded"}}`))
	}))
	t.Cleanup(backendServer.Close)

	d := setupServerTestDB(t)
	_, plainKey, err := d.APIKeys.Create(db.APIKeyInput{Name: "test-key"})
	if err != nil {
		t.Fatalf("failed to create API key: %v", err)
	}
	backend, err := d.Backends.Create(db.BackendInput{
		Name:    "test-backend",
		BaseURL: backendServer.URL,
	})
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}
	_, err = d.ModelMaps.Create(db.ModelMapInput{
		BackendID:   backend.ID,
		LocalModel:  "local-model",
		RemoteModel: "remote-model",
	})
	if err != nil {
		t.Fatalf("failed to create model mapping: %v", err)
	}

	srv := setupTestServer(t, d)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body(`{"model":"local-model","messages":[]}`))
	req.Header.Set("Authorization", "Bearer "+plainKey)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}

	logs, total, err := d.Logs.List(db.LogFilter{Limit: 10, ErrorsOnly: true})
	if err != nil {
		t.Fatalf("failed to list logs: %v", err)
	}
	if total != 1 || len(logs) != 1 {
		t.Fatalf("error logs total=%d len=%d, want 1", total, len(logs))
	}
	if logs[0].StatusCode != http.StatusBadGateway {
		t.Fatalf("log status = %d, want %d", logs[0].StatusCode, http.StatusBadGateway)
	}
	if logs[0].ErrorMessage == "" {
		t.Fatal("expected error message to be logged")
	}
}

func TestFrontendServesEmbeddedAssets(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServer(t, d)

	entries, err := frontendFS.ReadDir("frontend/dist/assets")
	if err != nil {
		t.Fatalf("failed to read embedded assets: %v", err)
	}

	var assetPath string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".js") {
			assetPath = "/assets/" + entry.Name()
			break
		}
	}
	if assetPath == "" {
		t.Fatal("expected at least one embedded JS asset")
	}

	req := httptest.NewRequest(http.MethodGet, assetPath, nil)
	rec := httptest.NewRecorder()

	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if strings.Contains(rec.Body.String(), "<!DOCTYPE html>") {
		t.Fatalf("asset request returned index.html fallback")
	}
	if strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("asset content type = %q, want non-html", rec.Header().Get("Content-Type"))
	}
}

func body(s string) io.Reader {
	return strings.NewReader(s)
}

func jsonValueEqual(got, want any) bool {
	gotJSON, err := json.Marshal(got)
	if err != nil {
		return false
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		return false
	}
	return string(gotJSON) == string(wantJSON)
}
