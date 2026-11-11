package handlers

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/bfenty/relai/internal/db"
)

func TestOpenAIExternalBackendHealthUsesProviderCredential(t *testing.T) {
	t.Helper()
	const providerKey = "test-openai-key"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+providerKey {
			http.Error(w, "missing provider credential", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	d, err := db.New(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()
	backend, err := d.Backends.Create(db.BackendInput{
		Name: "openai-fallback", BaseURL: upstream.URL, APIKey: providerKey,
		Provider: db.BackendProviderOpenAI, Origin: db.BackendOriginExternal,
	})
	if err != nil {
		t.Fatalf("create backend: %v", err)
	}

	h := NewAdminHandler(d, nil, nil)
	rec := httptest.NewRecorder()
	h.HandleBackendHealth(rec, httptest.NewRequest(http.MethodPost,
		"/api/backends/"+strconv.FormatInt(backend.ID, 10)+"/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("health response = %d: %s", rec.Code, rec.Body.String())
	}
}
