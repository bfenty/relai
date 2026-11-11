package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bfenty/relai/internal/db"
)

// Issue #12: Ollama native endpoints must coexist with the AdminAuth /api/*
// namespace. chi dispatches exact static paths registered on the root mux
// before any later group, so /api/chat authenticates with rlai_ keys while
// /api/backends keeps demanding admin auth.

func setupOllamaTestKey(t *testing.T, d *db.DB) string {
	t.Helper()
	_, plain, err := d.APIKeys.CreateTestKey("ollama-test")
	if err != nil {
		t.Fatalf("create test key: %v", err)
	}
	return plain
}

// stubOpenAIBackend returns an httptest server speaking OpenAI SSE, plus the
// backend/model needed to route to it.
func stubOpenAIBackend(t *testing.T, s *Server, chunks []string, usage string) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
		}
		if usage != "" {
			fmt.Fprintf(w, "data: %s\n\n", usage)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(upstream.Close)

	b, err := s.db.Backends.Create(db.BackendInput{
		Name: "stub", BaseURL: upstream.URL, Weight: 1, MaxConcurrent: 2, Status: "active",
	})
	if err != nil {
		t.Fatalf("create backend: %v", err)
	}
	if _, err := s.db.ModelMaps.Create(db.ModelMapInput{
		BackendID: b.ID, LocalModel: "test-model", RemoteModel: "test-model", ModelType: "chat",
	}); err != nil {
		t.Fatalf("create model map: %v", err)
	}
}

func TestOllamaChatStreamProducesNDJSON(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServer(t, d)
	key := setupOllamaTestKey(t, d)
	stubOpenAIBackend(t, srv, []string{
		`{"choices":[{"delta":{"content":"Hello"}}]}`,
		`{"choices":[{"delta":{"content":" world"}}]}`,
	}, `{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":7}}`)

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("content-type = %q, want application/x-ndjson", ct)
	}
	scanner := bufio.NewScanner(strings.NewReader(rec.Body.String()))
	var deltas []string
	var last map[string]any
	lines := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lines++
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("line %d is not valid JSON: %v (%s)", lines, err, line)
		}
		last = obj
		if done, _ := obj["done"].(bool); !done {
			msg, _ := obj["message"].(map[string]any)
			if msg == nil {
				t.Fatalf("intermediate line missing message: %s", line)
			}
			deltas = append(deltas, fmt.Sprint(msg["content"]))
		}
	}
	if lines < 3 {
		t.Fatalf("expected >=3 NDJSON lines (2 deltas + final), got %d: %s", lines, rec.Body.String())
	}
	if got := strings.Join(deltas, ""); got != "Hello world" {
		t.Fatalf("streamed content = %q, want %q", got, "Hello world")
	}
	if done, _ := last["done"].(bool); !done {
		t.Fatalf("final line must have done:true, got %v", last)
	}
	if ec, _ := last["eval_count"].(float64); ec != 7 {
		t.Fatalf("eval_count = %v, want 7 (usage chunk must survive)", last["eval_count"])
	}
	if pec, _ := last["prompt_eval_count"].(float64); pec != 5 {
		t.Fatalf("prompt_eval_count = %v, want 5", last["prompt_eval_count"])
	}
}

