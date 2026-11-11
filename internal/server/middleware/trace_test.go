package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTraceMiddlewareEnrichesJSONError(t *testing.T) {
	h := TraceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"message":"bad model","type":"relai_error","code":400}}`))
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/v1/chat/completions", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	trace := rec.Header().Get(TraceIDHeader)
	if len(trace) != 8 {
		t.Fatalf("X-Trace-Id = %q, want 8 hex chars", trace)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v (%s)", err, rec.Body.String())
	}
	inner, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("error object missing: %s", rec.Body.String())
	}
	if inner["message"] != "bad model" {
		t.Errorf("message altered: %v", inner["message"])
	}
	if inner["trace"] != trace {
		t.Errorf("error.trace = %v, want %s", inner["trace"], trace)
	}
	if body["trace"] != trace {
		t.Errorf("top-level trace = %v, want %s", body["trace"], trace)
	}
}

func TestTraceMiddlewareSuccessPassthrough(t *testing.T) {
	h := TraceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/models", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != `{"ok":true}` {
		t.Errorf("success body modified: %s", rec.Body.String())
	}
	if rec.Header().Get(TraceIDHeader) == "" {
		t.Error("success response missing X-Trace-Id")
	}
}

func TestTraceMiddlewarePreservesExistingTrace(t *testing.T) {
	h := TraceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"denied"}}`, http.StatusForbidden)
	}))

	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	req.Header.Set(TraceIDHeader, "cafebabe")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get(TraceIDHeader); got != "cafebabe" {
		t.Errorf("inbound trace not reused: %q", got)
	}
}

func TestTraceMiddlewareDoesNotBufferSSE(t *testing.T) {
	// SSE responses are 200 + text/event-stream: must stream through with
	// flushes intact, never buffered by the error path.
	flushed := 0
	h := TraceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: a\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
			flushed++
		}
		w.Write([]byte("data: b\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
			flushed++
		}
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/dashboard/stream", nil))

	if flushed != 2 {
		t.Errorf("Flush() forwarded %d times, want 2 (flush chain broken)", flushed)
	}
	if rec.Body.String() != "data: a\n\ndata: b\n\n" {
		t.Errorf("SSE body altered: %q", rec.Body.String())
	}
}

func TestTraceMiddlewareWrapsNonJSONError(t *testing.T) {
	h := TraceMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("too many failed attempts, try again later"))
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/auth/login", nil))

	// Non-JSON error bodies pass through unmodified (honest, not reshaped).
	if rec.Body.String() != "too many failed attempts, try again later" {
		t.Errorf("non-JSON error body was reshaped: %q", rec.Body.String())
	}
	if rec.Header().Get(TraceIDHeader) == "" {
		t.Error("missing X-Trace-Id on non-JSON error")
	}
}
