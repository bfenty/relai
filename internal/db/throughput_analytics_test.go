package db

import (
	"path/filepath"
	"testing"
	"time"
)

func throughputDB(t *testing.T) (*DB, int64) {
	t.Helper()
	d, err := New(filepath.Join(t.TempDir(), "tp.db"))
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

// Rates must be token-weighted per bucket: a 40k-token prefill at 1000 tok/s and
// a 1k-token prefill at 100 tok/s average to ~998 tok/s, not 550. Rows without
// phase timing count as requests only and never dilute the rate toward zero.
func TestThroughputTimeSeriesWeightsByTokens(t *testing.T) {
	d, host := throughputDB(t)
	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Hour)

	logTP := func(at time.Time, prompt, completion, prefillMs, decodeMs int) {
		id, err := d.Logs.Create(LogInput{
			BackendID: &host, Model: "m", Endpoint: "/v1/chat/completions",
			RequestTokens: prompt, ResponseTokens: completion, TotalTokens: prompt + completion,
			StatusCode: 200, LatencyMs: prefillMs + decodeMs,
			PrefillMs: prefillMs, DecodeMs: decodeMs,
		})
		if err != nil {
			t.Fatalf("log: %v", err)
		}
		if _, err := d.conn.Exec("UPDATE request_logs SET created_at = ? WHERE id = ?", at, id); err != nil {
			t.Fatalf("backdate: %v", err)
		}
	}

	// Hour 1: one big prefill (40000 tok / 40000 ms = 1000 tok/s) and one small
	// slow one (1000 tok / 10000 ms = 100 tok/s), plus an uninstrumented row.
	logTP(base.Add(time.Minute), 40000, 200, 40000, 10000) // 20 tok/s decode
	logTP(base.Add(2*time.Minute), 1000, 100, 10000, 10000)
	logTP(base.Add(3*time.Minute), 500, 50, 0, 0)

	got, err := d.ThroughputTimeSeries(base.Add(-time.Minute), time.Now(), "hour")
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	var p *ThroughputPoint
	for i := range got.Points {
		if !got.Points[i].Time.Before(base) && got.Points[i].Requests > 0 {
			p = &got.Points[i]
			break
		}
	}
	if p == nil {
		t.Fatal("no bucket with requests")
	}
	if p.Requests != 3 {
		t.Errorf("requests = %d, want 3", p.Requests)
	}
	if p.PrefillRequests != 2 {
		t.Errorf("prefill_requests = %d, want 2 (uninstrumented excluded)", p.PrefillRequests)
	}
	want := 1000 * 41000.0 / 50000.0 // 820 tok/s token-weighted
	if diff := p.PrefillTokS - want; diff > 1 || diff < -1 {
		t.Errorf("prefill_tok_s = %.1f, want ~%.1f", p.PrefillTokS, want)
	}
	wantDec := 1000 * 300.0 / 20000.0 // 15 tok/s
	if diff := p.DecodeTokS - wantDec; diff > 0.1 || diff < -0.1 {
		t.Errorf("decode_tok_s = %.1f, want ~%.1f", p.DecodeTokS, wantDec)
	}
}

// A prompt served mostly from the KV cache must not inflate the prefill rate:
// cached tokens were read, not evaluated. 40000 prompt tokens with 39000 cached
// over 3000ms is 1000 fresh tokens at ~333 tok/s, not 13333 tok/s.
func TestThroughputTimeSeriesExcludesCachedTokens(t *testing.T) {
	d, host := throughputDB(t)
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Hour).Add(5 * time.Minute)

	id, err := d.Logs.Create(LogInput{
		BackendID: &host, Model: "m", Endpoint: "/v1/chat/completions",
		RequestTokens: 40000, CachedTokens: 39000, ResponseTokens: 100, TotalTokens: 40100,
		StatusCode: 200, LatencyMs: 6000, PrefillMs: 3000, DecodeMs: 3000,
	})
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if _, err := d.conn.Exec("UPDATE request_logs SET created_at = ? WHERE id = ?", at, id); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	got, err := d.ThroughputTimeSeries(at.Add(-time.Minute), time.Now(), "hour")
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	var p *ThroughputPoint
	for i := range got.Points {
		if got.Points[i].Requests > 0 {
			p = &got.Points[i]
			break
		}
	}
	if p == nil {
		t.Fatal("no bucket with requests")
	}
	if p.PrefillTokens != 1000 {
		t.Errorf("prefill_tokens = %d, want 1000 (fresh only)", p.PrefillTokens)
	}
	if diff := p.PrefillTokS - 1000.0/3.0; diff > 0.01 || diff < -0.01 {
		t.Errorf("prefill_tok_s = %.1f, want ~%.1f", p.PrefillTokS, 1000.0/3.0)
	}
}

