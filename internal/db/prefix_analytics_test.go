package db

import (
	"testing"
	"time"
)

func logPrefix(t *testing.T, d *DB, backendID int64, cause string, promptTokens, cached int) {
	t.Helper()
	if _, err := d.Logs.Create(LogInput{
		BackendID: &backendID, Model: "qwen-next", Endpoint: "/v1/chat/completions",
		RequestTokens: promptTokens, TotalTokens: promptTokens, StatusCode: 200,
		CachedTokens: cached, RouterName: "qwen-next-router",
		PrefixCause: cause, PrefixRetained: 0.5, PrefixMessages: 10,
	}); err != nil {
		t.Fatalf("log: %v", err)
	}
}

func TestPrefixAnalyticsRanksCausesByCostNotCount(t *testing.T) {
	d, hostA, _ := statsDB(t)
	// Many cheap tail rewrites against one very expensive system-prompt rewrite.
	for i := 0; i < 40; i++ {
		logPrefix(t, d, hostA, "tail", 1000, 900)
	}
	logPrefix(t, d, hostA, "system", 180000, 0)

	s, err := d.PrefixAnalytics(time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}
	if len(s.Causes) == 0 {
		t.Fatal("no causes reported")
	}
	if s.Causes[0].Cause != "system" {
		t.Errorf("top cause = %q, want system; ranking must follow cost, not request "+
			"count (40 tail rewrites cost 4k tokens, one system rewrite costs 180k)",
			s.Causes[0].Cause)
	}
	if s.Faults != 41 {
		t.Errorf("faults = %d, want 41", s.Faults)
	}
	wantSeconds := 180000 / prefillTokensPerSecond
	if got := s.Causes[0].EstimatedSeconds; got < wantSeconds*0.99 || got > wantSeconds*1.01 {
		t.Errorf("estimated seconds = %.1f, want ~%.1f", got, wantSeconds)
	}
}

// Healthy appends must not be counted as faults, or the view cries wolf on every
// well-behaved client.
func TestPrefixAnalyticsDoesNotBlameAppends(t *testing.T) {
	d, hostA, _ := statsDB(t)
	for i := 0; i < 20; i++ {
		logPrefix(t, d, hostA, "append", 50000, 49000)
	}
	s, err := d.PrefixAnalytics(time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}
	if s.Faults != 0 {
		t.Errorf("faults = %d, want 0", s.Faults)
	}
	if s.ReprocessedTokens != 0 {
		t.Errorf("reprocessed = %d, want 0", s.ReprocessedTokens)
	}
	if s.Diagnosed != 20 {
		t.Errorf("diagnosed = %d, want 20", s.Diagnosed)
	}
}

// Rows predating the instrumentation carry no cause. Counting them would understate
// every share - the same bug that once made a healthy host look broken.
func TestPrefixAnalyticsIgnoresUndiagnosedRows(t *testing.T) {
	d, hostA, _ := statsDB(t)
	logIt(t, d, hostA, "sticky_hit", 5000, 4000, 0, 100) // no prefix cause
	logPrefix(t, d, hostA, "system", 10000, 0)

	s, err := d.PrefixAnalytics(time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}
	if s.Diagnosed != 1 {
		t.Errorf("diagnosed = %d, want 1; undiagnosed rows must be excluded", s.Diagnosed)
	}
	if s.Causes[0].Share != 1 {
		t.Errorf("share = %.2f, want 1", s.Causes[0].Share)
	}
}

func TestPrefixAnalyticsNamesOffenders(t *testing.T) {
	d, hostA, _ := statsDB(t)
	logPrefix(t, d, hostA, "system", 120000, 0)
	logPrefix(t, d, hostA, "tail", 1000, 900)

	s, err := d.PrefixAnalytics(time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}
	if len(s.Offenders) == 0 {
		t.Fatal("expected offenders")
	}
	if s.Offenders[0].Cause != "system" || s.Offenders[0].Model != "qwen-next" {
		t.Errorf("top offender = %+v, want the system rewrite on qwen-next", s.Offenders[0])
	}
}

func TestPrefixAnalyticsEmptyWindow(t *testing.T) {
	d, _, _ := statsDB(t)
	s, err := d.PrefixAnalytics(time.Now().Add(-time.Hour), time.Now())
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}
	if s.Diagnosed != 0 || len(s.Causes) != 0 {
		t.Errorf("expected an empty summary, got %+v", s)
	}
	// JSON-friendly empties, so the UI never has to guard against null.
	if s.Causes == nil || s.Offenders == nil {
		t.Error("slices must be non-nil for the API response")
	}
}

func TestPrefixTimeSeriesBucketsAndFillsGaps(t *testing.T) {
	d, hostA, _ := statsDB(t)
	logPrefix(t, d, hostA, "system", 90000, 0)
	logPrefix(t, d, hostA, "append", 90000, 89000)

	s, err := d.PrefixTimeSeries(time.Now().Add(-3*time.Hour), time.Now(), "hour")
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(s.Points) < 3 {
		t.Fatalf("expected empty buckets to be emitted, got %d points", len(s.Points))
	}
	var system, appends int64
	for _, p := range s.Points {
		system += p.System
		appends += p.Append
	}
	if system != 1 || appends != 1 {
		t.Errorf("system=%d append=%d, want 1 and 1", system, appends)
	}
	last := s.Points[len(s.Points)-1]
	if last.Faults != 1 || last.FaultRate != 0.5 {
		t.Errorf("last bucket faults=%d rate=%.2f, want 1 and 0.50", last.Faults, last.FaultRate)
	}
}
