package db

import (
	"path/filepath"
	"testing"
	"time"
)

func seriesDB(t *testing.T) (*DB, int64) {
	t.Helper()
	d, err := New(filepath.Join(t.TempDir(), "series.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	b, err := d.Backends.Create(BackendInput{
		Name: "h", BaseURL: "http://127.0.0.1:1", Weight: 1, MaxConcurrent: 4,
	})
	if err != nil {
		t.Fatalf("backend: %v", err)
	}
	return d, b.ID
}

func logAt(t *testing.T, d *DB, backendID int64, at time.Time, decision string,
	prompt, cached, saved, latency int) {
	t.Helper()
	id, err := d.Logs.Create(LogInput{
		BackendID: &backendID, Model: "m", Endpoint: "/v1/chat/completions",
		RequestTokens: prompt, TotalTokens: prompt, StatusCode: 200, LatencyMs: latency,
		RoutingDecision: decision, CachedTokens: cached, CompressedBytes: saved,
	})
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if _, err := d.conn.Exec("UPDATE request_logs SET created_at = ? WHERE id = ?", at, id); err != nil {
		t.Fatalf("backdate: %v", err)
	}
}

// Buckets must separate by time and compute rates per bucket, so a change in
// configuration shows up as a change in the series rather than being averaged away.
func TestRoutingTimeSeriesBucketsByHour(t *testing.T) {
	d, host := seriesDB(t)
	base := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Hour)

	// Hour 1: everything warm.
	for i := 0; i < 4; i++ {
		logAt(t, d, host, base.Add(time.Duration(i)*time.Minute), "sticky_hit", 40000, 39500, 0, 3000)
	}
	// Hour 2: two deep requests arrive cold - the expensive case.
	for i := 0; i < 2; i++ {
		logAt(t, d, host, base.Add(time.Hour+time.Duration(i)*time.Minute), "fresh", 40000, 0, 0, 80000)
	}
	logAt(t, d, host, base.Add(time.Hour+5*time.Minute), "sticky_hit", 40000, 39000, 4096, 3000)

	got, err := d.RoutingTimeSeries(base.Add(-time.Minute), time.Now(), "hour")
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if got.Interval != "hour" {
		t.Fatalf("interval = %q, want hour", got.Interval)
	}

	byTime := map[time.Time]RoutingPoint{}
	for _, p := range got.Points {
		byTime[p.Time] = p
	}
	h1, h2 := byTime[base], byTime[base.Add(time.Hour)]

	if h1.Requests != 4 {
		t.Errorf("hour 1 requests = %d, want 4", h1.Requests)
	}
	if h1.ColdRequests != 0 {
		t.Errorf("hour 1 should have no cold arrivals, got %d", h1.ColdRequests)
	}
	if h1.CacheReuseRate < 0.95 {
		t.Errorf("hour 1 reuse = %.3f, want >0.95", h1.CacheReuseRate)
	}

	if h2.ColdRequests != 2 {
		t.Errorf("hour 2 cold = %d, want 2", h2.ColdRequests)
	}
	if h2.DeepRequests != 3 {
		t.Errorf("hour 2 deep = %d, want 3", h2.DeepRequests)
	}
	if want := 2.0 / 3.0; h2.ColdRate < want-0.01 || h2.ColdRate > want+0.01 {
		t.Errorf("hour 2 cold rate = %.3f, want ~%.3f", h2.ColdRate, want)
	}
	if h2.ReprocessedTokens != 81000 {
		t.Errorf("hour 2 reprocessed = %d, want 81000", h2.ReprocessedTokens)
	}
	if h2.BytesSaved != 4096 || h2.CompressedRequests != 1 {
		t.Errorf("hour 2 compression = %d bytes over %d requests, want 4096/1",
			h2.BytesSaved, h2.CompressedRequests)
	}
}

// Empty buckets must still appear, or a gap in traffic renders as a straight line
// between distant points and hides an outage.
func TestRoutingTimeSeriesFillsEmptyBuckets(t *testing.T) {
	d, host := seriesDB(t)
	base := time.Now().UTC().Add(-4 * time.Hour).Truncate(time.Hour)
	logAt(t, d, host, base, "sticky_hit", 10000, 9900, 0, 1000)

	got, err := d.RoutingTimeSeries(base, time.Now(), "hour")
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(got.Points) < 4 {
		t.Fatalf("expected a point per hour, got %d", len(got.Points))
	}
	empty := 0
	for _, p := range got.Points {
		if p.Requests == 0 {
			empty++
		}
	}
	if empty == 0 {
		t.Error("expected empty buckets to be represented")
	}
}

// Shallow prompts must not count as cold: a 200-token prefill is free, and counting
// it would swamp the signal from the deep requests that actually cost time.
func TestRoutingTimeSeriesIgnoresShallowColdRequests(t *testing.T) {
	d, host := seriesDB(t)
	now := time.Now().UTC().Add(-30 * time.Minute)
	for i := 0; i < 20; i++ {
		logAt(t, d, host, now, "fresh", 150, 0, 0, 300) // shallow and cold
	}
	logAt(t, d, host, now, "fresh", 40000, 0, 0, 80000) // deep and cold

	got, err := d.RoutingTimeSeries(now.Add(-time.Hour), time.Now(), "hour")
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	var cold, deep int64
	for _, p := range got.Points {
		cold += p.ColdRequests
		deep += p.DeepRequests
	}
	if deep != 1 || cold != 1 {
		t.Errorf("deep=%d cold=%d, want 1/1 - shallow requests must not count", deep, cold)
	}
}

// Rows predating instrumentation are counted as traffic but excluded from rates.
func TestRoutingTimeSeriesExcludesUnmeasuredFromRates(t *testing.T) {
	d, host := seriesDB(t)
	now := time.Now().UTC().Add(-30 * time.Minute)
	logAt(t, d, host, now, "sticky_hit", 10000, 9900, 0, 1000)
	for i := 0; i < 10; i++ {
		logAt(t, d, host, now, "", 10000, 0, 0, 1000) // legacy rows
	}
	got, err := d.RoutingTimeSeries(now.Add(-time.Hour), time.Now(), "hour")
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	var reqs, measured int64
	var reuse float64
	for _, p := range got.Points {
		reqs += p.Requests
		measured += p.Measured
		if p.CacheReuseRate > reuse {
			reuse = p.CacheReuseRate
		}
	}
	if reqs != 11 {
		t.Errorf("requests = %d, want 11 (legacy rows are still traffic)", reqs)
	}
	if measured != 1 {
		t.Errorf("measured = %d, want 1", measured)
	}
	if reuse < 0.95 {
		t.Errorf("legacy rows dragged reuse to %.3f; expected ~0.99", reuse)
	}
}

func TestNormalizeRoutingInterval(t *testing.T) {
	cases := []struct {
		in   string
		span time.Duration
		want string
	}{
		{"minute", time.Hour, "minute"},
		{"day", time.Hour, "day"},
		{"", 2 * time.Hour, "minute"},
		{"", 48 * time.Hour, "hour"},
		{"", 30 * 24 * time.Hour, "day"},
		{"nonsense", 48 * time.Hour, "hour"},
	}
	for _, c := range cases {
		if got := normalizeRoutingInterval(c.in, c.span); got != c.want {
			t.Errorf("normalizeRoutingInterval(%q, %v) = %q, want %q", c.in, c.span, got, c.want)
		}
	}
}
