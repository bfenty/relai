package queue

import (
	"strings"
	"testing"
	"time"
)

// The queue view shows live throughput: prompt tok/s must be derived from the
// measured prefill duration and the estimated prompt size, and decode tok/s from
// the counted deltas since the first token. A snapshot must never expose the raw
// timestamps, only the computed rates.
func TestSnapshotComputesThroughputRates(t *testing.T) {
	tr := NewTracker()
	id := tr.Add(1, "host", "m")

	tr.Update(id, Update{
		PromptMs:          2000,
		EstPromptTokens:   4000,
		FirstTokenAt:      time.Now().Add(-100 * time.Millisecond),
		GeneratedTokens:   10,
		EstPromptTokensOk: true,
	})

	snap := tr.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("want 1 entry, got %d", len(snap))
	}
	e := snap[0]
	// 4000 est tokens / 2s prefill = 2000 tok/s.
	if e.PromptTokS < 1900 || e.PromptTokS > 2100 {
		t.Errorf("prompt_tok_s = %.1f, want ~2000", e.PromptTokS)
	}
	// 10 tokens in ~100ms = ~100 tok/s.
	if e.DecodeTokS < 60 || e.DecodeTokS > 160 {
		t.Errorf("decode_tok_s = %.1f, want ~100", e.DecodeTokS)
	}
	if e.PromptMs != 2000 {
		t.Errorf("prompt_ms = %d, want 2000", e.PromptMs)
	}
}

// Before the first token arrives there is no prefill duration to divide by, so
// prompt_tok_s must stay 0 rather than claiming an infinite speed. Likewise an
// entry with no generated tokens has no decode rate.
func TestRatesZeroBeforeSignals(t *testing.T) {
	tr := NewTracker()
	id := tr.Add(1, "host", "m")
	e := tr.Snapshot()[0]
	if e.PromptTokS != 0 || e.DecodeTokS != 0 {
		t.Errorf("fresh entry has rates %.1f/%.1f, want 0/0", e.PromptTokS, e.DecodeTokS)
	}

	tr.Update(id, Update{GeneratedTokens: 5, FirstTokenAt: time.Now().Add(-time.Second)})
	e = tr.Snapshot()[0]
	if e.PromptTokS != 0 {
		t.Errorf("prompt_tok_s = %.1f before prefill known, want 0", e.PromptTokS)
	}
	if e.DecodeTokS < 4 || e.DecodeTokS > 6 {
		t.Errorf("decode_tok_s = %.1f, want ~5", e.DecodeTokS)
	}
}

// Update must not resurrect a removed entry (request finished while a late SSE
// chunk was in flight).
func TestUpdateAfterRemoveIsNoop(t *testing.T) {
	tr := NewTracker()
	id := tr.Add(1, "host", "m")
	tr.Remove(id)
	tr.Update(id, Update{GeneratedTokens: 3})
	if len(tr.Snapshot()) != 0 {
		t.Error("removed entry reappeared")
	}
}

// The tracker stays content-free: rates and counts only, never prompt text.
func TestEntryJSONHasNoContentFields(t *testing.T) {
	tr := NewTracker()
	tr.Add(1, "host", "m")
	var sb strings.Builder
	for _, e := range tr.Snapshot() {
		sb.WriteString(e.Model + e.BackendName + e.ClientKey)
	}
	if strings.Contains(sb.String(), "prompt") {
		t.Error("unexpected content in entry fields")
	}
}
