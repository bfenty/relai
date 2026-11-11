package router

import (
	"context"
	"testing"

	"github.com/bfenty/relai/internal/db"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.New(":memory:")
	if err != nil {
		t.Fatalf("failed to create test DB: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestRoundRobin(t *testing.T) {
	d := setupTestDB(t)

	// Create two backends
	for i, name := range []string{"backend1", "backend2"} {
		backend, err := d.Backends.Create(db.BackendInput{
			Name:    name,
			BaseURL: "http://localhost:8080",
			Weight:  1,
		})
		if err != nil {
			t.Fatalf("failed to create backend: %v", err)
		}
		_, err = d.ModelMaps.Create(db.ModelMapInput{
			BackendID:   backend.ID,
			LocalModel:  []string{"gpt-4", "gpt-4-alt"}[i],
			RemoteModel: "remote-" + name,
		})
		if err != nil {
			t.Fatalf("failed to create model mapping: %v", err)
		}
	}

	rr := NewRoundRobin(d)
	ctx := context.Background()
	req := &RoutingRequest{Model: "gpt-4"}

	// Public Relai model names are unique, so a direct model resolves to one target.
	names := make(map[string]int)
	for i := 0; i < 4; i++ {
		b, err := rr.SelectBackend(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		names[b.Name]++
	}

	if names["backend1"] != 4 {
		t.Errorf("expected 4 requests to backend1, got %d", names["backend1"])
	}
	if names["backend2"] != 0 {
		t.Errorf("expected 0 requests to backend2, got %d", names["backend2"])
	}
}

func TestWeighted(t *testing.T) {
	d := setupTestDB(t)

	// Create backends with different weights
	heavy, err := d.Backends.Create(db.BackendInput{
		Name:    "heavy",
		BaseURL: "http://localhost:8080",
		Weight:  3,
	})
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}
	light, err := d.Backends.Create(db.BackendInput{
		Name:    "light",
		BaseURL: "http://localhost:8081",
		Weight:  1,
	})
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}
	for i, backend := range []*db.Backend{heavy, light} {
		_, err = d.ModelMaps.Create(db.ModelMapInput{
			BackendID:   backend.ID,
			LocalModel:  []string{"gpt-4", "gpt-4-light"}[i],
			RemoteModel: "remote-" + backend.Name,
		})
		if err != nil {
			t.Fatalf("failed to create model mapping: %v", err)
		}
	}

	w := NewWeighted(d)
	ctx := context.Background()
	req := &RoutingRequest{Model: "gpt-4"}

	// Public Relai model names are unique, so weighting applies once a router
	// has multiple explicit members; direct model lookup has one target.
	heavyCount := 0
	lightCount := 0
	for i := 0; i < 4; i++ {
		b, err := w.SelectBackend(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if b.Name == "heavy" {
			heavyCount++
		} else {
			lightCount++
		}
	}

	if heavyCount != 4 {
		t.Errorf("expected 4 heavy requests, got %d", heavyCount)
	}
	if lightCount != 0 {
		t.Errorf("expected 0 light requests, got %d", lightCount)
	}
}

func TestModelBased(t *testing.T) {
	d := setupTestDB(t)

	// Create a backend
	backend, err := d.Backends.Create(db.BackendInput{
		Name:    "openai",
		BaseURL: "https://api.openai.com",
	})
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}

	// Create model mapping
	_, err = d.ModelMaps.Create(db.ModelMapInput{
		BackendID:   backend.ID,
		RemoteModel: "gpt-4-0613",
		LocalModel:  "gpt-4",
	})
	if err != nil {
		t.Fatalf("failed to create model mapping: %v", err)
	}

	// A second target can use the same remote model family, but the Relai-facing
	// local model name must be unique.
	backend2, err := d.Backends.Create(db.BackendInput{
		Name:    "openai-2",
		BaseURL: "https://api2.openai.com",
	})
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}
	_, err = d.ModelMaps.Create(db.ModelMapInput{
		BackendID:   backend2.ID,
		RemoteModel: "gpt-4-1106",
		LocalModel:  "gpt-4-alt",
	})
	if err != nil {
		t.Fatalf("failed to create model mapping: %v", err)
	}

	mb := NewModelBased(d)
	ctx := context.Background()

	// Requests for gpt-4 resolve to the unique public model target.
	req := &RoutingRequest{Model: "gpt-4"}
	names := map[string]int{}
	for i := 0; i < 4; i++ {
		b, err := mb.SelectBackend(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		names[b.Name]++
	}
	if names["openai"] != 4 {
		t.Errorf("expected 4 openai requests, got %d", names["openai"])
	}
	if names["openai-2"] != 0 {
		t.Errorf("expected 0 openai-2 requests, got %d", names["openai-2"])
	}

	// Request for unknown model should fail
	req = &RoutingRequest{Model: "unknown-model"}
	_, err = mb.SelectBackend(ctx, req)
	if err == nil {
		t.Error("expected error for unknown model, got nil")
	}
}

func TestLatencyBased(t *testing.T) {
	d := setupTestDB(t)

	// Create backends with different latencies
	b1, err := d.Backends.Create(db.BackendInput{
		Name:    "fast",
		BaseURL: "http://localhost:8080",
	})
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}
	b2, err := d.Backends.Create(db.BackendInput{
		Name:    "slow",
		BaseURL: "http://localhost:8081",
	})
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}
	for i, backend := range []*db.Backend{b1, b2} {
		_, err = d.ModelMaps.Create(db.ModelMapInput{
			BackendID:   backend.ID,
			LocalModel:  []string{"gpt-4", "gpt-4-slow"}[i],
			RemoteModel: "remote-" + backend.Name,
		})
		if err != nil {
			t.Fatalf("failed to create model mapping: %v", err)
		}
	}

	// Set latencies
	d.Backends.UpdateHealth(b1.ID, "active", 50)
	d.Backends.UpdateHealth(b2.ID, "active", 200)

	lb := NewLatencyBased(d)
	ctx := context.Background()
	req := &RoutingRequest{Model: "gpt-4"}

	b, err := lb.SelectBackend(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.Name != "fast" {
		t.Errorf("expected fast backend, got %s", b.Name)
	}
}

