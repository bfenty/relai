package queue

import "testing"

// The learner drives the prefill progress estimate, so a failed request (zero
// rates) must not dilute a host's learned speed, and the first real sample must
// land at full weight rather than being averaged toward zero.
func TestSpeedLearnerIgnoresZeroSamples(t *testing.T) {
	l := NewSpeedLearner()
	l.Record(1, SpeedSample{})
	if got := l.Get(1); got.Samples != 0 {
		t.Fatalf("empty sample recorded: %+v", got)
	}
	l.Record(1, SpeedSample{PromptTokS: 500, DecodeTokS: 25})
	first := l.Get(1)
	if first.PromptTokS != 500 || first.DecodeTokS != 25 {
		t.Fatalf("first sample not full weight: %+v", first)
	}
	l.Record(1, SpeedSample{PromptTokS: 500, DecodeTokS: 25})
	l.Record(1, SpeedSample{PromptTokS: 100, DecodeTokS: 5})
	second := l.Get(1)
	if second.PromptTokS >= 500 || second.PromptTokS <= 100 {
		t.Fatalf("EWMA did not move between samples: %+v", second)
	}
	if second.Samples != 3 {
		t.Fatalf("samples = %d, want 3", second.Samples)
	}
}

// Unknown backends report zero speed (cold backend -> indeterminate bar).
func TestSpeedLearnerUnknownBackend(t *testing.T) {
	l := NewSpeedLearner()
	if got := l.Get(42); got.PromptTokS != 0 || got.Samples != 0 {
		t.Fatalf("unknown backend not zero: %+v", got)
	}
	if len(l.Snapshot()) != 0 {
		t.Fatal("snapshot not empty")
	}
}
