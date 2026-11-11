package db

import "testing"

func TestRouterModelType(t *testing.T) {
	d := setupTestDB(t)

	// Default type is chat.
	rt, err := d.Routers.Create(RouterInput{Name: "chat-router"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rt.ModelType != "chat" {
		t.Fatalf("default model_type = %q, want chat", rt.ModelType)
	}

	// Explicit embedding type round-trips and survives List/Get.
	emb, err := d.Routers.Create(RouterInput{Name: "embed-router", ModelType: "embedding"})
	if err != nil {
		t.Fatalf("create embedding: %v", err)
	}
	if emb.ModelType != "embedding" {
		t.Fatalf("model_type = %q, want embedding", emb.ModelType)
	}
	got, err := d.Routers.Get(emb.ID)
	if err != nil || got.ModelType != "embedding" {
		t.Fatalf("Get model_type = %q (err=%v), want embedding", got.ModelType, err)
	}

	// Unknown types normalize to chat; "embed" normalizes to "embedding".
	bad, _ := d.Routers.Create(RouterInput{Name: "bad-router", ModelType: "nonsense"})
	if bad.ModelType != "chat" {
		t.Fatalf("invalid type = %q, want chat", bad.ModelType)
	}
	alias, _ := d.Routers.Create(RouterInput{Name: "alias-router", ModelType: "embed"})
	if alias.ModelType != "embedding" {
		t.Fatalf("'embed' normalized to %q, want embedding", alias.ModelType)
	}

	// Update changes the type.
	updated, err := d.Routers.Update(rt.ID, RouterInput{Name: "chat-router", ModelType: "rerank"})
	if err != nil || updated.ModelType != "rerank" {
		t.Fatalf("update model_type = %q (err=%v), want rerank", updated.ModelType, err)
	}
}

// Router-level compression is the fallback for mappings that set none, so it has
// to survive a round trip - the struct field existed before the SQL did, which
// made the per-router option silently inert.
func TestRouterCompressionPersists(t *testing.T) {
	d := setupTestDB(t)

	rt, err := d.Routers.Create(RouterInput{Name: "r1", Strategy: "sticky", Compression: "aggressive"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rt.Compression != "aggressive" {
		t.Fatalf("create did not persist compression: %q", rt.Compression)
	}
	got, err := d.Routers.Get(rt.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Compression != "aggressive" {
		t.Errorf("compression lost on read: %q", got.Compression)
	}

	updated, err := d.Routers.Update(rt.ID, RouterInput{Name: "r1", Strategy: "sticky", Compression: "lossless"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Compression != "lossless" {
		t.Errorf("update did not persist compression: %q", updated.Compression)
	}

	// Unknown values normalise to off rather than being stored verbatim.
	off, err := d.Routers.Update(rt.ID, RouterInput{Name: "r1", Strategy: "sticky", Compression: "nonsense"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if off.Compression != "" {
		t.Errorf("unknown mode stored as %q, want empty", off.Compression)
	}
}

// Same for model mappings, which take precedence over the router.
func TestModelMapCompressionPersists(t *testing.T) {
	d := setupTestDB(t)
	b, err := d.Backends.Create(BackendInput{Name: "b", BaseURL: "http://127.0.0.1:1", Weight: 1, MaxConcurrent: 1})
	if err != nil {
		t.Fatalf("backend: %v", err)
	}
	mm, err := d.ModelMaps.Create(ModelMapInput{
		BackendID: b.ID, LocalModel: "lm", RemoteModel: "rm", Compression: "lossless",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if mm.Compression != "lossless" {
		t.Fatalf("create did not persist compression: %q", mm.Compression)
	}
	got, err := d.ModelMaps.Get(mm.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Compression != "lossless" {
		t.Errorf("compression lost on read: %q", got.Compression)
	}
}
