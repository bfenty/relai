package vrouter

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bfenty/relai/internal/hostlimit"
	"github.com/bfenty/relai/internal/proxy"
)

// A sub-agent's tools and system prompt are shared with its siblings; placing a new
// conversation on the host that already processed them saves that whole prefill.

var bigSystem = strings.Repeat("You are a careful coding agent. ", 400) // ~12 KB

func chatBody(t *testing.T, tools string, system, user string) []byte {
	t.Helper()
	msgs := []map[string]string{}
	if system != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": system})
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": user})
	body := map[string]any{"model": "m", "messages": msgs}
	if tools != "" {
		body["tools"] = json.RawMessage(tools)
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sharedOf(t *testing.T, body []byte) sharedPrefix {
	t.Helper()
	_, sp := computeRoutingKeys(httptest.NewRequest("POST", "/v1/chat/completions", nil), body)
	return sp
}

func TestSharedPrefixIsCommonToSiblingConversations(t *testing.T) {
	a := sharedOf(t, chatBody(t, "", bigSystem, "refactor the parser"))
	b := sharedOf(t, chatBody(t, "", bigSystem, "write the tests"))
	if a.key == "" || a.key != b.key {
		t.Fatalf("siblings with the same system prompt must share a prefix key: %q vs %q", a.key, b.key)
	}
	c := sharedOf(t, chatBody(t, "", bigSystem+" Different.", "refactor the parser"))
	if c.key == a.key {
		t.Fatalf("a different system prompt must not share the key")
	}
}

func TestSharedPrefixIncludesToolsIgnoringWhitespace(t *testing.T) {
	tools := `[{"type":"function","function":{"name":"read_file","parameters":{"type":"object"}}}]`
	spaced := "[ {\"type\": \"function\", \"function\": {\"name\": \"read_file\", \"parameters\": {\"type\": \"object\"}}} ]"
	a := sharedOf(t, chatBody(t, tools, bigSystem, "x"))
	b := sharedOf(t, chatBody(t, spaced, bigSystem, "y"))
	if a.key == "" || a.key != b.key {
		t.Fatalf("whitespace in tool definitions must not split the prefix")
	}
	none := sharedOf(t, chatBody(t, "", bigSystem, "x"))
	if none.key == a.key {
		t.Fatalf("tools are part of the prefix: with and without them must differ")
	}
}

func TestSharedPrefixIgnoresSmallPrefixes(t *testing.T) {
	if sp := sharedOf(t, chatBody(t, "", "You are helpful.", "hi")); sp.key != "" {
		t.Fatalf("a tiny system prompt is not worth steering for, got key %q", sp.key)
	}
}

func TestComputeSessionKeyUnchangedByRefactor(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := chatBody(t, "", "base", "task")
	key, _ := computeRoutingKeys(r, body)
	if key == "" || key != computeSessionKey(r, body) {
		t.Fatalf("session key must not depend on which entry point computed it")
	}
}

// ── Placement ──

func homeStates(capacities ...int) []*memberState {
	out := make([]*memberState, len(capacities))
	for i, c := range capacities {
		out[i] = &memberState{health: "active", slot: hostlimit.NewSlot(c)}
	}
	return out
}

func placeFresh(t *testing.T, m *Manager, states []*memberState, shared sharedPrefix) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	decision := ""
	idx, err := m.acquireMemberShared(ctx, stickyRouter(), states, testMembers(len(states)), "", nil, 4096, shared, &decision)
	if err != nil {
		t.Fatalf("placement failed: %v", err)
	}
	return idx, decision
}

func TestFreshConversationGoesToItsPrefixHome(t *testing.T) {
	m := testManager()
	shared := sharedPrefix{key: "k", bytes: 40_000}
	m.prefixHomes.set(1, "k", 3) // model map 3 = index 2
	for i := 0; i < 5; i++ {
		states := homeStates(1, 1, 1)
		idx, decision := placeFresh(t, m, states, shared)
		if idx != 2 || decision != proxy.RoutingPrefixHome {
			t.Fatalf("run %d: got member %d (%s), want the prefix home (2, prefix_home)", i, idx, decision)
		}
	}
}

func TestBusyPrefixHomeFallsBackToLoad(t *testing.T) {
	m := testManager()
	m.prefixHomes.set(1, "k", 1)
	states := homeStates(1, 1)
	if !states[0].slot.TryAcquire() { // the home is at capacity
		t.Fatal("setup")
	}
	idx, decision := placeFresh(t, m, states, sharedPrefix{key: "k", bytes: 400_000})
	if idx != 1 || decision != proxy.RoutingFresh {
		t.Fatalf("got member %d (%s), want the free host by load (1, fresh)", idx, decision)
	}
}

func TestWorkingPrefixHomeOnlyWinsOverIdleHostWhenPrefixIsExpensive(t *testing.T) {
	// The home is running one request (capacity 2); the other host is idle.
	setup := func() (*Manager, []*memberState) {
		m := testManager()
		m.prefixHomes.set(1, "k", 1)
		states := homeStates(2, 2)
		states[0].slot.TryAcquire()
		return m, states
	}

	m, states := setup()
	// ~1.5k tokens: ~7s to rebuild at the default rate, cheaper than crowding the home.
	if idx, _ := placeFresh(t, m, states, sharedPrefix{key: "k", bytes: 6_000}); idx != 1 {
		t.Fatalf("a cheap prefix should go to the idle host, got member %d", idx)
	}

	m, states = setup()
	// ~25k tokens: two minutes of prefill, worth sharing the warm host.
	if idx, d := placeFresh(t, m, states, sharedPrefix{key: "k", bytes: 100_000}); idx != 0 || d != proxy.RoutingPrefixHome {
		t.Fatalf("an expensive prefix should join its warm home, got member %d (%s)", idx, d)
	}
}

func TestOfflineOrUnknownHomeIsIgnored(t *testing.T) {
	m := testManager()
	m.prefixHomes.set(1, "k", 1)
	states := homeStates(1, 1)
	states[0].health = "offline"
	if idx, _ := placeFresh(t, m, states, sharedPrefix{key: "k", bytes: 100_000}); idx != 1 {
		t.Fatalf("an offline home must be skipped, got member %d", idx)
	}
	if idx, d := placeFresh(t, testManager(), homeStates(1, 1), sharedPrefix{key: "unseen", bytes: 100_000}); idx < 0 || d != proxy.RoutingFresh {
		t.Fatalf("an unknown prefix places by load, got %d (%s)", idx, d)
	}
}

func TestStickySpillKeepsItsDecisionWhenLandingOnPrefixHome(t *testing.T) {
	m := testManager()
	shared := sharedPrefix{key: "k", bytes: 100_000}
	m.prefixHomes.set(1, "k", 2)
	m.setSession("conv", 1) // the conversation's own host is member 0
	states := homeStates(1, 1)
	states[0].slot.TryAcquire() // and it's busy
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	decision := ""
	// A tiny prompt spills immediately rather than waiting.
	idx, err := m.acquireMemberShared(ctx, stickyRouter(), states, testMembers(2), "conv", nil, 1_000, shared, &decision)
	if err != nil || idx != 1 {
		t.Fatalf("got %d, %v; want the free prefix home", idx, err)
	}
	if decision != proxy.RoutingStickySpill {
		t.Fatalf("decision = %s; a spill must still be reported as a spill", decision)
	}
}

// ── Home table ──

func TestPrefixHomesExpireAndStayBounded(t *testing.T) {
	p := newPrefixHomes()
	p.set(1, "a", 7)
	if p.get(1, "a") != 7 || p.get(2, "a") != 0 {
		t.Fatal("homes are per router")
	}
	p.mu.Lock()
	p.homes[homeKey(1, "a")] = prefixHome{modelMapID: 7, lastSeen: time.Now().Add(-2 * prefixHomeTTL)}
	p.mu.Unlock()
	if p.get(1, "a") != 0 {
		t.Fatal("a stale home must not be trusted")
	}
	p.prune()
	if len(p.homes) != 0 {
		t.Fatal("prune must drop stale homes")
	}
	for i := 0; i < maxPrefixHomes+10; i++ {
		p.set(1, strings.Repeat("x", 1)+string(rune('a'+i%26))+time.Duration(i).String(), 1)
	}
	if len(p.homes) > maxPrefixHomes {
		t.Fatalf("table grew to %d, cap is %d", len(p.homes), maxPrefixHomes)
	}
}
