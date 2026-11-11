package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bfenty/relai/internal/db"
)

func setupGatewayTest(t *testing.T, upstream http.HandlerFunc) (*Gateway, *db.MCPServer) {
	t.Helper()

	upstreamServer := httptest.NewServer(upstream)
	t.Cleanup(upstreamServer.Close)

	d, err := db.New(":memory:")
	if err != nil {
		t.Fatalf("create db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	srv, err := d.MCPServers.Create(&db.MCPServer{
		Name:      "alpha",
		URL:       upstreamServer.URL,
		Transport: "http",
		IsEnabled: true,
	})
	if err != nil {
		t.Fatalf("create MCP server: %v", err)
	}

	return NewGateway(d.MCPServers), srv
}

func postRPC(t *testing.T, gateway *Gateway, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", ProtocolVersion)
	rec := httptest.NewRecorder()
	gateway.ServeHTTP(rec, req)
	return rec
}

func TestGatewayInitialize(t *testing.T) {
	gateway, _ := setupGatewayTest(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("initialize should not contact upstream servers")
	})

	rec := postRPC(t, gateway, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}

	var got map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	result := got["result"].(map[string]any)
	if result["protocolVersion"] != ProtocolVersion {
		t.Fatalf("protocolVersion = %v", result["protocolVersion"])
	}
	caps := result["capabilities"].(map[string]any)
	for _, key := range []string{"tools", "prompts", "resources"} {
		if _, ok := caps[key]; !ok {
			t.Fatalf("missing capability %q in %#v", key, caps)
		}
	}
}

func TestGatewayListsAndCallsPrefixedTools(t *testing.T) {
	var sawSessionOnCall bool
	gateway, srv := setupGatewayTest(t, func(w http.ResponseWriter, r *http.Request) {
		var msg rpcMessage
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}

		if msg.Method != "initialize" && r.Header.Get("MCP-Session-Id") != "session-alpha" {
			t.Fatalf("missing MCP session on %s", msg.Method)
		}

		switch msg.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("MCP-Session-Id", "session-alpha")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"init","result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"alpha","version":"1"}}}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"list","result":{"tools":[{"name":"search","description":"Search things","inputSchema":{"type":"object"}}]}}`))
		case "tools/call":
			sawSessionOnCall = true
			var params callParams
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				t.Fatalf("decode call params: %v", err)
			}
			if params.Name != "search" {
				t.Fatalf("upstream tool name = %q", params.Name)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"call","result":{"content":[{"type":"text","text":"ok"}],"isError":false}}`))
		default:
			t.Fatalf("unexpected upstream method %q", msg.Method)
		}
	})

	rec := postRPC(t, gateway, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body: %s", rec.Code, rec.Body.String())
	}
	expectedName := serverPrefix(*srv) + "__search"
	if !strings.Contains(rec.Body.String(), expectedName) {
		t.Fatalf("list response missing %q: %s", expectedName, rec.Body.String())
	}

	rec = postRPC(t, gateway, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"`+expectedName+`","arguments":{"q":"relai"}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("call status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !sawSessionOnCall {
		t.Fatalf("expected call to reach upstream with session")
	}
	if !strings.Contains(rec.Body.String(), `"text":"ok"`) {
		t.Fatalf("call response = %s", rec.Body.String())
	}
}

func TestGatewayRoutesTaskOperationsToOriginServer(t *testing.T) {
	gateway, srv := setupGatewayTest(t, func(w http.ResponseWriter, r *http.Request) {
		var msg rpcMessage
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch msg.Method {
		case "initialize":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"init","result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{},"tasks":{"requests":{"tools":{"call":{}}}}},"serverInfo":{"name":"alpha","version":"1"}}}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"call","result":{"task":{"taskId":"upstream-task","status":"working","createdAt":"2026-06-04T00:00:00Z","lastUpdatedAt":"2026-06-04T00:00:00Z","ttl":60000}}}`))
		case "tasks/get":
			if !strings.Contains(string(msg.Params), `"taskId":"upstream-task"`) {
				t.Fatalf("task params = %s", msg.Params)
			}
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"get","result":{"taskId":"upstream-task","status":"completed","createdAt":"2026-06-04T00:00:00Z","lastUpdatedAt":"2026-06-04T00:00:01Z","ttl":60000}}`))
		default:
			t.Fatalf("unexpected upstream method %q", msg.Method)
		}
	})

	name := serverPrefix(*srv) + "__generate"
	rec := postRPC(t, gateway, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":{},"task":{"ttl":60000}}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("call status = %d, body: %s", rec.Code, rec.Body.String())
	}
	wrapped := wrappedTaskID(*srv, "upstream-task")
	if !strings.Contains(rec.Body.String(), wrapped) {
		t.Fatalf("call response missing wrapped task ID: %s", rec.Body.String())
	}

	rec = postRPC(t, gateway, `{"jsonrpc":"2.0","id":2,"method":"tasks/get","params":{"taskId":"`+wrapped+`"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), wrapped) {
		t.Fatalf("get response missing wrapped task ID: %s", rec.Body.String())
	}
}

