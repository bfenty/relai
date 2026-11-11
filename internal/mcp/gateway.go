package mcp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bfenty/relai/internal/db"
)

const (
	ProtocolVersion = "2025-11-25"
	serverName      = "relai-mcp-gateway"
	serverVersion   = "0.1.0"
)

var supportedProtocolVersions = []string{"2025-03-26", "2025-06-18", "2025-11-25"}

type Gateway struct {
	repo   *db.MCPServerRepo
	client *http.Client

	mu       sync.Mutex
	sessions map[int64]*upstreamSession
}

type upstreamSession struct {
	protocolVersion string
	sessionID       string
	initializedAt   time.Time
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type listResponse struct {
	Items      []map[string]any
	NextCursor string
}

type targetRef struct {
	server db.MCPServer
	name   string
}

type upstreamResponse struct {
	statusCode int
	header     http.Header
	message    *rpcMessage
}

type callParams struct {
	Name string `json:"name"`
}

type readParams struct {
	URI string `json:"uri"`
}

type getPromptParams struct {
	Name string `json:"name"`
}

var slugRE = regexp.MustCompile(`[^A-Za-z0-9_]+`)

func NewGateway(repo *db.MCPServerRepo) *Gateway {
	return &Gateway{
		repo: repo,
		client: &http.Client{
			Timeout: 2 * time.Minute,
		},
		sessions: make(map[int64]*upstreamSession),
	}
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !g.validOrigin(r) {
		writeHTTPRPCError(w, nil, http.StatusForbidden, -32000, "invalid Origin header", nil)
		return
	}

	switch r.Method {
	case http.MethodOptions:
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		w.Header().Set("Allow", "POST, OPTIONS")
		writeHTTPRPCError(w, nil, http.StatusMethodNotAllowed, -32601, "GET streams are not supported by this MCP gateway", nil)
	case http.MethodDelete:
		w.Header().Set("Allow", "POST, OPTIONS")
		w.WriteHeader(http.StatusMethodNotAllowed)
	case http.MethodPost:
		g.handlePost(w, r)
	default:
		w.Header().Set("Allow", "POST, OPTIONS")
		writeHTTPRPCError(w, nil, http.StatusMethodNotAllowed, -32601, "method not allowed", nil)
	}
}

// ServeArtifact proxies an HTTP artifact path from an enabled upstream MCP server.
// Route shape: /mcp/artifacts/{server_name_or_prefix}/{upstream_path...}
func (g *Gateway) ServeArtifact(w http.ResponseWriter, r *http.Request) {
	server, upstreamPath, err := g.artifactTarget(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(upstreamPath, "/api/") {
		http.Error(w, "artifact path must start with /api/", http.StatusBadRequest)
		return
	}

	upstreamURL, err := url.Parse(server.URL)
	if err != nil {
		http.Error(w, "invalid upstream MCP server URL", http.StatusBadGateway)
		return
	}
	upstreamURL.Path = path.Join(path.Dir(upstreamURL.Path), upstreamPath)
	if strings.HasSuffix(upstreamPath, "/") && !strings.HasSuffix(upstreamURL.Path, "/") {
		upstreamURL.Path += "/"
	}
	upstreamURL.RawQuery = r.URL.RawQuery

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstreamURL.String(), nil)
	if err != nil {
		http.Error(w, "failed to create upstream request", http.StatusBadGateway)
		return
	}
	if server.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+server.APIKey)
	}
	req.Header.Set("User-Agent", "relai-mcp-gateway/0.1")

	resp, err := g.client.Do(req)
	if err != nil {
		http.Error(w, "failed to fetch artifact", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for _, header := range []string{"Content-Type", "Content-Length", "Cache-Control"} {
		if value := resp.Header.Get(header); value != "" {
			w.Header().Set(header, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, resp.Body)
}

func (g *Gateway) artifactTarget(rawPath string) (db.MCPServer, string, error) {
	rest := strings.TrimPrefix(rawPath, "/mcp/artifacts/")
	if rest == rawPath || rest == "" {
		return db.MCPServer{}, "", fmt.Errorf("artifact server is required")
	}
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
		return db.MCPServer{}, "", fmt.Errorf("artifact path is required")
	}
	serverName, err := url.PathUnescape(parts[0])
	if err != nil {
		return db.MCPServer{}, "", fmt.Errorf("invalid artifact server")
	}
	upstreamPath, err := url.PathUnescape("/" + parts[1])
	if err != nil {
		return db.MCPServer{}, "", fmt.Errorf("invalid artifact path")
	}
	servers, err := g.enabledHTTPServers()
	if err != nil {
		return db.MCPServer{}, "", err
	}
	for _, server := range servers {
		if server.Name == serverName || serverPrefix(server) == serverName {
			return server, upstreamPath, nil
		}
	}
	return db.MCPServer{}, "", fmt.Errorf("unknown MCP artifact server %q", serverName)
}

func (g *Gateway) handlePost(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<20))
	if err != nil {
		writeHTTPRPCError(w, nil, http.StatusBadRequest, -32700, "failed to read request body", nil)
		return
	}

	var msg rpcMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		writeHTTPRPCError(w, nil, http.StatusBadRequest, -32700, "parse error", nil)
		return
	}
	if msg.Method == "" {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if len(msg.ID) == 0 {
		g.handleNotification(w, &msg)
		return
	}

	id := decodeID(msg.ID)
	switch msg.Method {
	case "initialize":
		writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: id, Result: g.initializeResult(msg.Params)})
	case "ping":
		writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: id, Result: map[string]any{}})
	case "tools/list":
		g.handleList(w, r, id, "tools/list", "tools", transformName)
	case "tools/call":
		g.handleNamedForward(w, r, id, &msg, "tools/call", "name")
	case "tasks/get", "tasks/result", "tasks/cancel":
		g.handleTaskForward(w, r, id, &msg)
	case "prompts/list":
		g.handleList(w, r, id, "prompts/list", "prompts", transformName)
	case "prompts/get":
		g.handleNamedForward(w, r, id, &msg, "prompts/get", "name")
	case "resources/list":
		g.handleList(w, r, id, "resources/list", "resources", transformResourceURI)
	case "resources/templates/list":
		g.handleList(w, r, id, "resources/templates/list", "resourceTemplates", transformResourceTemplate)
	case "resources/read":
		g.handleResourceRead(w, r, id, &msg)
	default:
		writeJSON(w, http.StatusNotFound, rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32601, Message: "method not found"}})
	}
}

