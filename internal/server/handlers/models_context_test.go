package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bfenty/relai/internal/db"
	"github.com/bfenty/relai/internal/proxy"
	"github.com/bfenty/relai/internal/server/middleware"
	"github.com/bfenty/relai/pkg/openai"
)

// /v1/models must advertise each model's context window so clients (Hermes,
// for one) stop guessing from hardcoded catalogs. Halogen reports `context`
// on /health; llama.cpp reports `n_ctx` on /props. A model whose backends
// report nothing must omit the field entirely — never a fabricated 0.
func TestHandleModelsReportsContextLength(t *testing.T) {
	halogen := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte(`{"status":"ok","context":262144,"version":{"api":"0.18.10"}}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer halogen.Close()

	llama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/props" {
			_, _ = w.Write([]byte(`{"n_ctx":32768}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer llama.Close()

	silent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer silent.Close()

	d, err := db.New(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()

	for _, cfg := range []struct {
		name   string
		url    string
		local  string
		remote string
	}{
		{"halogen", halogen.URL, "qwen-router-model", "qwen-remote"},
		{"llama", llama.URL, "llama-model", "llama-remote"},
		{"silent", silent.URL, "silent-model", "silent-remote"},
	} {
		b, err := d.Backends.Create(db.BackendInput{Name: cfg.name, BaseURL: cfg.url, MaxConcurrent: 4})
		if err != nil {
			t.Fatalf("create backend %s: %v", cfg.name, err)
		}
		if _, err := d.ModelMaps.Create(db.ModelMapInput{
			BackendID: b.ID, RemoteModel: cfg.remote, LocalModel: cfg.local,
		}); err != nil {
			t.Fatalf("create model map %s: %v", cfg.local, err)
		}
	}

	h := NewOpenAIHandler(d, proxy.New(d, nil, nil), nil, nil, nil)

	_, plain, err := d.APIKeys.Create(db.APIKeyInput{Name: "models-test", Owner: "test"})
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}

	authed := middleware.Auth(d.APIKeys, "", nil)(http.HandlerFunc(h.HandleModels))
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	rec := httptest.NewRecorder()
	authed.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	var list openai.ModelList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	byID := map[string]openai.Model{}
	for _, m := range list.Data {
		byID[m.ID] = m
	}

	if got := byID["qwen-router-model"].ContextLength; got != 262144 {
		t.Errorf("halogen model context = %d, want 262144", got)
	}
	if got := byID["llama-model"].ContextLength; got != 32768 {
		t.Errorf("llama model context = %d, want 32768", got)
	}
	if got := byID["silent-model"].ContextLength; got != 0 {
		t.Errorf("silent model context = %d, want omitted/0", got)
	}
	// The silent model must not emit a fabricated zero in JSON.
	var raw struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	for _, m := range raw.Data {
		if m["id"] == "silent-model" {
			if _, ok := m["context_length"]; ok {
				t.Error("silent model must omit context_length, found fabricated value")
			}
		}
	}
}

// A virtual router whose members advertise different windows reports the
// smaller one: the router may place the conversation on either host, so
// promising the larger window would be a lie half the time. (Plain model
// maps cannot express this — local_model is UNIQUE — so the router is the
// real multi-backend topology.)
func TestHandleModelsContextTakesSmallestBackendWindow(t *testing.T) {
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"context":262144,"version":{"api":"0.18.10"}}`))
	}))
	defer big.Close()
	small := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"context":131072,"version":{"api":"0.18.10"}}`))
	}))
	defer small.Close()

	d, err := db.New(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()

	rt, err := d.Routers.Create(db.RouterInput{Name: "shared-router", Strategy: "sticky"})
	if err != nil {
		t.Fatalf("create router: %v", err)
	}
	for i, u := range []string{big.URL, small.URL} {
		b, err := d.Backends.Create(db.BackendInput{Name: string(rune('a' + i)), BaseURL: u, MaxConcurrent: 4})
		if err != nil {
			t.Fatalf("create backend: %v", err)
		}
		mm, err := d.ModelMaps.Create(db.ModelMapInput{
			BackendID: b.ID, RemoteModel: "remote-" + string(rune('a'+i)),
			LocalModel: "member-" + string(rune('a'+i)),
		})
		if err != nil {
			t.Fatalf("create model map: %v", err)
		}
		if _, err := d.Routers.AddMember(rt.ID, mm.ID, i); err != nil {
			t.Fatalf("add member: %v", err)
		}
	}

	h := NewOpenAIHandler(d, proxy.New(d, nil, nil), nil, nil, nil)
	_, plain, err := d.APIKeys.Create(db.APIKeyInput{Name: "models-test", Owner: "test"})
	if err != nil {
		t.Fatalf("create api key: %v", err)
	}

	authed := middleware.Auth(d.APIKeys, "", nil)(http.HandlerFunc(h.HandleModels))
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	rec := httptest.NewRecorder()
	authed.ServeHTTP(rec, req)

	var list openai.ModelList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, m := range list.Data {
		if m.ID == "shared-router" {
			if m.ContextLength != 131072 {
				t.Errorf("router context = %d, want 131072 (smallest member)", m.ContextLength)
			}
			return
		}
	}
	t.Fatal("shared-router missing from listing")
}
