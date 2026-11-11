package proxy

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/bfenty/relai/internal/db"
	"github.com/bfenty/relai/internal/queue"
)

func testDBForProxy(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.New(filepath.Join(t.TempDir(), "prio.db"))
	if err != nil {
		t.Fatalf("test db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// TestInteractivePriorityHeader covers the Halogen admission-reserve wiring:
// the header is set only for interactive contexts and a client-supplied
// header is always stripped, so priority is relai's decision alone.
func TestInteractivePriorityHeader(t *testing.T) {
	var gotHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v, ok := r.Header["X-Halogen-Priority"]; ok {
			gotHeader = v[0]
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	p := New(testDBForProxy(t), nil, queue.NewTracker())
	backend := &db.Backend{ID: 1, Name: "halogen-test", BaseURL: upstream.URL, Status: "active"}

	run := func(t *testing.T, ctx *RequestContext, clientHeader string) string {
		gotHeader = "<unset>"
		body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if clientHeader != "" {
			req.Header.Set("X-Halogen-Priority", clientHeader)
		}
		rec := httptest.NewRecorder()
		if err := p.ProxyRequest(rec, req, backend, &db.ModelMap{LocalModel: "m", RemoteModel: "m"}, ctx); err != nil {
			t.Fatalf("proxy request: %v", err)
		}
		return gotHeader
	}

	t.Run("interactive key sets header", func(t *testing.T) {
		if got := run(t, &RequestContext{Interactive: true}, ""); got != "1" {
			t.Fatalf("upstream X-Halogen-Priority = %q, want 1", got)
		}
	})
	t.Run("ordinary key sends no header", func(t *testing.T) {
		if got := run(t, &RequestContext{}, ""); got != "<unset>" {
			t.Fatalf("upstream X-Halogen-Priority = %q, want unset", got)
		}
	})
	t.Run("client cannot claim priority", func(t *testing.T) {
		if got := run(t, &RequestContext{}, "1"); got != "<unset>" {
			t.Fatalf("client-supplied header leaked: %q", got)
		}
	})
}
