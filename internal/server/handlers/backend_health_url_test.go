package handlers

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/bfenty/relai/internal/db"
)

// A base_url with a trailing slash must still probe "/v1/models", not "//v1/models".
// llama.cpp answers the doubled path with 404, which marked healthy hosts as "error"
// and dropped them out of ListActive() - every request then returned 503 "no active
// backend serves model", while the model mapping (which normalises) showed healthy.
func TestBackendHealthNormalisesTrailingSlash(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[]}`))
	}))
	defer upstream.Close()

	d, err := db.New(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	b, err := d.Backends.Create(db.BackendInput{
		Name: "trailing-slash", BaseURL: upstream.URL + "/", Weight: 1, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatalf("create backend: %v", err)
	}

	h := NewAdminHandler(d, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/backends/"+strconv.FormatInt(b.ID, 10)+"/health", nil)
	rec := httptest.NewRecorder()
	h.HandleBackendHealth(rec, req)

	if gotPath != "/v1/models" {
		t.Errorf("probed %q, want /v1/models", gotPath)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("health check failed: %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	after, err := d.Backends.Get(b.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if after.Status != "active" {
		t.Errorf("healthy host marked %q, want active", after.Status)
	}

	active, err := d.Backends.ListActive()
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("healthy host dropped out of ListActive: got %d", len(active))
	}
}

// A host without a trailing slash must keep working.
func TestBackendHealthPlainURLStillWorks(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"data":[]}`))
	}))
	defer upstream.Close()

	d, _ := db.New(":memory:")
	t.Cleanup(func() { d.Close() })
	b, _ := d.Backends.Create(db.BackendInput{
		Name: "plain", BaseURL: upstream.URL, Weight: 1, MaxConcurrent: 1,
	})

	h := NewAdminHandler(d, nil, nil)
	rec := httptest.NewRecorder()
	h.HandleBackendHealth(rec, httptest.NewRequest(http.MethodPost,
		"/api/backends/"+strconv.FormatInt(b.ID, 10)+"/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("health check failed: %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	after, _ := d.Backends.Get(b.ID)
	if after.Status != "active" {
		t.Errorf("status %q, want active", after.Status)
	}
	if after.LastLatencyMs < 0 {
		t.Errorf("latency not recorded: %d", after.LastLatencyMs)
	}
}

// A genuinely bad host is still marked error.
func TestBackendHealthMarksBadUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	d, _ := db.New(":memory:")
	t.Cleanup(func() { d.Close() })
	b, _ := d.Backends.Create(db.BackendInput{
		Name: "bad", BaseURL: upstream.URL, Weight: 1, MaxConcurrent: 1,
	})

	h := NewAdminHandler(d, nil, nil)
	rec := httptest.NewRecorder()
	h.HandleBackendHealth(rec, httptest.NewRequest(http.MethodPost,
		"/api/backends/"+strconv.FormatInt(b.ID, 10)+"/health", nil))

	after, _ := d.Backends.Get(b.ID)
	if after.Status != "error" {
		t.Errorf("bad upstream marked %q, want error", after.Status)
	}
}
