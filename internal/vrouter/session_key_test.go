package vrouter

import (
	"net/http"
	"strings"
	"testing"
)

func sessKeyFor(t *testing.T, body string, hdrs map[string]string) string {
	t.Helper()
	r, err := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for k, v := range hdrs {
		r.Header.Set(k, v)
	}
	return computeSessionKey(r, []byte(body))
}

const (
	sysHelper = `{"role":"system","content":"You are a helper."}`
	taskA     = `{"role":"user","content":"Analyse the auth module."}`
	taskB     = `{"role":"user","content":"Write tests for the parser."}`
)

func convo(user string, msgs ...string) string {
	b := "{"
	if user != "" {
		b += `"user":"` + user + `",`
	}
	return b + `"messages":[` + strings.Join(msgs, ",") + `]}`
}

// Sub-agents spawned by one runner share the caller's `user`, but each has a fresh
// context. They must get distinct keys so they can spread across hosts instead of
// pinning to whichever machine served the parent.
func TestSubAgentsWithSharedUserGetDistinctKeys(t *testing.T) {
	a := sessKeyFor(t, convo("agent-runner", sysHelper, taskA), nil)
	b := sessKeyFor(t, convo("agent-runner", sysHelper, taskB), nil)
	if a == b {
		t.Fatalf("sub-agents with different tasks collapsed onto one key: %s", a)
	}
}

// Same, when the framework propagates one trace id to every child.
func TestSubAgentsWithSharedSessionHeaderGetDistinctKeys(t *testing.T) {
	h := map[string]string{"X-Session-ID": "parent-trace-1"}
	a := sessKeyFor(t, convo("", sysHelper, taskA), h)
	b := sessKeyFor(t, convo("", sysHelper, taskB), h)
	if a == b {
		t.Fatalf("sub-agents under one trace id collapsed onto one key: %s", a)
	}
}

// Stickiness must survive across turns: appending messages does not change the key,
// so a continuing conversation keeps hitting the host that holds its KV cache.
func TestKeyStableAcrossTurns(t *testing.T) {
	turn1 := convo("agent-runner", sysHelper, taskA)
	turn2 := convo("agent-runner", sysHelper, taskA,
		`{"role":"assistant","content":"Here is the analysis."}`,
		`{"role":"user","content":"Now check error handling."}`)
	if a, b := sessKeyFor(t, turn1, nil), sessKeyFor(t, turn2, nil); a != b {
		t.Fatalf("key changed between turns, affinity lost:\n  turn1=%s\n  turn2=%s", a, b)
	}
}

// Two callers running the identical conversation stay separated by their identity,
// so one caller's follow-up cannot be routed to the other's cache.
func TestDistinctIdentitiesStaySeparate(t *testing.T) {
	a := sessKeyFor(t, convo("alice", sysHelper, taskA), nil)
	b := sessKeyFor(t, convo("bob", sysHelper, taskA), nil)
	if a == b {
		t.Fatalf("different users share a key: %s", a)
	}
}

// An explicit session id still distinguishes otherwise identical conversations.
func TestSessionHeaderDistinguishesIdenticalConversations(t *testing.T) {
	body := convo("", sysHelper, taskA)
	a := sessKeyFor(t, body, map[string]string{"X-Session-ID": "conv-1"})
	b := sessKeyFor(t, body, map[string]string{"X-Session-ID": "conv-2"})
	if a == b {
		t.Fatalf("distinct session ids share a key: %s", a)
	}
}

// No identity at all: shape alone still keys the conversation.
func TestShapeOnlyStillWorks(t *testing.T) {
	a := sessKeyFor(t, convo("", sysHelper, taskA), nil)
	b := sessKeyFor(t, convo("", sysHelper, taskB), nil)
	if a == "" || b == "" {
		t.Fatal("expected a shape-derived key")
	}
	if a == b {
		t.Fatal("different conversations share a shape key")
	}
	if !strings.HasPrefix(a, "s:") {
		t.Errorf("expected shape-only key to keep its s: prefix, got %s", a)
	}
}

// A request with nothing to key on yields no key, so the router falls back to
// least-connections placement rather than pinning arbitrarily.
func TestNoIdentityNoShapeYieldsNoKey(t *testing.T) {
	if k := sessKeyFor(t, `{"messages":[]}`, nil); k != "" {
		t.Errorf("expected empty key, got %q", k)
	}
}

// An unparseable body must not lose an explicit session id.
func TestUnparseableBodyKeepsHeaderIdentity(t *testing.T) {
	k := sessKeyFor(t, `not json`, map[string]string{"X-Session-ID": "conv-9"})
	if k != "h:conv-9" {
		t.Errorf("expected h:conv-9, got %q", k)
	}
}