func TestGatewayProxiesArtifacts(t *testing.T) {
	gateway, srv := setupGatewayTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/files/5" {
			t.Fatalf("upstream path = %q", r.URL.Path)
		}
		if r.URL.RawQuery != "filename=image.png&type=output" {
			t.Fatalf("upstream query = %q", r.URL.RawQuery)
		}
		if r.Header.Get("Authorization") != "Bearer upstream-secret" {
			t.Fatalf("missing upstream auth header")
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png"))
	})
	srv.APIKey = "upstream-secret"
	if _, err := gateway.repo.Update(srv.ID, srv); err != nil {
		t.Fatalf("update MCP server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/mcp/artifacts/alpha/api/files/5?filename=image.png&type=output", nil)
	rec := httptest.NewRecorder()
	gateway.ServeArtifact(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("content-type = %q", rec.Header().Get("Content-Type"))
	}
	if rec.Body.String() != "png" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestGatewayWrapsResourcesForReadRouting(t *testing.T) {
	const upstreamURI = "file:///project/README.md"
	gateway, srv := setupGatewayTest(t, func(w http.ResponseWriter, r *http.Request) {
		var msg rpcMessage
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}

		switch msg.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"init","result":{"protocolVersion":"2025-11-25","capabilities":{"resources":{}},"serverInfo":{"name":"alpha","version":"1"}}}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "resources/list":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"list","result":{"resources":[{"uri":"` + upstreamURI + `","name":"README.md","mimeType":"text/markdown"}]}}`))
		case "resources/read":
			var params readParams
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				t.Fatalf("decode read params: %v", err)
			}
			if params.URI != upstreamURI {
				t.Fatalf("upstream resource uri = %q", params.URI)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"read","result":{"contents":[{"uri":"` + upstreamURI + `","mimeType":"text/markdown","text":"hello"}]}}`))
		default:
			t.Fatalf("unexpected upstream method %q", msg.Method)
		}
	})

	rec := postRPC(t, gateway, `{"jsonrpc":"2.0","id":4,"method":"resources/list","params":{}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body: %s", rec.Code, rec.Body.String())
	}
	wrapped := wrappedResourceURI(*srv, upstreamURI)
	if !strings.Contains(rec.Body.String(), wrapped) {
		t.Fatalf("list response missing wrapped URI %q: %s", wrapped, rec.Body.String())
	}

	rec = postRPC(t, gateway, `{"jsonrpc":"2.0","id":5,"method":"resources/read","params":{"uri":"`+wrapped+`"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("read status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"text":"hello"`) || !strings.Contains(rec.Body.String(), wrapped) {
		t.Fatalf("read response = %s", rec.Body.String())
	}
}
