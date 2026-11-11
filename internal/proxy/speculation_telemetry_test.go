package proxy

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bfenty/relai/internal/db"
	"github.com/bfenty/relai/internal/queue"
)

// TestSpeculationTelemetryLog verifies that timings.draft_n / draft_n_accepted
// from a Halogen response are captured on the request log row (both the
// non-streamed body and the streamed usage chunk carry them).
func TestSpeculationTelemetryLog(t *testing.T) {
	d := testDBForProxy(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","choices":[{"message":{"content":"ok"}}],` +
			`"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15},` +
			`"timings":{"draft_n":40,"draft_n_accepted":28}}`))
	}))
	defer upstream.Close()

	p := New(d, nil, queue.NewTracker())
	backend, err := d.Backends.Create(db.BackendInput{Name: "halogen-spec", BaseURL: upstream.URL})
	if err != nil {
		t.Fatalf("create backend: %v", err)
	}

	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	if err := p.ProxyRequest(rec, req, backend, &db.ModelMap{LocalModel: "m", RemoteModel: "m"}, &RequestContext{}); err != nil {
		t.Fatalf("proxy request: %v", err)
	}

	logs, _, err := d.Logs.List(db.LogFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected 1 log row, got %d", len(logs))
	}
	if logs[0].DraftN != 40 || logs[0].DraftNAccepted != 28 {
		t.Fatalf("draft telemetry = %d/%d, want 40/28", logs[0].DraftN, logs[0].DraftNAccepted)
	}

	// And the throughput analytics roll it up into an accept rate.
	series, err := d.ThroughputTimeSeries(time.Now().Add(-time.Hour), time.Now().Add(time.Hour), "hour")
	if err != nil {
		t.Fatalf("throughput series: %v", err)
	}
	var found bool
	for _, pt := range series.Points {
		if pt.DraftN == 40 {
			found = true
			want := 28.0 / 40.0
			if got := pt.AcceptRate; got != want {
				t.Fatalf("accept rate = %v, want %v", got, want)
			}
			for _, bp := range pt.ByBackend {
				if bp.BackendID == backend.ID && bp.AcceptRate != want {
					t.Fatalf("per-host accept rate = %v, want %v", bp.AcceptRate, want)
				}
			}
		}
	}
	if !found {
		t.Fatal("no throughput point carried draft_n=40")
	}
}