func TestRoutersRequireExplicitModelPoolTargets(t *testing.T) {
	d := setupTestDB(t)

	included, err := d.Backends.Create(db.BackendInput{
		Name:    "included",
		BaseURL: "http://localhost:8080",
	})
	if err != nil {
		t.Fatalf("failed to create included backend: %v", err)
	}
	_, err = d.Backends.Create(db.BackendInput{
		Name:    "not-included",
		BaseURL: "http://localhost:8081",
	})
	if err != nil {
		t.Fatalf("failed to create unconfigured backend: %v", err)
	}
	_, err = d.ModelMaps.Create(db.ModelMapInput{
		BackendID:   included.ID,
		LocalModel:  "coding-fast",
		RemoteModel: "same-remote-name",
	})
	if err != nil {
		t.Fatalf("failed to create model mapping: %v", err)
	}

	rr := NewRoundRobin(d)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		target, err := rr.SelectTarget(ctx, &RoutingRequest{Model: "coding-fast"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if target.Backend.Name != "included" {
			t.Fatalf("expected included backend, got %s", target.Backend.Name)
		}
		if target.ModelMap.RemoteModel != "same-remote-name" {
			t.Fatalf("expected selected remote model same-remote-name, got %s", target.ModelMap.RemoteModel)
		}
	}

	_, err = rr.SelectTarget(ctx, &RoutingRequest{Model: "unknown"})
	if err == nil {
		t.Fatal("expected unknown friendly model to fail")
	}
}

func TestRouterFiltersTargetsByMode(t *testing.T) {
	d := setupTestDB(t)

	chatBackend, err := d.Backends.Create(db.BackendInput{
		Name:    "chat-backend",
		BaseURL: "http://localhost:8080",
	})
	if err != nil {
		t.Fatalf("failed to create chat backend: %v", err)
	}
	embedBackend, err := d.Backends.Create(db.BackendInput{
		Name:    "embed-backend",
		BaseURL: "http://localhost:8081",
	})
	if err != nil {
		t.Fatalf("failed to create embed backend: %v", err)
	}
	rerankBackend, err := d.Backends.Create(db.BackendInput{
		Name:    "rerank-backend",
		BaseURL: "http://localhost:8082",
	})
	if err != nil {
		t.Fatalf("failed to create rerank backend: %v", err)
	}

	for _, input := range []db.ModelMapInput{
		{
			BackendID:   chatBackend.ID,
			LocalModel:  "shared-model-chat",
			RemoteModel: "chat-remote",
			Modes:       []db.ModelMode{db.ModelModeChat},
		},
		{
			BackendID:   embedBackend.ID,
			LocalModel:  "shared-model-embed",
			RemoteModel: "embed-remote",
			Modes:       []db.ModelMode{db.ModelModeEmbed},
		},
		{
			BackendID:   rerankBackend.ID,
			LocalModel:  "shared-model-rerank",
			RemoteModel: "rerank-remote",
			Modes:       []db.ModelMode{db.ModelModeRerank},
		},
	} {
		if _, err := d.ModelMaps.Create(input); err != nil {
			t.Fatalf("failed to create model mapping: %v", err)
		}
	}

	rr := NewRoundRobin(d)
	tests := []struct {
		model       string
		mode        db.ModelMode
		wantBackend string
		wantRemote  string
	}{
		{"shared-model-chat", db.ModelModeChat, "chat-backend", "chat-remote"},
		{"shared-model-embed", db.ModelModeEmbed, "embed-backend", "embed-remote"},
		{"shared-model-rerank", db.ModelModeRerank, "rerank-backend", "rerank-remote"},
	}

	for _, tt := range tests {
		target, err := rr.SelectTarget(context.Background(), &RoutingRequest{
			Model: tt.model,
			Mode:  tt.mode,
		})
		if err != nil {
			t.Fatalf("unexpected error for mode %s: %v", tt.mode, err)
		}
		if target.Backend.Name != tt.wantBackend {
			t.Fatalf("backend for mode %s = %s, want %s", tt.mode, target.Backend.Name, tt.wantBackend)
		}
		if target.ModelMap.RemoteModel != tt.wantRemote {
			t.Fatalf("remote model for mode %s = %s, want %s", tt.mode, target.ModelMap.RemoteModel, tt.wantRemote)
		}
	}
}

func TestLeastConnections(t *testing.T) {
	d := setupTestDB(t)

	b1, err := d.Backends.Create(db.BackendInput{
		Name:    "busy",
		BaseURL: "http://localhost:8080",
	})
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}
	b2, err := d.Backends.Create(db.BackendInput{
		Name:    "idle",
		BaseURL: "http://localhost:8081",
	})
	if err != nil {
		t.Fatalf("failed to create backend: %v", err)
	}
	for i, backend := range []*db.Backend{b1, b2} {
		_, err = d.ModelMaps.Create(db.ModelMapInput{
			BackendID:   backend.ID,
			LocalModel:  []string{"gpt-4", "gpt-4-idle"}[i],
			RemoteModel: "remote-" + backend.Name,
		})
		if err != nil {
			t.Fatalf("failed to create model mapping: %v", err)
		}
	}

	active := NewActiveTracker()
	release := active.Increment(b1.ID)
	defer release()

	lc := NewLeastConnections(d, active)
	target, err := lc.SelectTarget(context.Background(), &RoutingRequest{Model: "gpt-4"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.Backend.Name != "busy" {
		t.Fatalf("expected unique gpt-4 target busy, got %s", target.Backend.Name)
	}

	release()
	target, err = lc.SelectTarget(context.Background(), &RoutingRequest{Model: "gpt-4"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target.Backend.Name != "busy" {
		t.Fatalf("expected first target after counts tie, got %s", target.Backend.Name)
	}
}

func TestModelMatches(t *testing.T) {
	tests := []struct {
		pattern string
		model   string
		want    bool
	}{
		{"gpt-4", "gpt-4", true},
		{"gpt-4", "gpt-3.5", false},
		{"*", "any-model", true},
		{"gpt-*", "gpt-4", true},
		{"gpt-*", "gpt-3.5-turbo", true},
		{"gpt-*", "claude-3", false},
	}

	for _, tt := range tests {
		got := modelMatches(tt.pattern, tt.model)
		if got != tt.want {
			t.Errorf("modelMatches(%q, %q) = %v, want %v", tt.pattern, tt.model, got, tt.want)
		}
	}
}
