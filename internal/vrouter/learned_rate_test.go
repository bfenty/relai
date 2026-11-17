package vrouter

import (
	"testing"
	"time"

	"github.com/bfenty/relai/internal/hostlimit"
	"github.com/bfenty/relai/internal/proxy"
	"github.com/bfenty/relai/internal/queue"
)

// The wait-or-spill decision prices a cold reprocess at the prefill speed the
// spill targets were actually measured at, not a fleet-wide constant.

func learnedManager(t *testing.T, samples map[int64][]float64) *Manager {
	t.Helper()
	p := proxy.New(nil, nil, nil)
	speeds := queue.NewSpeedLearner()
	for backend, rates := range samples {
		for _, r := range rates {
			speeds.Record(backend, queue.SpeedSample{PromptTokS: r})
		}
	}
	p.SetSpeedLearner(speeds)
	return NewManager(nil, p, nil)
}

func hostStates(backends ...int64) []*memberState {
	out := make([]*memberState, len(backends))
	for i, b := range backends {
		out[i] = &memberState{health: "active", slot: hostlimit.NewSlot(1), backendID: b}
	}
	return out
}

func repeat(v float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestSpillPrefillRateUsesSlowestLearnedAlternative(t *testing.T) {
	m := learnedManager(t, map[int64][]float64{
		1: repeat(900, 10), // the warm host itself: must be ignored
		2: repeat(400, 10),
		3: repeat(300, 10),
	})
	got := m.spillPrefillRate(hostStates(1, 2, 3), 0, nil)
	if got < 299 || got > 301 {
		t.Fatalf("rate = %.1f, want the slowest alternative (300)", got)
	}
}

func TestSpillPrefillRateNeedsEnoughSamples(t *testing.T) {
	m := learnedManager(t, map[int64][]float64{2: repeat(900, minLearnedSamples-1)})
	if got := m.spillPrefillRate(hostStates(1, 2), 0, nil); got != prefillTokensPerSecond {
		t.Fatalf("rate = %.1f, want the default until enough samples (%.0f)", got, prefillTokensPerSecond)
	}
}

func TestSpillPrefillRateIgnoresImplausibleAndOfflineHosts(t *testing.T) {
	m := learnedManager(t, map[int64][]float64{
		2: repeat(50_000, 10), // a cache read misreported as prefill
		3: repeat(100, 10),    // offline below
	})
	states := hostStates(1, 2, 3)
	states[2].health = "offline"
	if got := m.spillPrefillRate(states, 0, nil); got != prefillTokensPerSecond {
		t.Fatalf("rate = %.1f, want the default when no alternative is trustworthy", got)
	}
}

func TestSpillPrefillRateWithoutLearner(t *testing.T) {
	m := NewManager(nil, nil, nil)
	if got := m.spillPrefillRate(hostStates(1, 2), 0, nil); got != prefillTokensPerSecond {
		t.Fatalf("rate = %.1f, want the default with no proxy", got)
	}
}

func TestStickyWaitBudgetScalesWithMeasuredRate(t *testing.T) {
	const prompt = 160 * 1024 // ~40k tokens
	slow := stickyWaitBudgetAt(prompt, 200)
	fast := stickyWaitBudgetAt(prompt, 400)
	if fast >= slow {
		t.Fatalf("a faster spill target must shorten the wait budget: slow=%v fast=%v", slow, fast)
	}
	if d := slow - 2*fast; d < -time.Second || d > time.Second {
		t.Fatalf("budget should scale inversely with rate: slow=%v fast=%v", slow, fast)
	}
	if stickyWaitBudgetAt(prompt, 0) != stickyWaitBudget(prompt) {
		t.Fatalf("an unknown rate must fall back to the default budget")
	}
}

func TestSpillImmediatelyAtFasterRateSpillsLargerPrompts(t *testing.T) {
	m := NewManager(nil, nil, nil)
	// 24 KB ≈ 6k tokens: 30s at the default 200 tok/s (wait), 15s at 400 tok/s (spill).
	if m.shouldSpillImmediatelyAt(1, "", 24_000, prefillTokensPerSecond) {
		t.Fatalf("at the default rate a 6k-token prompt is not cheap enough to spill at once")
	}
	if !m.shouldSpillImmediatelyAt(1, "", 24_000, 400) {
		t.Fatalf("at a measured 400 tok/s a 6k-token prompt is cheap enough to spill at once")
	}
}
