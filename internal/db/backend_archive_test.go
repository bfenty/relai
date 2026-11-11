package db

import (
	"path/filepath"
	"testing"
)

func archiveTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := New(filepath.Join(t.TempDir(), "arch.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// Archiving retires a stale host from the UI and routing while keeping its row, so
// request_logs continue to name the host that served them.
func TestArchiveRetiresHostButKeepsAnalytics(t *testing.T) {
	d := archiveTestDB(t)
	b, err := d.Backends.Create(BackendInput{
		Name: "stale", BaseURL: "http://127.0.0.1:18088", Weight: 1, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := d.Logs.Create(LogInput{
		BackendID: &b.ID, Model: "m", Endpoint: "/v1/chat/completions",
		TotalTokens: 4242, Cost: 1.5, StatusCode: 200,
	}); err != nil {
		t.Fatalf("log: %v", err)
	}

	if err := d.Backends.Archive(b.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}

	// Gone from the hosts list...
	list, _ := d.Backends.List()
	for _, x := range list {
		if x.ID == b.ID {
			t.Error("archived host still appears in List()")
		}
	}
	// ...and from routing...
	active, _ := d.Backends.ListActive()
	for _, x := range active {
		if x.ID == b.ID {
			t.Error("archived host still routable via ListActive()")
		}
	}
	// ...but still retrievable, and still visible when asked for.
	if _, err := d.Backends.Get(b.ID); err != nil {
		t.Errorf("archived host should still exist: %v", err)
	}
	all, _ := d.Backends.ListIncludingArchived()
	found := false
	for _, x := range all {
		if x.ID == b.ID {
			found = true
			if x.ArchivedAt == nil {
				t.Error("archived host has no archived_at timestamp")
			}
		}
	}
	if !found {
		t.Error("archived host missing from ListIncludingArchived()")
	}

	// The analytics row keeps its host attribution.
	var backendID *int64
	var tokens int
	if err := d.conn.QueryRow("SELECT backend_id, total_tokens FROM request_logs").Scan(&backendID, &tokens); err != nil {
		t.Fatalf("read log: %v", err)
	}
	if backendID == nil || *backendID != b.ID {
		t.Error("log lost its host attribution on archive")
	}
	if tokens != 4242 {
		t.Errorf("log lost its metrics: %d", tokens)
	}
}

// An archived host can be brought back, paused so it does not immediately take traffic.
func TestUnarchiveRestoresHostPaused(t *testing.T) {
	d := archiveTestDB(t)
	b, _ := d.Backends.Create(BackendInput{
		Name: "returning", BaseURL: "http://127.0.0.1:18088", Weight: 1, MaxConcurrent: 1,
	})
	if err := d.Backends.Archive(b.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if err := d.Backends.Unarchive(b.ID); err != nil {
		t.Fatalf("unarchive: %v", err)
	}
	after, _ := d.Backends.Get(b.ID)
	if after.ArchivedAt != nil {
		t.Error("archived_at not cleared")
	}
	if after.Status != "paused" {
		t.Errorf("expected paused after unarchive, got %q", after.Status)
	}
	list, _ := d.Backends.List()
	found := false
	for _, x := range list {
		if x.ID == b.ID {
			found = true
		}
	}
	if !found {
		t.Error("unarchived host missing from List()")
	}
}

// Archiving twice is reported rather than silently succeeding.
func TestArchiveTwiceReportsError(t *testing.T) {
	d := archiveTestDB(t)
	b, _ := d.Backends.Create(BackendInput{
		Name: "x", BaseURL: "http://127.0.0.1:1", Weight: 1, MaxConcurrent: 1,
	})
	if err := d.Backends.Archive(b.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if err := d.Backends.Archive(b.ID); err == nil {
		t.Error("expected an error archiving an already-archived host")
	}
}

// Purge still works for a host that has served traffic (the FK would otherwise block it).
func TestPurgeDeletesHostWithRequestLogs(t *testing.T) {
	d := archiveTestDB(t)
	b, _ := d.Backends.Create(BackendInput{
		Name: "purge-me", BaseURL: "http://127.0.0.1:1", Weight: 1, MaxConcurrent: 1,
	})
	d.ModelMaps.Create(ModelMapInput{BackendID: b.ID, LocalModel: "pm", RemoteModel: "r"})
	d.Logs.Create(LogInput{BackendID: &b.ID, Model: "pm", Endpoint: "/v1/chat/completions",
		TotalTokens: 7, StatusCode: 200})

	if err := d.Backends.Delete(b.ID); err != nil {
		t.Fatalf("purge failed: %v", err)
	}
	if _, err := d.Backends.Get(b.ID); err == nil {
		t.Error("backend still present after purge")
	}
	var n, dangling int
	d.conn.QueryRow("SELECT COUNT(*) FROM request_logs").Scan(&n)
	d.conn.QueryRow("SELECT COUNT(*) FROM request_logs WHERE backend_id IS NOT NULL").Scan(&dangling)
	if n != 1 {
		t.Errorf("expected the log row to survive purge, got %d", n)
	}
	if dangling != 0 {
		t.Errorf("%d logs still reference the purged host", dangling)
	}
}