func TestOllamaChatNonStream(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServer(t, d)
	key := setupOllamaTestKey(t, d)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"model":"test-model","choices":[{"message":{"role":"assistant","content":"Hi there"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`)
	}))
	t.Cleanup(upstream.Close)
	b, _ := srv.db.Backends.Create(db.BackendInput{Name: "stub", BaseURL: upstream.URL, Weight: 1, MaxConcurrent: 2, Status: "active"})
	srv.db.ModelMaps.Create(db.ModelMapInput{BackendID: b.ID, LocalModel: "test-model", RemoteModel: "test-model", ModelType: "chat"})

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hi"}],"stream":false}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Model           string `json:"model"`
		Done            bool   `json:"done"`
		DoneReason      string `json:"done_reason"`
		EvalCount       int    `json:"eval_count"`
		PromptEvalCount int    `json:"prompt_eval_count"`
		Message         struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if resp.Message.Content != "Hi there" || !resp.Done || resp.EvalCount != 4 || resp.PromptEvalCount != 3 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestOllamaGenerateNonStream(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServer(t, d)
	key := setupOllamaTestKey(t, d)

	var gotBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"4"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`)
	}))
	t.Cleanup(upstream.Close)
	b, _ := srv.db.Backends.Create(db.BackendInput{Name: "stub", BaseURL: upstream.URL, Weight: 1, MaxConcurrent: 2, Status: "active"})
	srv.db.ModelMaps.Create(db.ModelMapInput{BackendID: b.ID, LocalModel: "test-model", RemoteModel: "test-model", ModelType: "chat"})

	req := httptest.NewRequest(http.MethodPost, "/api/generate", strings.NewReader(
		`{"model":"test-model","prompt":"2+2?","system":"You answer briefly.","stream":false,"options":{"temperature":0.2,"num_predict":8,"unknown_option":123}}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	// generate shape: "response" field, not "message"
	var resp struct {
		Response string `json:"response"`
		Done     bool   `json:"done"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Response != "4" || !resp.Done {
		t.Fatalf("generate response wrong: %v body=%s", err, rec.Body.String())
	}
	// translation checks on the forwarded OpenAI body
	if gotBody["temperature"] != 0.2 {
		t.Fatalf("temperature not translated: %v", gotBody["temperature"])
	}
	if gotBody["max_tokens"] != float64(8) {
		t.Fatalf("num_predict not mapped to max_tokens: %v", gotBody["max_tokens"])
	}
	if _, ok := gotBody["unknown_option"]; ok {
		t.Fatal("unknown ollama option must not leak into OpenAI body")
	}
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("system+prompt must become 2 messages, got %d: %v", len(msgs), msgs)
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "You answer briefly." {
		t.Fatalf("system message not first: %v", first)
	}
}

func TestOllamaTagsListsRouters(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServer(t, d)
	key := setupOllamaTestKey(t, d)

	if _, err := d.Routers.Create(db.RouterInput{Name: "smart-router", Strategy: "round_robin", ModelType: "chat"}); err != nil {
		t.Fatalf("create router: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Models []struct {
			Name    string `json:"name"`
			Model   string `json:"model"`
			Details struct {
				Family string `json:"family"`
			} `json:"details"`
		} `json:"models"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Models) != 1 || resp.Models[0].Name != "smart-router" || resp.Models[0].Details.Family != "relai" {
		t.Fatalf("unexpected tags: %s", rec.Body.String())
	}
}

func TestOllamaVersion(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServer(t, d)
	key := setupOllamaTestKey(t, d)

	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp map[string]string
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["version"] == "" {
		t.Fatalf("version empty: %s", rec.Body.String())
	}
}

// The collision trap: /api/chat must accept rlai_ inference keys while
// /api/backends (same /api prefix, AdminAuth group) still rejects them.
func TestOllamaRoutesDoNotWeakenAdminRoutes(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServer(t, d)
	key := setupOllamaTestKey(t, d)

	// rlai_ key on an ollama route: must NOT be 401 (reaches handler; 400 for
	// empty body is fine — auth passed).
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusNotFound {
		t.Fatalf("ollama route rejected rlai_ key: %d %s", rec.Code, rec.Body.String())
	}

	// rlai_ key on admin route: must be 401/403 — AdminAuth still enforced.
	req = httptest.NewRequest(http.MethodGet, "/api/backends", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Fatalf("admin route accepted rlai_ key: %d %s", rec.Code, rec.Body.String())
	}

	// admin key on admin route: still works.
	req = httptest.NewRequest(http.MethodGet, "/api/backends", nil)
	req.Header.Set("Authorization", "Bearer admin")
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("admin route rejected admin key: %d", rec.Code)
	}
}

// Ollama routes must reject anonymous callers (same auth as /v1).
func TestOllamaRoutesRequireAuth(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServer(t, d)

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/chat"},
		{http.MethodPost, "/api/generate"},
		{http.MethodGet, "/api/tags"},
		{http.MethodGet, "/api/version"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without key: status = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

func TestOllamaUnknownModel404(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServer(t, d)
	key := setupOllamaTestKey(t, d)

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(
		`{"model":"nope","messages":[{"role":"user","content":"hi"}],"stream":false}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	// relai has no backends at all -> 503 service unavailable is the honest
	// upstream answer; what matters is Ollama-shaped {"error": ...} body.
	if rec.Code < 400 {
		t.Fatalf("expected error status, got %d", rec.Code)
	}
	var e map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e["error"] == "" {
		t.Fatalf("error body not ollama-shaped: %s", rec.Body.String())
	}
}

// ---- regression tests for review findings (deleg_3e9fe5f1) ----

// stubCapturingBackend routes test-model to an upstream that records the
// forwarded OpenAI body and answers with the given status/content-type/body.
func stubCapturingBackend(t *testing.T, s *Server, status int, contentType, body string, captured *map[string]any) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got map[string]any
		_ = json.NewDecoder(r.Body).Decode(&got)
		*captured = got
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(upstream.Close)
	b, err := s.db.Backends.Create(db.BackendInput{Name: "stub", BaseURL: upstream.URL, Weight: 1, MaxConcurrent: 2, Status: "active"})
	if err != nil {
		t.Fatalf("create backend: %v", err)
	}
	if _, err := s.db.ModelMaps.Create(db.ModelMapInput{BackendID: b.ID, LocalModel: "test-model", RemoteModel: "test-model", ModelType: "chat"}); err != nil {
		t.Fatalf("create model map: %v", err)
	}
}

// MAJOR: stream truncated upstream (no [DONE]) must report done_reason
// "truncate", never a clean "stop".
func TestOllamaStreamTruncationReportsTruncate(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServer(t, d)
	key := setupOllamaTestKey(t, d)
	stubOpenAIBackendNoDone := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
	}))
	t.Cleanup(stubOpenAIBackendNoDone.Close)
	b, _ := srv.db.Backends.Create(db.BackendInput{Name: "stub", BaseURL: stubOpenAIBackendNoDone.URL, Weight: 1, MaxConcurrent: 2, Status: "active"})
	srv.db.ModelMaps.Create(db.ModelMapInput{BackendID: b.ID, LocalModel: "test-model", RemoteModel: "test-model", ModelType: "chat"})

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var last map[string]any
	for _, line := range strings.Split(strings.TrimSpace(rec.Body.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if err := json.Unmarshal([]byte(line), &last); err != nil {
			t.Fatalf("bad NDJSON line %q: %v", line, err)
		}
	}
	if done, _ := last["done"].(bool); !done {
		t.Fatalf("expected final done line, got %v", last)
	}
	if dr, _ := last["done_reason"].(string); dr != "truncate" {
		t.Fatalf("done_reason = %v, want truncate", last["done_reason"])
	}
}

