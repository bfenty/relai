package router

import "sync"

// ActiveTracker tracks in-flight requests per backend.
type ActiveTracker struct {
	mu     sync.RWMutex
	counts map[int64]int
}

func NewActiveTracker() *ActiveTracker {
	return &ActiveTracker{counts: make(map[int64]int)}
}

// Increment records one active request for a backend and returns a release func.
func (t *ActiveTracker) Increment(backendID int64) func() {
	t.mu.Lock()
	t.counts[backendID]++
	t.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			if t.counts[backendID] <= 1 {
				delete(t.counts, backendID)
				return
			}
			t.counts[backendID]--
		})
	}
}

func (t *ActiveTracker) Count(backendID int64) int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.counts[backendID]
}
