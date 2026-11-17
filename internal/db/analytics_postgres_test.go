package db

import (
	"os"
	"reflect"
	"testing"
	"time"
)

// UsageAnalytics pre-aggregates in SQL with a dialect-specific slot expression, so
// the Postgres path needs its own check. It runs only when a disposable database
// is provided, e.g.:
//
//	docker run --rm -d -p 55432:5432 -e POSTGRES_PASSWORD=t postgres:16
//	RELAI_TEST_POSTGRES_DSN='postgres://postgres:t@127.0.0.1:55432/postgres?sslmode=disable' \
//	  go test ./internal/db -run Postgres
//
// The test empties request_logs in that database.
func TestUsageAnalyticsPostgresMatchesSQLite(t *testing.T) {
	dsn := os.Getenv("RELAI_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("RELAI_TEST_POSTGRES_DSN not set")
	}

	lite, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer lite.Close()
	pg, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	if err := pg.UsePostgresLogs(dsn); err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	if _, err := pg.Logs.db.Exec(`TRUNCATE request_logs`); err != nil {
		t.Fatal(err)
	}

	// Rows spread over three days, including either side of slot, hour and UTC-day
	// boundaries, so any disagreement in slot arithmetic shows up in the buckets.
	base := time.Date(2026, 3, 10, 23, 44, 59, 0, time.UTC)
	offsets := []time.Duration{0, time.Second, 15 * time.Minute, 16 * time.Minute, 75 * time.Minute, 26 * time.Hour, 49*time.Hour + 30*time.Minute}
	models := []string{"a", "b", "a", "c", "b", "a", "c"}
	for i, off := range offsets {
		for _, d := range []*DB{lite, pg} {
			at := base.Add(off)
			status := 200
			if i%3 == 0 {
				status = 502
			}
			_, err := d.Logs.db.Exec(`INSERT INTO request_logs (model, endpoint, total_tokens, cost, status_code, created_at)
				VALUES (`+d.Logs.placeholders(6)+`)`,
				models[i], "/v1/chat/completions", 100*(i+1), 0.01*float64(i+1), status, d.Logs.timeParam(at))
			if err != nil {
				t.Fatalf("insert: %v", err)
			}
		}
	}

	for _, interval := range []string{"hour", "day", "week"} {
		opts := AnalyticsOptions{Start: base.Add(-time.Hour), End: base.Add(72 * time.Hour), GroupBy: "model", Interval: interval}
		want, err := lite.UsageAnalytics(opts)
		if err != nil {
			t.Fatal(err)
		}
		got, err := pg.UsageAnalytics(opts)
		if err != nil {
			t.Fatalf("%s: postgres: %v", interval, err)
		}
		if want.Summary.Requests != len(offsets) {
			t.Fatalf("%s: sqlite counted %d requests, want %d", interval, want.Summary.Requests, len(offsets))
		}
		if !reflect.DeepEqual(roundCosts(want), roundCosts(got)) {
			t.Errorf("%s: postgres and sqlite disagree\nsqlite:   %+v\npostgres: %+v", interval, want, got)
		}
	}
}

// roundCosts strips float noise so the two stores' sums compare equal.
func roundCosts(r *AnalyticsResult) *AnalyticsResult {
	round := func(v float64) float64 { return float64(int64(v*1e9+0.5)) / 1e9 }
	out := *r
	out.Summary.Cost = round(r.Summary.Cost)
	out.Groups = append([]AnalyticsGroup(nil), r.Groups...)
	for i := range out.Groups {
		out.Groups[i].Cost = round(out.Groups[i].Cost)
	}
	out.Buckets = make([]AnalyticsBucket, len(r.Buckets))
	for i, b := range r.Buckets {
		nb := AnalyticsBucket{Time: b.Time.UTC(), Groups: map[string]AnalyticsCell{}}
		for k, c := range b.Groups {
			c.Cost = round(c.Cost)
			nb.Groups[k] = c
		}
		out.Buckets[i] = nb
	}
	out.Start, out.End = r.Start.UTC(), r.End.UTC()
	return &out
}
