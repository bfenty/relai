package db

import "testing"

func TestRouterAffinitiesUpsertGetAndExpire(t *testing.T) {
	d, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Close()

	backend, err := d.Backends.Create(BackendInput{Name: "b", BaseURL: "http://backend"})
	if err != nil {
		t.Fatalf("create backend: %v", err)
	}
	mapping, err := d.ModelMaps.Create(ModelMapInput{BackendID: backend.ID, LocalModel: "local", RemoteModel: "remote"})
	if err != nil {
		t.Fatalf("create mapping: %v", err)
	}
	router, err := d.Routers.Create(RouterInput{Name: "router"})
	if err != nil {
		t.Fatalf("create router: %v", err)
	}

	if err := d.RouterAffinities.Upsert(router.ID, "hash", mapping.ID); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := d.RouterAffinities.Get(router.ID, "hash")
	if err != nil || got != mapping.ID {
		t.Fatalf("Get = %d, %v; want %d, nil", got, err, mapping.ID)
	}
	if err := d.RouterAffinities.DeleteOlderThan("2999-01-01T00:00:00Z"); err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	got, err = d.RouterAffinities.Get(router.ID, "hash")
	if err != nil || got != 0 {
		t.Fatalf("expired Get = %d, %v; want 0, nil", got, err)
	}
}
