package vrouter

import (
	"testing"

	"github.com/bfenty/relai/internal/db"
)

// #18: shouldSpillImmediately compares the estimated re-prefill cost of a
// conversation against spillImmediateBudget, preferring the persisted
// footprint (#19) over the raw prompt size when present.

func newFootprintDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.New(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestShouldSpillImmediatelySmallFootprintSpills(t *testing.T) {
	d := newFootprintDB(t)
	m := NewManager(d, nil, nil)

	// ~3k tokens (12 KB) → ~15s at the conservative rate: at the boundary, spills.
	if !m.shouldSpillImmediately(1, "sess-a", 12_000) {
		t.Fatalf("a 12KB prompt should spill immediately")
	}
	// A whale (2 MB ≈ 500k tokens → 40 min) must never spill immediately.
	if m.shouldSpillImmediately(1, "sess-b", 2_000_000) {
		t.Fatalf("a whale prompt must not spill immediately")
	}
}

func TestShouldSpillImmediatelyUsesPersistedFootprint(t *testing.T) {
	d := newFootprintDB(t)
	m := NewManager(d, nil, nil)

	b, err := d.Backends.Create(db.BackendInput{Name: "f", BaseURL: "http://x", Weight: 1, MaxConcurrent: 1})
	if err != nil {
		t.Fatalf("backend: %v", err)
	}
	mm, err := d.ModelMaps.Create(db.ModelMapInput{BackendID: b.ID, LocalModel: "m", RemoteModel: "m"})
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	r, err := d.Routers.Create(db.RouterInput{Name: "vr", Strategy: "sticky"})
	if err != nil {
		t.Fatalf("router: %v", err)
	}

	// Persist a whale footprint for this session; even a small current prompt
	// must not spill — the warm host is holding a huge cache worth protecting.
	hash := persistentSessionKey("sess-whale")
	if err := d.SessionFootprints.Upsert(r.ID, hash, mm.ID, 2_000_000); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if m.shouldSpillImmediately(r.ID, "sess-whale", 1_000) {
		t.Fatalf("persisted whale footprint must keep sticky wait")
	}

	// A small persisted footprint with a large current prompt spills: the
	// footprint (what the warm host actually holds) beats the raw prompt size.
	if err := d.SessionFootprints.Upsert(r.ID, persistentSessionKey("sess-small"), mm.ID, 4_000); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if !m.shouldSpillImmediately(r.ID, "sess-small", 2_000_000) {
		t.Fatalf("small persisted footprint should spill despite large prompt")
	}
}
