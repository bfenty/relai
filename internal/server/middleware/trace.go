package middleware

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// TraceIDHeader carries a per-request correlation ID on every response.
const TraceIDHeader = "X-Trace-Id"

// NewTraceID returns an 8-hex-char correlation ID.
func NewTraceID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(b[:])
}

// TraceMiddleware assigns a trace ID to every request, sets the X-Trace-Id
// response header, and enriches JSON error bodies with a "trace" field so a
// UI-visible error can be pinned to one greppable journal line.
//
// Only JSON error responses (status >= 400, Content-Type json) are buffered;
// SSE streams, non-JSON bodies, and success responses pass through untouched.
func TraceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trace := r.Header.Get(TraceIDHeader)
		if trace == "" {
			trace = NewTraceID()
		}
		w.Header().Set(TraceIDHeader, trace)
		tw := &traceWriter{ResponseWriter: w, status: http.StatusOK, trace: trace, path: r.URL.Path}
		next.ServeHTTP(tw, r)
		tw.finish()
	})
}

type traceWriter struct {
	http.ResponseWriter
	status    int
	wroteHdr  bool
	buffering bool // JSON error path: body captured, nothing written downstream yet
	trace     string
	path      string
	body      bytes.Buffer
}

func (t *traceWriter) WriteHeader(code int) {
	t.status = code
	if code >= 400 && isJSONContentType(t.Header().Get("Content-Type")) {
		// Defer the header write; finish() decides the final shape.
		t.buffering = true
		return
	}
	t.wroteHdr = true
	t.ResponseWriter.WriteHeader(code)
}

func (t *traceWriter) Write(b []byte) (int, error) {
	if !t.wroteHdr && !t.buffering {
		t.WriteHeader(http.StatusOK)
	}
	if t.buffering {
		return t.body.Write(b)
	}
	return t.ResponseWriter.Write(b)
}

// Flush forwards to the underlying ResponseWriter when it supports flushing
// (SSE dashboard stream). Buffered error responses have nothing to flush.
func (t *traceWriter) Flush() {
	if t.buffering {
		return
	}
	if f, ok := t.ResponseWriter.(http.Flusher); ok && t.wroteHdr {
		f.Flush()
	}
}

// Unwrap exposes the wrapped ResponseWriter for http.Unwrap consumers.
func (t *traceWriter) Unwrap() http.ResponseWriter { return t.ResponseWriter }

func isJSONContentType(ct string) bool {
	if ct == "" {
		return true // handlers that WriteHeader(4xx) without a type usually mean JSON
	}
	ct = strings.ToLower(ct)
	return strings.Contains(ct, "application/json") || strings.Contains(ct, "text/json")
}

func (t *traceWriter) finish() {
	if !t.buffering {
		return
	}
	h := t.Header()
	h.Set("Content-Type", "application/json")
	h.Set(TraceIDHeader, t.trace)

	raw := t.body.Bytes()
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil {
		if _, has := obj["trace"]; !has {
			obj["trace"] = t.trace
		}
		// OpenAI-shaped errors nest under "error": add trace there too so
		// clients reading error.trace find it without top-level knowledge.
		if inner, ok := obj["error"].(map[string]any); ok {
			if _, has := inner["trace"]; !has {
				inner["trace"] = t.trace
			}
		}
		if out, err := json.Marshal(obj); err == nil {
			t.ResponseWriter.WriteHeader(t.status)
			t.ResponseWriter.Write(out)
			t.logError("")
			return
		}
	}

	// Non-JSON or unparseable error body: wrap it honestly.
	wrapped, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"message": http.StatusText(t.status),
			"type":    "relai_error",
			"code":    t.status,
			"trace":   t.trace,
		},
	})
	t.ResponseWriter.WriteHeader(t.status)
	t.ResponseWriter.Write(wrapped)
	t.logError(string(raw))
}

func (t *traceWriter) logError(rawBody string) {
	if rawBody != "" {
		rawBody = " raw=" + truncate(rawBody, 200)
	}
	log.Printf("level=warning msg=\"API error response\" status=%d trace=%s path=%s%s",
		t.status, t.trace, t.path, rawBody)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
