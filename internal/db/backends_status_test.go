package db

import "testing"

// An update that omits Status (the host edit form has no status field) must not
// blank the stored status. A blank status drops the host out of ListActive() and
// silently removes it from routing - observed as a host going "offline" right
// after changing its max-concurrent setting.
func TestBackendUpdateWithoutStatusPreservesIt(t *testing.T) {
	d := setupTestDB(t)

	b, err := d.Backends.Create(BackendInput{
		Name: "host-a", BaseURL: "http://10.0.0.5:18088", Weight: 1, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if b.Status != "active" {
		t.Fatalf("expected new backend to default to active, got %q", b.Status)
	}

	// Exactly what the host edit form sends when setting concurrency to 4: no Status.
	updated, err := d.Backends.Update(b.ID, BackendInput{
		Name: "host-a", BaseURL: "http://10.0.0.5:18088", Weight: 1, MaxConcurrent: 4,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.MaxConcurrent != 4 {
		t.Errorf("expected max_concurrent 4, got %d", updated.MaxConcurrent)
	}
	if updated.Status != "active" {
		t.Errorf("status was clobbered by an update that omitted it: got %q, want active", updated.Status)
	}

	active, err := d.Backends.ListActive()
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("backend dropped out of ListActive after a concurrency change: got %d active", len(active))
	}
}

// An update that does carry Status must still apply it (the pause/activate toggle).
func TestBackendUpdateWithStatusStillApplies(t *testing.T) {
	d := setupTestDB(t)

	b, err := d.Backends.Create(BackendInput{
		Name: "host-b", BaseURL: "http://localhost:9999", Weight: 1, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	paused, err := d.Backends.Update(b.ID, BackendInput{
		Name: "host-b", BaseURL: "http://localhost:9999", Weight: 1, MaxConcurrent: 1, Status: "paused",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if paused.Status != "paused" {
		t.Errorf("expected explicit status to apply: got %q, want paused", paused.Status)
	}

	// And a subsequent status-less edit must keep it paused, not silently reactivate it.
	stillPaused, err := d.Backends.Update(b.ID, BackendInput{
		Name: "host-b", BaseURL: "http://localhost:9999", Weight: 1, MaxConcurrent: 8,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if stillPaused.Status != "paused" {
		t.Errorf("status-less edit reactivated a paused host: got %q, want paused", stillPaused.Status)
	}
}