func (g *Gateway) handleNotification(w http.ResponseWriter, msg *rpcMessage) {
	switch msg.Method {
	case "notifications/initialized", "notifications/cancelled":
		w.WriteHeader(http.StatusAccepted)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}

func (g *Gateway) initializeResult(params json.RawMessage) map[string]any {
	version := ProtocolVersion
	var parsed struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(params) > 0 && json.Unmarshal(params, &parsed) == nil && parsed.ProtocolVersion != "" {
		if contains(supportedProtocolVersions, parsed.ProtocolVersion) {
			version = parsed.ProtocolVersion
		}
	}
	return map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{
			"tools":     map[string]any{"listChanged": false},
			"prompts":   map[string]any{"listChanged": false},
			"resources": map[string]any{"listChanged": false},
			"tasks": map[string]any{
				"requests": map[string]any{
					"tools": map[string]any{"call": map[string]any{}},
				},
			},
		},
		"serverInfo": map[string]any{
			"name":        serverName,
			"title":       "Relai MCP Gateway",
			"version":     serverVersion,
			"description": "Aggregates enabled MCP servers configured in Relai.",
		},
	}
}

func (g *Gateway) handleList(w http.ResponseWriter, r *http.Request, id any, method, resultKey string, transform func(db.MCPServer, map[string]any) map[string]any) {
	servers, err := g.enabledHTTPServers()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, rpcResponse{JSONRPC: "2.0", ID: id, Error: internalError(err)})
		return
	}

	items := make([]map[string]any, 0)
	for _, srv := range servers {
		result, err := g.listFromUpstream(r.Context(), srv, method, resultKey)
		if err != nil {
			continue
		}
		for _, item := range result.Items {
			items = append(items, transform(srv, item))
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		return fmt.Sprint(items[i]["name"], items[i]["uri"], items[i]["uriTemplate"]) < fmt.Sprint(items[j]["name"], items[j]["uri"], items[j]["uriTemplate"])
	})

	writeJSON(w, http.StatusOK, rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  map[string]any{resultKey: items},
	})
}

