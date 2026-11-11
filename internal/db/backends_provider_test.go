package db

import "testing"

func TestLegacyBackendDefaultsToLocalOpenAICompatible(t *testing.T) {
	d := setupTestDB(t)

	backend, err := d.Backends.Create(BackendInput{
		Name: "existing-halogen", BaseURL: "http://127.0.0.1:18088", Weight: 1, MaxConcurrent: 2,
	})
	if err != nil {
		t.Fatalf("create legacy-shaped backend: %v", err)
	}
	if backend.Provider != BackendProviderOpenAICompatible {
		t.Fatalf("provider = %q, want legacy default %q", backend.Provider, BackendProviderOpenAICompatible)
	}
	if backend.Origin != BackendOriginLocal {
		t.Fatalf("origin = %q, want legacy default %q", backend.Origin, BackendOriginLocal)
	}
}

func TestExternalBackendProviderAndOriginPersist(t *testing.T) {
	d := setupTestDB(t)

	backend, err := d.Backends.Create(BackendInput{
		Name: "openai-fallback", BaseURL: "https://api.openai.com", Weight: 1, MaxConcurrent: 1,
		Provider: BackendProviderOpenAI, Origin: BackendOriginExternal,
	})
	if err != nil {
		t.Fatalf("create external backend: %v", err)
	}
	if backend.Provider != BackendProviderOpenAI || backend.Origin != BackendOriginExternal {
		t.Fatalf("created backend = provider %q origin %q, want openai/external", backend.Provider, backend.Origin)
	}

	got, err := d.Backends.Get(backend.ID)
	if err != nil {
		t.Fatalf("reload external backend: %v", err)
	}
	if got.Provider != BackendProviderOpenAI || got.Origin != BackendOriginExternal {
		t.Fatalf("reloaded backend = provider %q origin %q, want openai/external", got.Provider, got.Origin)
	}
}

func TestBackendUpdateWithoutProviderOrOriginPreservesClassification(t *testing.T) {
	d := setupTestDB(t)

	backend, err := d.Backends.Create(BackendInput{
		Name: "openai-fallback", BaseURL: "https://api.openai.com", Weight: 1, MaxConcurrent: 1,
		Provider: BackendProviderOpenAI, Origin: BackendOriginExternal,
	})
	if err != nil {
		t.Fatalf("create external backend: %v", err)
	}

	updated, err := d.Backends.Update(backend.ID, BackendInput{
		Name: "openai-fallback", BaseURL: "https://api.openai.com", Weight: 2, MaxConcurrent: 3,
	})
	if err != nil {
		t.Fatalf("update without classification: %v", err)
	}
	if updated.Provider != BackendProviderOpenAI || updated.Origin != BackendOriginExternal {
		t.Fatalf("classification was clobbered: provider %q origin %q", updated.Provider, updated.Origin)
	}
}
