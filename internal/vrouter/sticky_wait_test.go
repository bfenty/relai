package vrouter

import (
	"context"
	"testing"
	"time"

	"github.com/bfenty/relai/internal/db"
)

func stickyRouter() *db.Router { return &db.Router{ID: 1, Strategy: "sticky"} }

// A deep conversation is worth waiting minutes for: reprocessing it elsewhere costs
// far more than queueing on the host that already holds its KV cache.
func TestStickyWaitBudgetScalesWithPromptSize(t *testing.T) {
	small := stickyWaitBudget(2 * 1024)   // ~500 tokens
	large := stickyWaitBudget(160 * 1024) // ~40k tokens, the observed median depth
	if small >= large {
		t.Fatalf("budget should grow with prompt size: small=%v large=%v", small, large)
	}
	if small > 5*time.Second {
		t.Errorf("a shallow prompt should give up quickly, got %v", small)
	}
	if large < 30*time.Second {
		t.Errorf("a deep prompt should be worth a long wait, got %v", large)
	}
	if got := stickyWaitBudget(1 << 30); got > maxStickyWait {
		t.Errorf("budget exceeded the cap: %v", got)
	}
	if got := stickyWaitBudget(0); got <= 0 {
		t.Errorf("expected a positive floor for an unknown prompt size, got %v", got)
	}
}

// With a free machine available, a shallow sub-agent must not sit behind a busy
// sticky host - it has no cache worth preserving, so it should bounce quickly.
func TestShallowRequestBouncesToFreeMember(t *testing.T) {
	m := testManager()
	states := testStates(2)
	members := testMembers(2)
	states[0].slot.TryAcquire() // sticky host is busy; member 1 is free
	m.setSession("sub-agent", members[0].ModelMapID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	idx, err := m.acquireMember(ctx, stickyRouter(), states, members, "sub-agent", nil, 2*1024, nil)
	if err != nil {
		t.Fatalf("acquireMember failed: %v", err)
	}
	defer states[idx].release()

	if idx != 1 {
		t.Fatalf("expected the free member 1, got %d", idx)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("shallow request waited %v before bouncing", elapsed)
	}
}

// When every alternative is busy too, bouncing accomplishes nothing: the request
// would pay a full prompt reprocess and then queue anyway. It must wait for its
// warm host instead.
func TestAllMembersBusyWaitsForStickyRatherThanBouncing(t *testing.T) {
	m := testManager()
	states := testStates(2)
	members := testMembers(2)
	states[0].slot.TryAcquire() // sticky busy
	states[1].slot.TryAcquire() // and so is the only alternative
	m.setSession("sub-agent", members[0].ModelMapID)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan int, 1)
	go func() {
		// Even with a tiny prompt (a 1s budget), it should keep waiting for member 0
		// because nothing else is free.
		idx, err := m.acquireMember(ctx, stickyRouter(), states, members, "sub-agent", nil, 512, nil)
		if err != nil {
			done <- -1
			return
		}
		done <- idx
	}()

	// Free the sticky host after the shallow budget would have expired.
	time.Sleep(1500 * time.Millisecond)
	states[0].release()

	select {
	case idx := <-done:
		if idx != 0 {
			t.Fatalf("expected to wait for the sticky member 0, got %d", idx)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request never acquired a member")
	}
}

// A free sticky host is taken immediately, with no waiting logic involved.
func TestFreeStickyMemberTakenImmediately(t *testing.T) {
	m := testManager()
	states := testStates(2)
	members := testMembers(2)
	m.setSession("warm", members[1].ModelMapID)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	idx, err := m.acquireMember(ctx, stickyRouter(), states, members, "warm", nil, 160*1024, nil)
	if err != nil {
		t.Fatalf("acquireMember failed: %v", err)
	}
	defer states[idx].release()

	if idx != 1 {
		t.Fatalf("expected sticky member 1, got %d", idx)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("free sticky member took %v to acquire", elapsed)
	}
}

func TestAnyOtherMemberFree(t *testing.T) {
	states := testStates(3)
	if !anyOtherMemberFree(states, 0, nil) {
		t.Error("expected a free member when others are idle")
	}
	states[1].slot.TryAcquire()
	states[2].slot.TryAcquire()
	if anyOtherMemberFree(states, 0, nil) {
		t.Error("expected no free member when all others are busy")
	}
	states[1].release()
	states[1].setHealth("offline")
	if anyOtherMemberFree(states, 0, nil) {
		t.Error("an offline member must not count as free")
	}
}