// A fully cached prompt has no measurable prefill at all — it must not count as
// a prefill sample even though prefill_ms is nonzero (queue + first token).
func TestThroughputTimeSeriesFullyCachedExcluded(t *testing.T) {
	d, host := throughputDB(t)
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Hour).Add(5 * time.Minute)

	id, err := d.Logs.Create(LogInput{
		BackendID: &host, Model: "m", Endpoint: "/v1/chat/completions",
		RequestTokens: 40000, CachedTokens: 40000, ResponseTokens: 100, TotalTokens: 40100,
		StatusCode: 200, LatencyMs: 6000, PrefillMs: 500, DecodeMs: 5500,
	})
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if _, err := d.conn.Exec("UPDATE request_logs SET created_at = ? WHERE id = ?", at, id); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	got, err := d.ThroughputTimeSeries(at.Add(-time.Minute), time.Now(), "hour")
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	for _, p := range got.Points {
		if p.PrefillRequests > 0 {
			t.Fatalf("prefill_requests = %d, want 0 (nothing was evaluated)", p.PrefillRequests)
		}
	}
}

// The per-host split must aggregate each backend's concurrent decode windows
// into one machine-level rate: two 100-token requests of 10s each on the same
// host report 200 tokens / 20s = 10 tok/s for that host, while a request on a
// second host stays on its own line. Rows without a backend (archived hosts)
// count in the aggregate point but produce no per-host entry.
func TestThroughputTimeSeriesPerHostSplit(t *testing.T) {
	d, hostA := throughputDB(t)
	hostB, err := d.Backends.Create(BackendInput{
		Name: "h2", BaseURL: "http://127.0.0.1:2", Weight: 1, MaxConcurrent: 4,
	})
	if err != nil {
		t.Fatalf("backend b: %v", err)
	}
	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Hour)

	logTP := func(at time.Time, host *int64, completion, decodeMs int) {
		id, err := d.Logs.Create(LogInput{
			BackendID: host, Model: "m", Endpoint: "/v1/chat/completions",
			ResponseTokens: completion, TotalTokens: completion,
			StatusCode: 200, LatencyMs: decodeMs, DecodeMs: decodeMs,
		})
		if err != nil {
			t.Fatalf("log: %v", err)
		}
		if _, err := d.conn.Exec("UPDATE request_logs SET created_at = ? WHERE id = ?", at, id); err != nil {
			t.Fatalf("backdate: %v", err)
		}
	}

	logTP(base.Add(time.Minute), &hostA, 100, 10000)
	logTP(base.Add(2*time.Minute), &hostA, 100, 10000)
	logTP(base.Add(3*time.Minute), &hostB.ID, 60, 6000)
	logTP(base.Add(4*time.Minute), nil, 50, 5000) // no backend (archived host)

	got, err := d.ThroughputTimeSeries(base.Add(-time.Minute), time.Now(), "hour")
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	var p *ThroughputPoint
	for i := range got.Points {
		if got.Points[i].Requests > 0 {
			p = &got.Points[i]
			break
		}
	}
	if p == nil {
		t.Fatal("no bucket with requests")
	}
	if len(p.ByBackend) != 2 {
		t.Fatalf("by_backend len = %d, want 2 (backendless row excluded)", len(p.ByBackend))
	}
	a, b := p.ByBackend[0], p.ByBackend[1]
	if a.BackendID != hostA || a.BackendName != "h" {
		t.Errorf("first host = %d/%q, want %d/h", a.BackendID, a.BackendName, hostA)
	}
	if a.Requests != 2 || a.DecodeRequests != 2 {
		t.Errorf("host A requests = %d/%d, want 2/2", a.Requests, a.DecodeRequests)
	}
	if diff := a.DecodeTokS - 10.0; diff > 0.01 || diff < -0.01 {
		t.Errorf("host A decode_tok_s = %.2f, want 10", a.DecodeTokS)
	}
	if b.BackendID != hostB.ID || b.DecodeTokS != 10.0 {
		t.Errorf("host B = %d at %.2f tok/s, want %d at 10", b.BackendID, b.DecodeTokS, hostB.ID)
	}
	// Aggregate point still includes the backendless row.
	if p.Requests != 4 {
		t.Errorf("aggregate requests = %d, want 4", p.Requests)
	}
	// No decode_start_at instrumentation on these rows -> wall-clock rate
	// stays 0 rather than pretending to know the windows.
	if p.ByBackend[0].DecodeWallTokS != 0 {
		t.Errorf("legacy rows DecodeWallTokS = %.2f, want 0", p.ByBackend[0].DecodeWallTokS)
	}
}

