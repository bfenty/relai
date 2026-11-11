package vrouter

import (
	"math"
	"testing"
)

// #14 (gosec G115): the round-robin cursor is an atomic.Uint64 that eventually
// wraps. The old code did int(cursor.Add(1)-1) % n, which goes NEGATIVE once the
// counter passes math.MaxInt64, panicking with index-out-of-range in the
// placement hot path. The fix takes the modulo in uint64 before the int
// conversion, so any cursor value maps into [0, n).

func TestLeastLoadedCursorWrapStaysInBounds(t *testing.T) {
	m := testManager()
	states := testStates(3)
	// Park the cursor exactly at the wrap point: Add(1) rolls over to 0,
	// minus 1 wraps to MaxUint64 — the value that made int(...) negative.
	m.rrCursor(1).Store(math.MaxUint64)

	got := m.tryAcquireLeastLoaded(1, states, "", nil)
	if got < 0 || got >= len(states) {
		t.Fatalf("index out of bounds after cursor wrap: %d (n=%d)", got, len(states))
	}
}

func TestRoundRobinCursorWrapStaysInBounds(t *testing.T) {
	m := testManager()
	states := testStates(4)
	members := testMembers(4)
	m.rrCursor(2).Store(math.MaxUint64)

	got := m.tryAcquireCacheAware(2, states, members, nil)
	if got < 0 || got >= len(states) {
		t.Fatalf("index out of bounds after cursor wrap: %d (n=%d)", got, len(states))
	}
}