func (g *Gateway) handleNamedForward(w http.ResponseWriter, r *http.Request, id any, msg *rpcMessage, method, field string) {
	var params map[string]any
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		writeJSON(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", ID: id, Error: invalidParams("invalid params")})
		return
	}
	rawName, _ := params[field].(string)
	target, err := g.resolveName(rawName)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", ID: id, Error: invalidParams(err.Error())})
		return
	}
	params[field] = target.name
	forward := *msg
	forward.Params = mustMarshalRaw(params)

	resp, err := g.send(r.Context(), target.server, &forward)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, rpcResponse{JSONRPC: "2.0", ID: id, Error: upstreamErr(target.server.Name, err)})
		return
	}
	if method == "tools/call" {
		g.rewriteTaskResponse(resp, target.server)
	}
	g.writeForwardedResponse(w, id, resp)
}

func (g *Gateway) handleTaskForward(w http.ResponseWriter, r *http.Request, id any, msg *rpcMessage) {
	var params map[string]any
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		writeJSON(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", ID: id, Error: invalidParams("invalid params")})
		return
	}
	rawTaskID, _ := params["taskId"].(string)
	srv, taskID, err := g.unwrapTaskID(rawTaskID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", ID: id, Error: invalidParams(err.Error())})
		return
	}
	params["taskId"] = taskID
	forward := *msg
	forward.Params = mustMarshalRaw(params)

	resp, err := g.send(r.Context(), srv, &forward)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, rpcResponse{JSONRPC: "2.0", ID: id, Error: upstreamErr(srv.Name, err)})
		return
	}
	g.rewriteTaskResponse(resp, srv)
	g.writeForwardedResponse(w, id, resp)
}

func (g *Gateway) handleResourceRead(w http.ResponseWriter, r *http.Request, id any, msg *rpcMessage) {
	var params map[string]any
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		writeJSON(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", ID: id, Error: invalidParams("invalid params")})
		return
	}
	rawURI, _ := params["uri"].(string)
	srv, uri, err := g.unwrapResourceURI(rawURI)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", ID: id, Error: invalidParams(err.Error())})
		return
	}
	params["uri"] = uri
	forward := *msg
	forward.Params = mustMarshalRaw(params)

	resp, err := g.send(r.Context(), srv, &forward)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, rpcResponse{JSONRPC: "2.0", ID: id, Error: upstreamErr(srv.Name, err)})
		return
	}
	g.rewriteResourceReadResponse(resp, srv)
	g.writeForwardedResponse(w, id, resp)
}

func (g *Gateway) writeForwardedResponse(w http.ResponseWriter, id any, resp *upstreamResponse) {
	if resp.message == nil {
		writeJSON(w, http.StatusBadGateway, rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32603, Message: "upstream returned no JSON-RPC response"}})
		return
	}
	if resp.message.Error != nil {
		writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: id, Error: resp.message.Error})
		return
	}
	var result any = map[string]any{}
	if len(resp.message.Result) > 0 {
		_ = json.Unmarshal(resp.message.Result, &result)
	}
	writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (g *Gateway) listFromUpstream(ctx context.Context, srv db.MCPServer, method, resultKey string) (*listResponse, error) {
	out := &listResponse{Items: []map[string]any{}}
	cursor := ""
	for page := 0; page < 20; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		msg := &rpcMessage{
			JSONRPC: "2.0",
			ID:      mustMarshalRaw(nextID()),
			Method:  method,
			Params:  mustMarshalRaw(params),
		}
		resp, err := g.send(ctx, srv, msg)
		if err != nil {
			return nil, err
		}
		if resp.message == nil || resp.message.Error != nil {
			if resp.message != nil && resp.message.Error != nil {
				return nil, errors.New(resp.message.Error.Message)
			}
			return nil, errors.New("missing upstream response")
		}

		var decoded map[string]json.RawMessage
		if err := json.Unmarshal(resp.message.Result, &decoded); err != nil {
			return nil, err
		}
		var pageItems []map[string]any
		if raw := decoded[resultKey]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &pageItems)
		}
		out.Items = append(out.Items, pageItems...)

		cursor = ""
		if raw := decoded["nextCursor"]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &cursor)
		}
		if cursor == "" {
			break
		}
	}
	return out, nil
}