// MAJOR: upstream finish_reason=length must surface as done_reason=length.
func TestOllamaStreamFinishReasonLength(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServer(t, d)
	key := setupOllamaTestKey(t, d)
	stubOpenAIBackend(t, srv, []string{
		`{"choices":[{"delta":{"content":"cut"},"finish_reason":"length"}]}`,
	}, "")

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"done_reason":"length"`) {
		t.Fatalf("expected done_reason length, got: %s", body)
	}
}

// MAJOR: stream:true against a backend that answers with a single JSON body
// must still produce valid NDJSON with the content, not an empty stream.
func TestOllamaStreamNonSSEBodyTranslates(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServer(t, d)
	key := setupOllamaTestKey(t, d)
	var captured map[string]any
	stubCapturingBackend(t, srv, http.StatusOK, "application/json",
		`{"model":"test-model","choices":[{"message":{"role":"assistant","content":"json answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`,
		&captured)

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "json answer") {
		t.Fatalf("expected content delta in NDJSON, got: %s", body)
	}
	if !strings.Contains(body, `"done":true`) {
		t.Fatalf("expected done:true line, got: %s", body)
	}
}

// MAJOR: upstream response headers must not leak into the Ollama response.
func TestOllamaStreamDoesNotLeakUpstreamHeaders(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServer(t, d)
	key := setupOllamaTestKey(t, d)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Upstream-Secret", "leaky")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(upstream.Close)
	b, _ := srv.db.Backends.Create(db.BackendInput{Name: "stub", BaseURL: upstream.URL, Weight: 1, MaxConcurrent: 2, Status: "active"})
	srv.db.ModelMaps.Create(db.ModelMapInput{BackendID: b.ID, LocalModel: "test-model", RemoteModel: "test-model", ModelType: "chat"})

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if v := rec.Header().Get("X-Upstream-Secret"); v != "" {
		t.Fatalf("upstream header leaked: X-Upstream-Secret=%q", v)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("content-type = %q, want application/x-ndjson", ct)
	}
}

// MAJOR: num_predict:-1 (unbounded) must not be forwarded as max_tokens:-1;
// MAJOR: format schema must be wrapped in the json_schema envelope.
func TestOllamaOptionsTranslation(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServer(t, d)
	key := setupOllamaTestKey(t, d)
	var captured map[string]any
	stubCapturingBackend(t, srv, http.StatusOK, "application/json",
		`{"model":"test-model","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`,
		&captured)

	schema := `{"type":"object","properties":{"x":{"type":"string"}}}`
	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hi"}],"stream":false,"format":`+schema+`,"options":{"num_predict":-1,"temperature":0.5}}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if mt, ok := captured["max_tokens"]; ok {
		t.Fatalf("num_predict:-1 must not forward max_tokens, got %v", mt)
	}
	if temp, _ := captured["temperature"].(float64); temp != 0.5 {
		t.Fatalf("temperature = %v, want 0.5", captured["temperature"])
	}
	rf, _ := captured["response_format"].(map[string]any)
	if rf == nil || rf["type"] != "json_schema" {
		t.Fatalf("response_format not wrapped as json_schema envelope: %v", captured["response_format"])
	}
	js, _ := rf["json_schema"].(map[string]any)
	if js == nil || js["name"] != "response" {
		t.Fatalf("json_schema envelope missing name: %v", rf)
	}
	sch, _ := js["schema"].(map[string]any)
	if sch == nil || sch["type"] != "object" {
		t.Fatalf("schema not forwarded: %v", js)
	}
}

// ---- regression tests for second-pass review (deleg_f22f9ebd) ----

// ollamaStubSSE routes test-model to a raw-SSE-speaking upstream.
func ollamaStubSSE(t *testing.T, s *Server, sseBody string) {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseBody)
	}))
	t.Cleanup(upstream.Close)
	b, err := s.db.Backends.Create(db.BackendInput{Name: "stub", BaseURL: upstream.URL, Weight: 1, MaxConcurrent: 2, Status: "active"})
	if err != nil {
		t.Fatalf("create backend: %v", err)
	}
	if _, err := s.db.ModelMaps.Create(db.ModelMapInput{BackendID: b.ID, LocalModel: "test-model", RemoteModel: "test-model", ModelType: "chat"}); err != nil {
		t.Fatalf("create model map: %v", err)
	}
}

