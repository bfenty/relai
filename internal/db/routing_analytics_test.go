package db

import (
	"path/filepath"
	"testing"
	"time"
)

func statsDB(t *testing.T) (*DB, int64, int64) {
	t.Helper()
	d, err := New(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	a, err := d.Backends.Create(BackendInput{Name: "host-a", BaseURL: "http://a:1", Weight: 1, MaxConcurrent: 4})
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, err := d.Backends.Create(BackendInput{Name: "host-b", BaseURL: "http://b:1", Weight: 1, MaxConcurrent: 4})
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	return d, a.ID, b.ID
}

func logIt(t *testing.T, d *DB, backendID int64, decision string, promptTokens, cached, waitMs, latency int) {
	t.Helper()
	if _, err := d.Logs.Create(LogInput{
		BackendID: &backendID, Model: "m", Endpoint: "/v1/chat/completions",
		RequestTokens: promptTokens, TotalTokens: promptTokens, StatusCode: 200,
		LatencyMs: latency, RoutingDecision: decision, CachedTokens: cached, WaitMs: waitMs,
		RouterName: "qwen-next-router", SessionKey: "s:abc",
	}); err != nil {
		t.Fatalf("log: %v", err)
	}
}

func TestRoutingAnalyticsSummarisesAffinity(t *testing.T) {
	d, hostA, hostB := statsDB(t)

	// Three conversations pinned and warm on host A...
	for i := 0; i < 3; i++ {
		logIt(t, d, hostA, "sticky_hit", 40000, 39500, 0, 3000)
	}
	// ...one that had to wait for its host but kept the cache...
	logIt(t, d, hostA, "sticky_wait", 40000, 39000, 12000, 15000)
	// ...one that gave up and paid a full reprocess on the other host...
	logIt(t, d, hostB, "sticky_spill", 40000, 0, 3000, 210000)
	// ...and one brand new conversation placed by load.
	logIt(t, d, hostB, "fresh", 2000, 0, 0, 4000)

	got, err := d.RoutingAnalytics(time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("RoutingAnalytics: %v", err)
	}

	if got.Total != 6 {
		t.Errorf("total = %d, want 6", got.Total)
	}
	if got.Decisions["sticky_hit"] != 3 {
		t.Errorf("sticky_hit = %d, want 3", got.Decisions["sticky_hit"])
	}
	// 4 of 6 routed requests kept their host (3 hits + 1 wait).
	if want := 4.0 / 6.0; got.StickyHitRate < want-0.01 || got.StickyHitRate > want+0.01 {
		t.Errorf("sticky hit rate = %.3f, want ~%.3f", got.StickyHitRate, want)
	}
	if want := 1.0 / 6.0; got.SpillRate < want-0.01 || got.SpillRate > want+0.01 {
		t.Errorf("spill rate = %.3f, want ~%.3f", got.SpillRate, want)
	}
	// Prompt tokens the upstreams had to prefill: 3x500 from the warm hits, 1000 from
	// the one that waited, the spill's whole 40000, and the fresh conversation's 2000.
	const wantReprocessed = 3*500 + 1000 + 40000 + 2000
	if got.ReprocessedTokens != wantReprocessed {
		t.Errorf("reprocessed = %d, want %d", got.ReprocessedTokens, wantReprocessed)
	}
	if got.CacheReuseRate <= 0.7 {
		t.Errorf("cache reuse rate = %.3f, expected most of the prompt to be cached", got.CacheReuseRate)
	}
	if got.MeanWaitMs <= 0 {
		t.Error("expected a non-zero mean wait")
	}

	if len(got.Hosts) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(got.Hosts))
	}
	byName := map[string]HostRoutingStats{}
	for _, h := range got.Hosts {
		byName[h.BackendName] = h
	}
	a, b := byName["host-a"], byName["host-b"]
	if a.Requests != 4 || b.Requests != 2 {
		t.Errorf("host split = a:%d b:%d, want a:4 b:2", a.Requests, b.Requests)
	}
	if a.CacheReuseRate <= b.CacheReuseRate {
		t.Errorf("the warm host should show better cache reuse: a=%.3f b=%.3f",
			a.CacheReuseRate, b.CacheReuseRate)
	}
	if b.MeanLatencyMs <= a.MeanLatencyMs {
		t.Errorf("the reprocessing host should be slower: a=%d b=%d", a.MeanLatencyMs, b.MeanLatencyMs)
	}
}

