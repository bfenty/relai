package queue

import (
	"sync"
	"time"
)

// SpeedSample is one completed request's measured throughput.
type SpeedSample struct {
	PromptTokS float64 // actual prompt tokens / measured prefill duration
	DecodeTokS float64 // actual completion tokens / decode duration
}

// BackendSpeed is an EWMA of a backend's recent throughput, learned from
// completed requests. It powers the queue view's prefill progress estimate: while
// a prompt is processing the backend sends nothing, so the only honest progress
// signal is "how fast does this host usually chew through a prompt of this size".
type BackendSpeed struct {
	BackendID  int64   `json:"backend_id"`
	PromptTokS float64 `json:"prompt_tok_s"`
	DecodeTokS float64 `json:"decode_tok_s"`
	Samples    int64   `json:"samples"`
	updated    time.Time
}

// speedDecay is how quickly old samples lose influence (~10 samples half-life).
const speedDecay = 0.93

// SpeedLearner keeps a per-backend EWMA of prefill and decode speed.
type SpeedLearner struct {
	mu      sync.RWMutex
	speeds  map[int64]*BackendSpeed
	maxHost int
}

func NewSpeedLearner() *SpeedLearner {
	return &SpeedLearner{speeds: make(map[int64]*BackendSpeed), maxHost: 512}
}

// Record folds one completed request into the backend's EWMA. Zero rates (failed
// or unmeasurable requests) are ignored so errors never drag the estimate down.
func (l *SpeedLearner) Record(backendID int64, s SpeedSample) {
	if backendID <= 0 || (s.PromptTokS <= 0 && s.DecodeTokS <= 0) {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	bs, ok := l.speeds[backendID]
	if !ok {
		if len(l.speeds) >= l.maxHost {
			return // pathological cardinality guard; backends are few
		}
		bs = &BackendSpeed{BackendID: backendID}
		l.speeds[backendID] = bs
	}
	a := 1 - speedDecay
	if s.PromptTokS > 0 {
		bs.PromptTokS = ewma(bs.PromptTokS, s.PromptTokS, bs.Samples > 0, a)
	}
	if s.DecodeTokS > 0 {
		bs.DecodeTokS = ewma(bs.DecodeTokS, s.DecodeTokS, bs.Samples > 0, a)
	}
	bs.Samples++
	bs.updated = time.Now()
}

func ewma(prev, next float64, hasPrev bool, alpha float64) float64 {
	if !hasPrev || prev <= 0 {
		return next
	}
	return prev*(1-alpha) + next*alpha
}

// Get returns the learned speed for a backend (zero value if unknown).
func (l *SpeedLearner) Get(backendID int64) BackendSpeed {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if bs, ok := l.speeds[backendID]; ok {
		return *bs
	}
	return BackendSpeed{BackendID: backendID}
}

// Snapshot returns all learned speeds for the admin API.
func (l *SpeedLearner) Snapshot() []BackendSpeed {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]BackendSpeed, 0, len(l.speeds))
	for _, bs := range l.speeds {
		out = append(out, *bs)
	}
	return out
}
