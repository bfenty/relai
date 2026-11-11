package db

import (
	"testing"
	"time"
)

// A request logged "now" must be counted by every dashboard range and land in
// a usage-series bucket. SQLite stores created_at as a UTC string, so this
// guards the UTC/local conversion of the query bounds (the host may run in
// any timezone).
func TestDashboardStatsCountsFreshLogInAllRanges(t *testing.T) {
	d, err := New(":memory:")
	if err != nil {
		t.Fatalf("db.New failed: %v", err)
	}
	defer d.Close()

	if _, err := d.Logs.Create(LogInput{
		Model: "m", Endpoint: "/v1/chat/completions",
		TotalTokens: 10, ResponseTokens: 5, Cost: 0.01, StatusCode: 200, LatencyMs: 100,
	}); err != nil {
		t.Fatalf("create log: %v", err)
	}

	cases := []struct {
		rangeStr string
		buckets  int
		interval string
	}{
		{"today", 24, "hour"},
		{"week", 7, "day"},
		{"month", 30, "day"},
	}
	for _, tc := range cases {
		stats, err := d.Logs.DashboardStats(tc.rangeStr)
		if err != nil {
			t.Fatalf("DashboardStats(%q): %v", tc.rangeStr, err)
		}
		if stats.TotalRequestsToday != 1 {
			t.Errorf("range %q: TotalRequestsToday = %d, want 1", tc.rangeStr, stats.TotalRequestsToday)
		}
		if len(stats.UsageSeries) != tc.buckets {
			t.Errorf("range %q: %d buckets, want %d", tc.rangeStr, len(stats.UsageSeries), tc.buckets)
		}
		if stats.SeriesInterval != tc.interval {
			t.Errorf("range %q: interval %q, want %q", tc.rangeStr, stats.SeriesInterval, tc.interval)
		}
		total := 0
		for _, p := range stats.UsageSeries {
			total += p.Requests
		}
		if total != 1 {
			t.Errorf("range %q: usage series counted %d requests, want 1", tc.rangeStr, total)
		}
	}
}

func TestDashboardStatsTimeBoundsUseUTC(t *testing.T) {
	d, err := New(":memory:")
	if err != nil {
		t.Fatalf("db.New failed: %v", err)
	}
	defer d.Close()

	// Insert a row timestamped 36h ago (UTC string, matching CURRENT_TIMESTAMP
	// format): it must appear in week/month but not today's counters.
	old := time.Now().UTC().Add(-36 * time.Hour).Format("2006-01-02 15:04:05")
	if _, err := d.conn.Exec(`
		INSERT INTO request_logs (model, endpoint, total_tokens, cost, status_code, latency_ms, created_at)
		VALUES ('m', '/v1/chat/completions', 10, 0.01, 200, 100, ?)`, old); err != nil {
		t.Fatalf("insert old log: %v", err)
	}

	stats, err := d.Logs.DashboardStats("week")
	if err != nil {
		t.Fatalf("DashboardStats: %v", err)
	}
	if stats.TotalRequestsToday != 0 {
		t.Errorf("TotalRequestsToday = %d, want 0 for a 36h-old log", stats.TotalRequestsToday)
	}
	if stats.TotalRequestsWeek != 1 {
		t.Errorf("TotalRequestsWeek = %d, want 1", stats.TotalRequestsWeek)
	}
}
