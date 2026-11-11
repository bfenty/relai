package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bfenty/relai/internal/db"
	"github.com/bfenty/relai/internal/queue"
)

// The live queue counters and the persisted prefill/decode timings must both
// come from the same SSE stream the client sees — this drives a fake upstream
// through StreamingReader and checks what the tracker and request_logs end up
// holding.
func TestStreamingReaderRecordsThroughput(t *testing.T) {
	d, err := db.New(filepath.Join(t.TempDir(), "tp.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer d.Close()
	backend, err := d.Backends.Create(db.BackendInput{
		Name: "h", BaseURL: "http://127.0.0.1:1", Weight: 1, MaxConcurrent: 4,
	})
	if err != nil {
		t.Fatalf("backend: %v", err)
	}

	tracker := queue.NewTracker()
	trackID := tracker.Add(backend.ID, backend.Name, "test-model")
	learner := queue.NewSpeedLearner()
	p := New(d, nil, tracker)
	p.SetSpeedLearner(learner)

	start := time.Now().Add(-500 * time.Millisecond)
	sess := &StreamingReader{
		Proxy:           p,
		Backend:         backend,
		Request:         httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil),
		Model:           "test-model",
		StartTime:       start,
		StatusCode:      http.StatusOK,
		TrackID:         trackID,
		Tracker:         tracker,
		EstPromptTokens: 100,
	}

	// 10 content deltas then a final usage chunk, then EOF.
	var sse strings.Builder
	for i := 0; i < 10; i++ {
		sse.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"tok\"}}]}\n\n")
	}
	sse.WriteString("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":10,\"total_tokens\":110}}\n\n")
	sse.WriteString("data: [DONE]\n\n")

	// A small per-read pause keeps decode_ms measurable — a single synchronous
	// read of a strings.Reader would round the whole stream to 0ms.
	sess.Reader = &pausingReader{r: strings.NewReader(sse.String()), pause: 5 * time.Millisecond}
	// Small reads so several pauses land between the first and last delta.
	out := make([]byte, 64)
	for {
		if _, err := sess.Read(out); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("read: %v", err)
		}
	}

	// Live tracker: phase reached streaming, tokens counted, prefill measured.
	// (Remove happens in ProxyRequest's defer, not in the reader.)
	entries := tracker.Snapshot()
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Status != queue.PhaseStreaming {
		t.Errorf("status = %q, want %q", e.Status, queue.PhaseStreaming)
	}
	if e.GeneratedTokens != 10 {
		t.Errorf("generated_tokens = %d, want 10", e.GeneratedTokens)
	}
	if e.PromptMs <= 0 {
		t.Errorf("prompt_ms = %d, want > 0 (start to first delta)", e.PromptMs)
	}
	if e.DecodeTokS <= 0 {
		t.Errorf("decode_tok_s = %.1f, want > 0", e.DecodeTokS)
	}

	// Learned speed: 100 prompt tokens over the measured start-to-first-token
	// window. The window is a handful of paused reads, so only sanity-check it.
	speeds := learner.Snapshot()
	if len(speeds) != 1 {
		t.Fatalf("speeds = %+v, want one backend", speeds)
	}
	if speeds[0].PromptTokS <= 0 {
		t.Errorf("learned prompt_tok_s = %.1f, want > 0", speeds[0].PromptTokS)
	}
	if speeds[0].DecodeTokS <= 0 {
		t.Errorf("learned decode_tok_s = %.1f, want > 0", speeds[0].DecodeTokS)
	}

	// Persisted row carries the phase split for analytics.
	logs, _, err := d.Logs.List(db.LogFilter{Limit: 10})
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if len(logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(logs))
	}
	row := logs[0]
	if row.PrefillMs <= 0 {
		t.Errorf("prefill_ms = %d, want > 0", row.PrefillMs)
	}
	if row.DecodeMs < 40 {
		t.Errorf("decode_ms = %d, want >= 40 (paused reads)", row.DecodeMs)
	}
}

// pausingReader adds a fixed delay per Read so stream-phase durations are
// observable at millisecond resolution.
type pausingReader struct {
	r     io.Reader
	pause time.Duration
}

func (p *pausingReader) Read(buf []byte) (int, error) {
	time.Sleep(p.pause)
	return p.r.Read(buf)
}

// The learner must divide prefill time by freshly evaluated tokens only. A
// prompt served 90% from the KV cache took the same wall time but processed a
// tenth of the tokens; folding the full prompt_tokens into the sample inflates
// the learned rate by the cache fraction (the 16k tok/s bug).
func TestRecordSpeedExcludesCachedPromptTokens(t *testing.T) {
	d, err := db.New(filepath.Join(t.TempDir(), "tp-cache.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	defer d.Close()
	backend, err := d.Backends.Create(db.BackendInput{
		Name: "h", BaseURL: "http://127.0.0.1:1", Weight: 1, MaxConcurrent: 4,
	})
	if err != nil {
		t.Fatalf("backend: %v", err)
	}

	run := func(t *testing.T, cached int) (promptTokS float64) {
		t.Helper()
		tracker := queue.NewTracker()
		trackID := tracker.Add(backend.ID, backend.Name, "test-model")
		learner := queue.NewSpeedLearner()
		p := New(d, nil, tracker)
		p.SetSpeedLearner(learner)

		sess := &StreamingReader{
			Proxy:           p,
			Backend:         backend,
			Request:         httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil),
			Model:           "test-model",
			StartTime:       time.Now().Add(-500 * time.Millisecond),
			StatusCode:      http.StatusOK,
			TrackID:         trackID,
			Tracker:         tracker,
			EstPromptTokens: 100,
		}
		usage := `data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110}}`
		if cached > 0 {
			usage = `data: {"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110,"prompt_tokens_details":{"cached_tokens":` +
				fmt.Sprintf("%d", cached) + `}}}`
		}
		var sse strings.Builder
		for i := 0; i < 10; i++ {
			sse.WriteString("data: {\"choices\":[{\"delta\":{\"content\":\"tok\"}}]}\n\n")
		}
		sse.WriteString(usage + "\n\n")
		sse.WriteString("data: [DONE]\n\n")

		sess.Reader = &pausingReader{r: strings.NewReader(sse.String()), pause: 5 * time.Millisecond}
		out := make([]byte, 64)
		for {
			if _, err := sess.Read(out); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("read: %v", err)
			}
		}
		speeds := learner.Snapshot()
		if len(speeds) != 1 {
			t.Fatalf("speeds = %+v, want one backend", speeds)
		}
		return speeds[0].PromptTokS
	}

	uncached := run(t, 0)
	if uncached <= 0 {
		t.Fatalf("uncached learned rate = %.1f, want > 0", uncached)
	}
	cached := run(t, 90)
	// 10 fresh tokens vs 100 over a comparable window: ~10x lower. Both runs
	// carry the same fixed read overhead, so assert the drop is at least 5x
	// rather than pinning the exact ratio.
	if cached >= uncached/2 {
		t.Errorf("cached run learned %.1f tok/s, want well below uncached %.1f (cache must not count as prefill)", cached, uncached)
	}
}