// MAJOR-1: SSE stream that never yields content (keepalives + role-only
// delta) must produce valid NDJSON with a terminal line, never an
// application/json error body under HTTP 200.
func TestOllamaStreamContentlessSSEStaysNDJSON(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServer(t, d)
	key := setupOllamaTestKey(t, d)
	ollamaStubSSE(t, srv, ": keepalive\n\ndata: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("content-type = %q, want application/x-ndjson (content-less SSE must not become an error body)", ct)
	}
	body := strings.TrimSpace(rec.Body.String())
	var obj map[string]any
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		t.Fatalf("body is not a single NDJSON line: %q (%v)", body, err)
	}
	if done, _ := obj["done"].(bool); !done {
		t.Fatalf("expected terminal done:true line, got %v", obj)
	}
}

// MAJOR-2: clean finish_reason:"stop" without a [DONE] sentinel is a
// complete answer — done_reason must stay "stop", not "truncate".
func TestOllamaStreamStopWithoutDoneIsNotTruncated(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServer(t, d)
	key := setupOllamaTestKey(t, d)
	ollamaStubSSE(t, srv, "data: {\"choices\":[{\"delta\":{\"content\":\"whole\"},\"finish_reason\":\"stop\"}]}\n\n")

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"done_reason":"stop"`) {
		t.Fatalf("finish_reason stop must win over missing [DONE], got: %s", body)
	}
	if strings.Contains(body, "truncate") {
		t.Fatalf("must not report truncation for clean stop, got: %s", body)
	}
}

// MAJOR-3: backend ignoring stream:false and answering with SSE must be
// folded into one Ollama JSON completion, not passed through raw.
func TestOllamaNonStreamAgainstSSEBackendFolds(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServer(t, d)
	key := setupOllamaTestKey(t, d)
	ollamaStubSSE(t, srv,
		"data: {\"model\":\"test-model\",\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n"+
			"data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n"+
			"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2}}\n\n"+
			"data: [DONE]\n\n")

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hi"}],"stream":false}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "data:") {
		t.Fatalf("raw SSE leaked into non-stream response: %s", rec.Body.String())
	}
	var resp struct {
		Done       bool   `json:"done"`
		DoneReason string `json:"done_reason"`
		Message    struct {
			Content string `json:"content"`
		} `json:"message"`
		EvalCount int `json:"eval_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("not valid Ollama JSON: %v (%s)", err, rec.Body.String())
	}
	if resp.Message.Content != "Hello" {
		t.Fatalf("folded content = %q, want %q", resp.Message.Content, "Hello")
	}
	if !resp.Done || resp.DoneReason != "stop" {
		t.Fatalf("done/done_reason wrong: %+v", resp)
	}
	if resp.EvalCount != 2 {
		t.Fatalf("usage must survive folding, eval_count = %d", resp.EvalCount)
	}
}