// An empty window must not divide by zero or invent numbers.
func TestRoutingAnalyticsEmptyWindow(t *testing.T) {
	d, _, _ := statsDB(t)
	got, err := d.RoutingAnalytics(time.Now().Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatalf("RoutingAnalytics: %v", err)
	}
	if got.Total != 0 || got.StickyHitRate != 0 || got.CacheReuseRate != 0 {
		t.Errorf("expected zeroes for an empty window, got %+v", got)
	}
}

// Requests that predate the feature carry no decision and must be reported as such
// rather than silently counted as hits.
func TestRoutingAnalyticsCountsUnrecorded(t *testing.T) {
	d, hostA, _ := statsDB(t)
	logIt(t, d, hostA, "", 1000, 0, 0, 500)
	got, err := d.RoutingAnalytics(time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("RoutingAnalytics: %v", err)
	}
	if got.Decisions["unrecorded"] != 1 {
		t.Errorf("expected 1 unrecorded, got %v", got.Decisions)
	}
	if got.StickyHitRate != 0 {
		t.Errorf("unrecorded requests must not count as sticky hits, got %.3f", got.StickyHitRate)
	}
	if got.UnmeasuredRequests != 1 || got.MeasuredRequests != 0 {
		t.Errorf("unmeasured=%d measured=%d, want 1/0", got.UnmeasuredRequests, got.MeasuredRequests)
	}
}

// Rows predating the routing columns have request_tokens but no cached_tokens. Counting
// their prompts would drag cache reuse toward zero and make a healthy host look broken -
// which is exactly what it did on first deployment.
func TestRoutingAnalyticsExcludesPreDeployRowsFromRates(t *testing.T) {
	d, hostA, _ := statsDB(t)
	// One well-cached measured request...
	logIt(t, d, hostA, "sticky_hit", 10000, 9800, 0, 1000)
	// ...and fifty legacy rows with prompts but no cache data.
	for i := 0; i < 50; i++ {
		logIt(t, d, hostA, "", 10000, 0, 0, 1000)
	}
	got, err := d.RoutingAnalytics(time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("RoutingAnalytics: %v", err)
	}
	if got.CacheReuseRate < 0.9 {
		t.Errorf("legacy rows dragged cache reuse to %.3f; expected ~0.98 from the measured row", got.CacheReuseRate)
	}
	if got.MeasuredRequests != 1 || got.UnmeasuredRequests != 50 {
		t.Errorf("measured=%d unmeasured=%d, want 1/50", got.MeasuredRequests, got.UnmeasuredRequests)
	}
	if len(got.Hosts) != 1 {
		t.Fatalf("want 1 host, got %d", len(got.Hosts))
	}
	if got.Hosts[0].CacheReuseRate < 0.9 {
		t.Errorf("per-host reuse also skewed: %.3f", got.Hosts[0].CacheReuseRate)
	}
}

// Compression savings are reported alongside cache reuse, with the number of
// requests actually compressed so the figure stays honest when only some targets
// have it enabled.
func TestRoutingAnalyticsReportsCompression(t *testing.T) {
	d, hostA, hostB := statsDB(t)
	compressed := func(backendID int64, saved int) {
		t.Helper()
		if _, err := d.Logs.Create(LogInput{
			BackendID: &backendID, Model: "m", Endpoint: "/v1/chat/completions",
			RequestTokens: 10000, TotalTokens: 10000, StatusCode: 200, LatencyMs: 1000,
			RoutingDecision: "sticky_hit", CachedTokens: 9000, CompressedBytes: saved,
		}); err != nil {
			t.Fatalf("log: %v", err)
		}
	}
	compressed(hostA, 4096)
	compressed(hostA, 2048)
	compressed(hostB, 0) // this target does not compress

	got, err := d.RoutingAnalytics(time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("RoutingAnalytics: %v", err)
	}
	if got.BytesSaved != 6144 {
		t.Errorf("bytes saved = %d, want 6144", got.BytesSaved)
	}
	if got.CompressedRequests != 2 {
		t.Errorf("compressed requests = %d, want 2 (the third target compresses nothing)",
			got.CompressedRequests)
	}
	byName := map[string]HostRoutingStats{}
	for _, h := range got.Hosts {
		byName[h.BackendName] = h
	}
	if byName["host-a"].BytesSaved != 6144 {
		t.Errorf("host-a saved = %d, want 6144", byName["host-a"].BytesSaved)
	}
	if byName["host-b"].BytesSaved != 0 {
		t.Errorf("host-b saved = %d, want 0", byName["host-b"].BytesSaved)
	}
}