func (g *Gateway) send(ctx context.Context, srv db.MCPServer, msg *rpcMessage) (*upstreamResponse, error) {
	if err := g.ensureInitialized(ctx, srv); err != nil {
		return nil, err
	}
	resp, err := g.sendRaw(ctx, srv, msg, true)
	if err == nil && resp.statusCode != http.StatusNotFound && resp.statusCode != http.StatusBadRequest {
		return resp, nil
	}

	g.clearSession(srv.ID)
	if initErr := g.ensureInitialized(ctx, srv); initErr != nil {
		if err != nil {
			return nil, err
		}
		return nil, initErr
	}
	return g.sendRaw(ctx, srv, msg, true)
}

func (g *Gateway) ensureInitialized(ctx context.Context, srv db.MCPServer) error {
	g.mu.Lock()
	session := g.sessions[srv.ID]
	g.mu.Unlock()
	if session != nil {
		return nil
	}

	initMsg := &rpcMessage{
		JSONRPC: "2.0",
		ID:      mustMarshalRaw(nextID()),
		Method:  "initialize",
		Params: mustMarshalRaw(map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    serverName,
				"version": serverVersion,
			},
		}),
	}

	resp, err := g.sendRaw(ctx, srv, initMsg, false)
	if err != nil {
		return err
	}
	if resp.message == nil {
		return errors.New("initialize returned no JSON-RPC response")
	}
	if resp.message.Error != nil {
		return errors.New(resp.message.Error.Message)
	}

	version := ProtocolVersion
	var initResult struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(resp.message.Result) > 0 && json.Unmarshal(resp.message.Result, &initResult) == nil && initResult.ProtocolVersion != "" {
		version = initResult.ProtocolVersion
	}

	g.mu.Lock()
	g.sessions[srv.ID] = &upstreamSession{
		protocolVersion: version,
		sessionID:       resp.header.Get("MCP-Session-Id"),
		initializedAt:   time.Now(),
	}
	g.mu.Unlock()

	initialized := &rpcMessage{JSONRPC: "2.0", Method: "notifications/initialized"}
	_, _ = g.sendRaw(ctx, srv, initialized, true)
	return nil
}

func (g *Gateway) sendRaw(ctx context.Context, srv db.MCPServer, msg *rpcMessage, useSession bool) (*upstreamResponse, error) {
	payload, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("User-Agent", "relai-mcp-gateway/0.1")
	req.Header.Set("MCP-Protocol-Version", g.protocolVersion(srv.ID))
	if srv.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+srv.APIKey)
	}
	if msg.Method != "" {
		req.Header.Set("Mcp-Method", msg.Method)
	}
	if name := requestNameHeader(msg); name != "" {
		req.Header.Set("Mcp-Name", name)
	}
	if useSession {
		if sessionID := g.sessionID(srv.ID); sessionID != "" {
			req.Header.Set("MCP-Session-Id", sessionID)
		}
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	out := &upstreamResponse{statusCode: resp.StatusCode, header: resp.Header.Clone()}
	if resp.StatusCode == http.StatusAccepted {
		return out, nil
	}

	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	var data []byte
	if mediaType == "text/event-stream" {
		data, err = readFinalSSEData(resp.Body)
	} else {
		data, err = io.ReadAll(resp.Body)
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return out, nil
	}

	var rpc rpcMessage
	if err := json.Unmarshal(data, &rpc); err != nil {
		return nil, fmt.Errorf("decode upstream JSON-RPC response: %w", err)
	}
	out.message = &rpc
	return out, nil
}