// ---- regression tests for third-pass review (deleg_37732b8b) ----

// MAJOR: foldSSEToChat must not fabricate done_reason:"stop" for an SSE
// body that stopped mid-stream (no [DONE], no finish_reason).
func TestOllamaNonStreamFoldTruncatedSSENotsTop(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServer(t, d)
	key := setupOllamaTestKey(t, d)
	ollamaStubSSE(t, srv,
		"data: {\"model\":\"test-model\",\"choices\":[{\"delta\":{\"content\":\"partial \"}}]}\n\n")

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hi"}],"stream":false}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		DoneReason string `json:"done_reason"`
		Message    struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("not valid Ollama JSON: %v (%s)", err, rec.Body.String())
	}
	if resp.Message.Content != "partial " {
		t.Fatalf("content = %q, want the partial text", resp.Message.Content)
	}
	if resp.DoneReason != "truncate" {
		t.Fatalf("mid-stream death must report truncate, got done_reason=%q", resp.DoneReason)
	}
}

// MAJOR: an in-band SSE error event must surface as an Ollama error, not a
// fabricated empty success.
func TestOllamaNonStreamFoldSSEErrorEvent(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServer(t, d)
	key := setupOllamaTestKey(t, d)
	ollamaStubSSE(t, srv,
		"data: {\"error\":{\"message\":\"upstream exploded\",\"type\":\"server_error\"}}\n\n")

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hi"}],"stream":false}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if strings.Contains(rec.Body.String(), "data:") {
		t.Fatalf("raw SSE leaked: %s", rec.Body.String())
	}
	var resp struct {
		Error   string `json:"error"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("not valid Ollama JSON: %v (%s)", err, rec.Body.String())
	}
	if resp.Error == "" {
		t.Fatalf("SSE error event must produce a non-empty error field, got: %s", rec.Body.String())
	}
	if !strings.Contains(resp.Error, "upstream exploded") {
		t.Fatalf("error message should propagate, got %q", resp.Error)
	}
	if rec.Code == http.StatusOK {
		t.Fatalf("error event must not return 200 success shape: %s", rec.Body.String())
	}
}

// A non-streaming client against a backend that answers the forced
// stream:true with an unparseable 200 body must get exactly ONE Ollama
// error object — not an error followed by a second "no completion" body.
func TestOllamaNonStreamUntranslatableBodySingleError(t *testing.T) {
	d := setupServerTestDB(t)
	srv := setupTestServer(t, d)
	key := setupOllamaTestKey(t, d)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "not json at all")
	}))
	t.Cleanup(upstream.Close)
	b, err := srv.db.Backends.Create(db.BackendInput{
		Name: "stub", BaseURL: upstream.URL, Weight: 1, MaxConcurrent: 2, Status: "active",
	})
	if err != nil {
		t.Fatalf("create backend: %v", err)
	}
	if _, err := srv.db.ModelMaps.Create(db.ModelMapInput{
		BackendID: b.ID, LocalModel: "test-model", RemoteModel: "test-model", ModelType: "chat",
	}); err != nil {
		t.Fatalf("create model map: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(
		`{"model":"test-model","messages":[{"role":"user","content":"hi"}],"stream":false}`))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body: %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body must be a single JSON error object, got %q: %v", rec.Body.String(), err)
	}
	if resp.Error == "" {
		t.Fatalf("expected non-empty error field, got: %s", rec.Body.String())
	}
}
