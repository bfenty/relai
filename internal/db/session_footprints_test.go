package db

import (
	"testing"
	"time"
)

func TestSessionFootprintsUpsertListExpire(t *testing.T) {
	d, err := New(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	b, err := d.Backends.Create(BackendInput{Name: "f", BaseURL: "http://x", Weight: 1, MaxConcurrent: 1})
	if err != nil {
		t.Fatalf("backend: %v", err)
	}
	mm, err := d.ModelMaps.Create(ModelMapInput{BackendID: b.ID, LocalModel: "m", RemoteModel: "m"})
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	r, err := d.Routers.Create(RouterInput{Name: "vr", Strategy: "sticky"})
	if err != nil {
		t.Fatalf("router: %v", err)
	}

	// Upsert two sessions, whale and minnow.
	if err := d.SessionFootprints.Upsert(r.ID, "hash-whale", mm.ID, 5_000_000); err != nil {
		t.Fatalf("upsert whale: %v", err)
	}
	if err := d.SessionFootprints.Upsert(r.ID, "hash-minnow", mm.ID, 2_000); err != nil {
		t.Fatalf("upsert minnow: %v", err)
	}
	// Update refreshes bytes.
	if err := d.SessionFootprints.Upsert(r.ID, "hash-minnow", mm.ID, 3_000); err != nil {
		t.Fatalf("refresh minnow: %v", err)
	}

	list, err := d.SessionFootprints.ListByRouter(r.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2, got %d", len(list))
	}
	// Ascending by retained_bytes: minnow first, refreshed value.
	if list[0].SessionKeyHash != "hash-minnow" || list[0].RetainedBytes != 3_000 {
		t.Fatalf("minnow wrong: %+v", list[0])
	}
	if list[1].SessionKeyHash != "hash-whale" || list[1].RetainedBytes != 5_000_000 {
		t.Fatalf("whale wrong: %+v", list[1])
	}

	// Expire everything older than the future: all gone.
	cutoff := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	if err := d.SessionFootprints.DeleteOlderThan(cutoff); err != nil {
		t.Fatalf("expire: %v", err)
	}
	list, err = d.SessionFootprints.ListByRouter(r.ID)
	if err != nil {
		t.Fatalf("list after expire: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("want 0 after expire, got %d", len(list))
	}
}