func (g *Gateway) enabledHTTPServers() ([]db.MCPServer, error) {
	servers, err := g.repo.List()
	if err != nil {
		return nil, err
	}
	out := make([]db.MCPServer, 0, len(servers))
	for _, srv := range servers {
		if !srv.IsEnabled {
			continue
		}
		transport := strings.ToLower(strings.TrimSpace(srv.Transport))
		if transport == "" || transport == "http" || transport == "streamable_http" || transport == "sse" {
			if srv.URL != "" {
				out = append(out, srv)
			}
		}
	}
	return out, nil
}

func (g *Gateway) resolveName(name string) (*targetRef, error) {
	servers, err := g.enabledHTTPServers()
	if err != nil {
		return nil, err
	}
	for _, srv := range servers {
		prefix := serverPrefix(srv)
		if strings.HasPrefix(name, prefix+"__") {
			orig := strings.TrimPrefix(name, prefix+"__")
			if orig == "" {
				return nil, fmt.Errorf("invalid gateway-prefixed name %q", name)
			}
			return &targetRef{server: srv, name: orig}, nil
		}
	}
	return nil, fmt.Errorf("unknown gateway-prefixed name %q", name)
}

func (g *Gateway) unwrapResourceURI(rawURI string) (db.MCPServer, string, error) {
	parsed, err := url.Parse(rawURI)
	if err != nil || parsed.Scheme != "relai+mcp" {
		return db.MCPServer{}, "", fmt.Errorf("resource URI must come from resources/list")
	}
	serverID := strings.TrimPrefix(parsed.Host, "server-")
	var id int64
	if _, err := fmt.Sscanf(serverID, "%d", &id); err != nil {
		return db.MCPServer{}, "", fmt.Errorf("invalid resource URI server")
	}
	encoded := strings.TrimPrefix(parsed.Path, "/")
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return db.MCPServer{}, "", fmt.Errorf("invalid resource URI payload")
	}
	srv, err := g.repo.Get(id)
	if err != nil || srv == nil || !srv.IsEnabled {
		return db.MCPServer{}, "", fmt.Errorf("MCP server is not available")
	}
	return *srv, string(decoded), nil
}

func (g *Gateway) unwrapTaskID(rawTaskID string) (db.MCPServer, string, error) {
	parsed, err := url.Parse(rawTaskID)
	if err != nil || parsed.Scheme != "relai+mcp-task" {
		return db.MCPServer{}, "", fmt.Errorf("task ID must come from a Relai task response")
	}
	serverID := strings.TrimPrefix(parsed.Host, "server-")
	var id int64
	if _, err := fmt.Sscanf(serverID, "%d", &id); err != nil {
		return db.MCPServer{}, "", fmt.Errorf("invalid task ID server")
	}
	encoded := strings.TrimPrefix(parsed.Path, "/")
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return db.MCPServer{}, "", fmt.Errorf("invalid task ID payload")
	}
	srv, err := g.repo.Get(id)
	if err != nil || srv == nil || !srv.IsEnabled {
		return db.MCPServer{}, "", fmt.Errorf("MCP server is not available")
	}
	return *srv, string(decoded), nil
}

func (g *Gateway) rewriteResourceReadResponse(resp *upstreamResponse, srv db.MCPServer) {
	if resp == nil || resp.message == nil || len(resp.message.Result) == 0 {
		return
	}
	var result map[string]any
	if err := json.Unmarshal(resp.message.Result, &result); err != nil {
		return
	}
	contents, ok := result["contents"].([]any)
	if !ok {
		return
	}
	for _, raw := range contents {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if uri, ok := item["uri"].(string); ok {
			item["uri"] = wrappedResourceURI(srv, uri)
		}
	}
	resp.message.Result = mustMarshalRaw(result)
}

func (g *Gateway) rewriteTaskResponse(resp *upstreamResponse, srv db.MCPServer) {
	if resp == nil || resp.message == nil || len(resp.message.Result) == 0 {
		return
	}
	var result any
	if err := json.Unmarshal(resp.message.Result, &result); err != nil {
		return
	}
	rewriteTaskIDs(result, srv)
	resp.message.Result = mustMarshalRaw(result)
}

func rewriteTaskIDs(value any, srv db.MCPServer) {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if key == "taskId" {
				if taskID, ok := child.(string); ok && taskID != "" {
					v[key] = wrappedTaskID(srv, taskID)
				}
				continue
			}
			rewriteTaskIDs(child, srv)
		}
	case []any:
		for _, child := range v {
			rewriteTaskIDs(child, srv)
		}
	}
}

