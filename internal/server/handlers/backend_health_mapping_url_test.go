package handlers

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/bfenty/relai/internal/db"
)

func healthCheck(t *testing.T, d *db.DB, id int64) *httptest.ResponseRecorder {
	t.Helper()
	h := NewAdminHandler(d, nil, nil)
	rec := httptest.NewRecorder()
	h.HandleBackendHealth(rec, httptest.NewRequest(http.MethodPost,
		"/api/backends/"+strconv.FormatInt(id, 10)+"/health", nil))
	return rec
}

func modelsServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"data":[]}`))
	}))
}

// A backend whose model mappings carry their own base_url must be probed there.
// proxy.ProxyRequest prefers the mapping's URL, so probing only the backend's -
// e.g. a bare hostname that resolves to port 80 - checks an address no traffic
// uses, and a healthy host gets dropped from ListActive().
func TestBackendHealthUsesModelMappingURL(t *testing.T) {
	upstream := modelsServer(t)
	defer upstream.Close()

	d, err := db.New(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	// Backend URL points at a port with nothing on it; the mapping carries the real one.
	b, err := d.Backends.Create(db.BackendInput{
		Name: "remote-host", BaseURL: "http://127.0.0.1:9", Weight: 1, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatalf("create backend: %v", err)
	}
	if _, err := d.ModelMaps.Create(db.ModelMapInput{
		BackendID: b.ID, LocalModel: "qwen-next-remote", RemoteModel: "qwen3.8-flash-next",
		BaseURL: upstream.URL,
	}); err != nil {
		t.Fatalf("create mapping: %v", err)
	}

	rec := healthCheck(t, d, b.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("health check failed: %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	after, _ := d.Backends.Get(b.ID)
	if after.Status != "active" {
		t.Errorf("host marked %q, want active", after.Status)
	}
	active, _ := d.Backends.ListActive()
	if len(active) != 1 {
		t.Fatalf("healthy host dropped out of ListActive: got %d", len(active))
	}
}

// With no mapping override, the backend's own URL is still used.
func TestBackendHealthFallsBackToBackendURL(t *testing.T) {
	upstream := modelsServer(t)
	defer upstream.Close()

	d, _ := db.New(":memory:")
	t.Cleanup(func() { d.Close() })
	b, _ := d.Backends.Create(db.BackendInput{
		Name: "plain", BaseURL: upstream.URL, Weight: 1, MaxConcurrent: 1,
	})
	d.ModelMaps.Create(db.ModelMapInput{
		BackendID: b.ID, LocalModel: "m", RemoteModel: "r",
	})

	if rec := healthCheck(t, d, b.ID); rec.Code != http.StatusOK {
		t.Fatalf("health check failed: %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	after, _ := d.Backends.Get(b.ID)
	if after.Status != "active" {
		t.Errorf("status %q, want active", after.Status)
	}
}

// If nothing serving the host answers, it is still marked error.
func TestBackendHealthAllURLsDownMarksError(t *testing.T) {
	d, _ := db.New(":memory:")
	t.Cleanup(func() { d.Close() })
	b, _ := d.Backends.Create(db.BackendInput{
		Name: "down", BaseURL: "http://127.0.0.1:9", Weight: 1, MaxConcurrent: 1,
	})
	d.ModelMaps.Create(db.ModelMapInput{
		BackendID: b.ID, LocalModel: "m", RemoteModel: "r", BaseURL: "http://127.0.0.1:9",
	})

	if rec := healthCheck(t, d, b.ID); rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
	after, _ := d.Backends.Get(b.ID)
	if after.Status != "error" {
		t.Errorf("status %q, want error", after.Status)
	}
}
