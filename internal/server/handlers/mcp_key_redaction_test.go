package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/bfenty/relai/internal/db"
	"github.com/go-chi/chi/v5"
)

// #15: MCP server responses leaked the stored api_key in plaintext, and a PUT
// that omitted api_key (the GUI sends one whenever the user edits without
// re-entering the key) overwrote the stored key with an empty string.
// Backends already redact via redactBackendKey; MCP servers must behave the same.

// mcpIDRequest builds a request with chi's {id} URL param populated, the way
// the mounted router would.
func mcpIDRequest(method, path string, id int64, body string) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.FormatInt(id, 10))
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestMCPServerResponsesRedactAPIKey(t *testing.T) {
	d, err := db.New(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	h := NewAdminHandler(d, nil, nil)

	// POST create with a key.
	body := `{"name":"rag","url":"http://127.0.0.1:1/mcp","transport":"http","api_key":"sk-secret-123","is_enabled":true}`
	rec := httptest.NewRecorder()
	h.HandleMCPServers(rec, httptest.NewRequest(http.MethodPost, "/api/mcp-servers", strings.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sk-secret-123") {
		t.Fatalf("POST response leaked api_key: %s", rec.Body.String())
	}
	var created db.MCPServer
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("parse create response: %v", err)
	}
	if !created.HasAPIKey {
		t.Fatalf("create response should report has_api_key=true")
	}

	// The stored key must still be usable (gateway reads it via repo.Get).
	got, err := d.MCPServers.Get(created.ID)
	if err != nil || got.APIKey != "sk-secret-123" {
		t.Fatalf("stored key damaged: %v %q", err, got.APIKey)
	}

	// GET list and GET one must not leak either.
	rec = httptest.NewRecorder()
	h.HandleMCPServers(rec, httptest.NewRequest(http.MethodGet, "/api/mcp-servers", nil))
	if strings.Contains(rec.Body.String(), "sk-secret-123") {
		t.Fatalf("GET list leaked api_key: %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.HandleMCPServer(rec, mcpIDRequest(http.MethodGet, "/api/mcp-servers/"+strconv.FormatInt(created.ID, 10), created.ID, ""))
	if strings.Contains(rec.Body.String(), "sk-secret-123") {
		t.Fatalf("GET one leaked api_key: %s", rec.Body.String())
	}
}

func TestMCPServerUpdateWithoutKeyPreservesStored(t *testing.T) {
	d, err := db.New(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	h := NewAdminHandler(d, nil, nil)
	body := `{"name":"rag","url":"http://127.0.0.1:1/mcp","transport":"http","api_key":"sk-secret-123","is_enabled":true}`
	rec := httptest.NewRecorder()
	h.HandleMCPServers(rec, httptest.NewRequest(http.MethodPost, "/api/mcp-servers", strings.NewReader(body)))
	var created db.MCPServer
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("parse create response: %v", err)
	}

	// PUT with no api_key field at all — exactly what the GUI sends on a metadata edit.
	rec = httptest.NewRecorder()
	upd := `{"name":"rag-renamed","url":"http://127.0.0.1:1/mcp","transport":"http","is_enabled":true}`
	h.HandleMCPServer(rec, mcpIDRequest(http.MethodPut, "/api/mcp-servers/"+strconv.FormatInt(created.ID, 10), created.ID, upd))
	if rec.Code != http.StatusOK {
		t.Fatalf("update: got %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sk-secret-123") {
		t.Fatalf("PUT response leaked api_key: %s", rec.Body.String())
	}

	got, err := d.MCPServers.Get(created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.APIKey != "sk-secret-123" {
		t.Fatalf("update wiped the stored key: %q", got.APIKey)
	}
	if got.Name != "rag-renamed" {
		t.Fatalf("update did not apply other fields: %q", got.Name)
	}
}