func (g *Gateway) protocolVersion(serverID int64) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s := g.sessions[serverID]; s != nil && s.protocolVersion != "" {
		return s.protocolVersion
	}
	return ProtocolVersion
}

func (g *Gateway) sessionID(serverID int64) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s := g.sessions[serverID]; s != nil {
		return s.sessionID
	}
	return ""
}

func (g *Gateway) clearSession(serverID int64) {
	g.mu.Lock()
	delete(g.sessions, serverID)
	g.mu.Unlock()
}

func (g *Gateway) validOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

func transformName(srv db.MCPServer, item map[string]any) map[string]any {
	out := cloneMap(item)
	if name, ok := out["name"].(string); ok && name != "" {
		out["name"] = serverPrefix(srv) + "__" + name
	}
	out["_meta"] = withRelaiMeta(out["_meta"], srv)
	return out
}

func transformResourceURI(srv db.MCPServer, item map[string]any) map[string]any {
	out := cloneMap(item)
	if uri, ok := out["uri"].(string); ok && uri != "" {
		out["uri"] = wrappedResourceURI(srv, uri)
	}
	out["_meta"] = withRelaiMeta(out["_meta"], srv)
	return out
}

func transformResourceTemplate(srv db.MCPServer, item map[string]any) map[string]any {
	out := cloneMap(item)
	out["_meta"] = withRelaiMeta(out["_meta"], srv)
	if name, ok := out["name"].(string); ok && name != "" {
		out["name"] = serverPrefix(srv) + "__" + name
	}
	return out
}

func withRelaiMeta(existing any, srv db.MCPServer) map[string]any {
	meta, _ := existing.(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	meta["relai/server_id"] = srv.ID
	meta["relai/server_name"] = srv.Name
	return meta
}

func wrappedResourceURI(srv db.MCPServer, uri string) string {
	return fmt.Sprintf("relai+mcp://server-%d/%s", srv.ID, base64.RawURLEncoding.EncodeToString([]byte(uri)))
}

func wrappedTaskID(srv db.MCPServer, taskID string) string {
	return fmt.Sprintf("relai+mcp-task://server-%d/%s", srv.ID, base64.RawURLEncoding.EncodeToString([]byte(taskID)))
}

func serverPrefix(srv db.MCPServer) string {
	slug := slugRE.ReplaceAllString(strings.TrimSpace(srv.Name), "_")
	slug = strings.Trim(slug, "_")
	if slug == "" {
		slug = "server"
	}
	return fmt.Sprintf("%s_%d", slug, srv.ID)
}

func requestNameHeader(msg *rpcMessage) string {
	if len(msg.Params) == 0 {
		return ""
	}
	var params map[string]any
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return ""
	}
	for _, key := range []string{"name", "uri"} {
		if v, ok := params[key].(string); ok {
			return v
		}
	}
	return ""
}

func readFinalSSEData(r io.Reader) ([]byte, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var data strings.Builder
	var last []byte
	flush := func() {
		if strings.TrimSpace(data.String()) != "" {
			last = []byte(strings.TrimSuffix(data.String(), "\n"))
		}
		data.Reset()
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			data.WriteByte('\n')
		}
	}
	flush()
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return last, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeHTTPRPCError(w http.ResponseWriter, id any, status, code int, message string, data any) {
	writeJSON(w, status, rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message, Data: data}})
}

func invalidParams(message string) *rpcError {
	return &rpcError{Code: -32602, Message: message}
}

func internalError(err error) *rpcError {
	return &rpcError{Code: -32603, Message: err.Error()}
}

func upstreamErr(server string, err error) *rpcError {
	return &rpcError{Code: -32603, Message: "upstream MCP server error", Data: map[string]any{"server": server, "error": err.Error()}}
}

func decodeID(raw json.RawMessage) any {
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func mustMarshalRaw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func nextID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func contains(values []string, needle string) bool {
	for _, v := range values {
		if v == needle {
			return true
		}
	}
	return false
}