// Two requests decoding CONCURRENTLY on one host share wall-clock time, so the
// wall-clock aggregate must show the machine's combined output: two 100-token
// requests that each slowed to 20s under batching occupied one shared 20s
// window -> 200 tok / 20s = 10 tok/s, double the 5 tok/s per-request rate the
// summed-duration metric reports. This is the "concurrency pays off" property
// the summed metric structurally cannot show.
func TestThroughputTimeSeriesWallClockRisesWithConcurrency(t *testing.T) {
	d, hostA := throughputDB(t)
	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Hour)

	logWall := func(start time.Time, completion, decodeMs int) {
		st := start
		id, err := d.Logs.Create(LogInput{
			BackendID: &hostA, Model: "m", Endpoint: "/v1/chat/completions",
			ResponseTokens: completion, TotalTokens: completion,
			StatusCode: 200, LatencyMs: decodeMs, DecodeMs: decodeMs,
			DecodeStartAt: &st,
		})
		if err != nil {
			t.Fatalf("log: %v", err)
		}
		if _, err := d.conn.Exec("UPDATE request_logs SET created_at = ? WHERE id = ?", start, id); err != nil {
			t.Fatalf("backdate: %v", err)
		}
	}

	t0 := base.Add(time.Minute)
	logWall(t0, 100, 20000)
	logWall(t0, 100, 20000) // fully concurrent with the first

	got, err := d.ThroughputTimeSeries(base.Add(-time.Minute), time.Now(), "hour")
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	var a *BackendThroughput
	for i := range got.Points {
		for j := range got.Points[i].ByBackend {
			if got.Points[i].ByBackend[j].Requests > 0 {
				a = &got.Points[i].ByBackend[j]
			}
		}
	}
	if a == nil {
		t.Fatal("no per-host bucket")
	}
	// Summed-duration rate: 200 tok / 40s = 5 (sags under concurrency).
	if diff := a.DecodeTokS - 5.0; diff > 0.01 || diff < -0.01 {
		t.Errorf("DecodeTokS = %.2f, want 5", a.DecodeTokS)
	}
	// Wall-clock rate: 200 tok / 20s union = 10 (rises with concurrency).
	if diff := a.DecodeWallTokS - 10.0; diff > 0.01 || diff < -0.01 {
		t.Errorf("DecodeWallTokS = %.2f, want 10", a.DecodeWallTokS)
	}
	if a.DecodeWallTokS <= a.DecodeTokS {
		t.Errorf("wall rate %.2f should exceed summed rate %.2f under concurrency",
			a.DecodeWallTokS, a.DecodeTokS)
	}
}

// A decode window straddling a bucket boundary splits its tokens proportionally
// to the wall-clock overlap with each bucket: a 60s window starting at :59:30
// puts 50 tokens and 30s of union time in each of the two hours -> 50/30 tok/s
// in both.
func TestThroughputTimeSeriesWallClockSplitsAcrossBuckets(t *testing.T) {
	d, hostA := throughputDB(t)
	base := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Hour)
	straddle := base.Add(59*time.Minute + 30*time.Second)
	st := straddle
	id, err := d.Logs.Create(LogInput{
		BackendID: &hostA, Model: "m", Endpoint: "/v1/chat/completions",
		ResponseTokens: 100, TotalTokens: 100,
		StatusCode: 200, LatencyMs: 60000, DecodeMs: 60000,
		DecodeStartAt: &st,
	})
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if _, err := d.conn.Exec("UPDATE request_logs SET created_at = ? WHERE id = ?", straddle, id); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	got, err := d.ThroughputTimeSeries(base, base.Add(2*time.Hour), "hour")
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	var hits int
	for _, p := range got.Points {
		for _, bp := range p.ByBackend {
			if bp.DecodeWallTokS <= 0 {
				continue
			}
			hits++
			// 50 tokens attributed into a bucket with 30s of union time.
			if diff := bp.DecodeWallTokS - 50.0/30.0; diff > 0.01 || diff < -0.01 {
				t.Errorf("bucket %s wall tok/s = %.4f, want %.4f",
					p.Time.Format(time.RFC3339), bp.DecodeWallTokS, 50.0/30.0)
			}
		}
	}
	if hits != 2 {
		t.Fatalf("wall tokens landed in %d buckets, want 2 (straddling window)", hits)
	}
}
