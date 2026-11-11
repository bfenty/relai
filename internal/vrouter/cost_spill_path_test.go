package vrouter

import (
	"context"
	"testing"

	"github.com/bfenty/relai/internal/db"
	"github.com/bfenty/relai/internal/proxy"
)

// #18 fast path: warm host busy, another host free, conversation cheap →
// immediate spill (decision sticky_spill, no queueing).
func TestStickyBusyCheapConversationSpillsImmediately(t *testing.T) {
	m := testManager()
	router := &db.Router{ID: 7, Strategy: "sticky"}
	states := testStates(2)
	members := testMembers(2)
	states[0].slot.TryAcquire() // warm member 0 busy
	defer states[0].release()
	m.setSession("agent-a", members[0].ModelMapID)

	decision := ""
	idx, err := m.acquireMember(context.Background(), router, states, members, "agent-a", nil, 4_000, &decision)
	if err != nil {
		t.Fatalf("acquireMember: %v", err)
	}
	defer states[idx].release()
	if idx == 0 {
		t.Fatalf("cheap conversation should have spilled off busy warm member")
	}
	if decision != proxy.RoutingStickySpill {
		t.Fatalf("decision = %q, want sticky_spill", decision)
	}
}

// Whale conversation (large prompt) must NOT take the fast path: with the warm
// host busy and a short caller deadline, the whale waits on its warm member
// until the caller's context expires rather than spilling instantly.
func TestStickyBusyWhaleDoesNotFastSpill(t *testing.T) {
	m := testManager()
	router := &db.Router{ID: 7, Strategy: "sticky"}
	states := testStates(2)
	members := testMembers(2)
	states[0].slot.TryAcquire()
	defer states[0].release()
	m.setSession("agent-whale", members[0].ModelMapID)

	ctx, cancel := context.WithTimeout(context.Background(), 20*1e6) // 20ms
	defer cancel()
	decision := ""
	_, err := m.acquireMember(ctx, router, states, members, "agent-whale", nil, 2_000_000, &decision)
	if err == nil {
		t.Fatal("whale should have timed out waiting for its warm member, not spilled")
	}
}
