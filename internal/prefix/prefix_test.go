package prefix

import (
	"encoding/json"
	"fmt"
	"testing"
)

type msg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func body(msgs ...msg) []byte {
	b, err := json.Marshal(map[string]any{"messages": msgs})
	if err != nil {
		panic(err)
	}
	return b
}

func sys(s string) msg  { return msg{Role: "system", Content: s} }
func user(s string) msg { return msg{Role: "user", Content: s} }
func asst(s string) msg { return msg{Role: "assistant", Content: s} }

func TestAppendIsHealthy(t *testing.T) {
	prev := Compute(body(sys("you are a bot"), user("hello"), asst("hi")))
	cur := Compute(body(sys("you are a bot"), user("hello"), asst("hi"), user("more")))
	d := Compare(prev, cur)
	if d.Cause != CauseAppend {
		t.Errorf("cause = %q, want append", d.Cause)
	}
	if d.Retained != 1 {
		t.Errorf("an append forfeits nothing, got retained=%.2f", d.Retained)
	}
	if d.Costly() {
		t.Error("an append must not be reported as costly")
	}
}

// The headline case: a timestamp in the system prompt invalidates the whole prompt,
// however long the conversation has grown.
func TestChangedSystemPromptIsDiagnosed(t *testing.T) {
	prev := Compute(body(sys("time is 10:00"), user("hello"), asst("hi")))
	cur := Compute(body(sys("time is 10:01"), user("hello"), asst("hi")))
	d := Compare(prev, cur)
	if d.Cause != CauseSystem {
		t.Errorf("cause = %q, want system", d.Cause)
	}
	if d.DivergedAt != 0 {
		t.Errorf("diverged at %d, want 0", d.DivergedAt)
	}
	if d.Retained != 0 {
		t.Errorf("retained = %.2f, want 0 - nothing survives a changed system prompt", d.Retained)
	}
	if !d.Costly() {
		t.Error("a changed system prompt is the most costly case there is")
	}
}

func TestRewrittenTailIsCheap(t *testing.T) {
	prev := Compute(body(sys("s"), user("a"), asst("b"), user("c")))
	cur := Compute(body(sys("s"), user("a"), asst("b"), user("different")))
	d := Compare(prev, cur)
	if d.Cause != CauseTail {
		t.Errorf("cause = %q, want tail", d.Cause)
	}
	if d.Retained <= 0 {
		t.Error("a tail rewrite should retain most of the prompt")
	}
}

func TestRewrittenHeadIsDistinctFromTail(t *testing.T) {
	msgs := []msg{sys("s"), user("a"), asst("b"), user("c"), asst("d"), user("e")}
	prev := Compute(body(msgs...))
	changed := append([]msg(nil), msgs...)
	changed[1] = user("rewritten")
	d := Compare(prev, Compute(body(changed...)))
	if d.Cause != CauseHead {
		t.Errorf("cause = %q, want head", d.Cause)
	}
	if d.DivergedAt != 1 {
		t.Errorf("diverged at %d, want 1", d.DivergedAt)
	}
}

// A sliding context window shares no opening with its predecessor, so without the
// suffix check it is indistinguishable from an unrelated prompt - and the client
// would never learn that its own trimming is what costs it the cache.
func TestSlidingWindowIsNotMistakenForANewConversation(t *testing.T) {
	full := []msg{sys("s"), user("a"), asst("b"), user("c"), asst("d"), user("e")}
	prev := Compute(body(full...))
	cur := Compute(body(full[3:]...))
	d := Compare(prev, cur)
	if d.Cause != CauseTruncated {
		t.Errorf("cause = %q, want truncated", d.Cause)
	}
	if !d.Costly() {
		t.Error("a window slide forfeits the whole prefix and is costly")
	}
}

func TestUnrelatedPromptIsANewConversation(t *testing.T) {
	prev := Compute(body(sys("s"), user("a"), asst("b")))
	cur := Compute(body(sys("other"), user("z"), asst("y")))
	d := Compare(prev, cur)
	if d.Cause != CauseNewConversation {
		t.Errorf("cause = %q, want new_conversation", d.Cause)
	}
	if d.Costly() {
		t.Error("a genuinely new conversation is not a fault")
	}
}

// Retention is what decides the cost of a miss, and message sizes differ by orders
// of magnitude, so it has to be measured in bytes rather than message count.
func TestRetentionIsWeightedByBytesNotMessageCount(t *testing.T) {
	big := user(fmt.Sprintf("%040000d", 1)) // one very large tool-result-sized turn
	prev := Compute(body(sys("s"), big, user("x"), user("y"), user("z")))
	cur := Compute(body(sys("s"), big, user("x"), user("y"), user("CHANGED")))
	d := Compare(prev, cur)
	if d.Retained < 0.99 {
		t.Errorf("retained = %.4f; four of five messages survive and they hold "+
			"nearly all the bytes, so retention should be near 1", d.Retained)
	}
}

func TestUnparseableBodyIsNotReportedAsAFault(t *testing.T) {
	if f := Compute([]byte("not json")); f.Len() != 0 {
		t.Errorf("expected an empty fingerprint, got %d messages", f.Len())
	}
	d := Compare(Fingerprint{}, Compute(body(user("a"))))
	if d.Costly() {
		t.Error("missing information must never be reported as a rewrite")
	}
}

func TestToolCallsParticipateInTheHash(t *testing.T) {
	a := []byte(`{"messages":[{"role":"assistant","tool_calls":[{"id":"1"}]}]}`)
	b := []byte(`{"messages":[{"role":"assistant","tool_calls":[{"id":"2"}]}]}`)
	if Compute(a).Hashes[0] == Compute(b).Hashes[0] {
		t.Error("differing tool calls must produce differing hashes; they are part " +
			"of the cached prefix")
	}
}

// ─── Tracker ──────────────────────────────────────────────────────────────────

func TestTrackerFollowsAGrowingConversation(t *testing.T) {
	tr := NewTracker()
	msgs := []msg{sys("s"), user("a")}
	if d := tr.Observe("k", Compute(body(msgs...))); d.Cause != CauseNewConversation {
		t.Errorf("first sighting = %q, want new_conversation", d.Cause)
	}
	for i := 0; i < 8; i++ {
		msgs = append(msgs, asst(fmt.Sprintf("r%d", i)), user(fmt.Sprintf("q%d", i)))
		d := tr.Observe("k", Compute(body(msgs...)))
		if d.Cause != CauseAppend {
			t.Fatalf("turn %d: cause = %q, want append", i, d.Cause)
		}
	}
}

// The reason the tracker does not key off relai's session key: that key folds in the
// system prompt, so an unstable system prompt changes it every turn and the fault
// hides itself. Matching by common prefix has to find the predecessor anyway.
func TestTrackerCatchesAnUnstableSystemPromptAcrossTurns(t *testing.T) {
	tr := NewTracker()
	tr.Observe("k", Compute(body(sys("t=1"), user("a"), asst("b"))))
	d := tr.Observe("k", Compute(body(sys("t=2"), user("a"), asst("b"), user("c"))))
	if d.Cause != CauseSystem {
		t.Errorf("cause = %q, want system", d.Cause)
	}
}

func TestTrackerKeepsConversationsApart(t *testing.T) {
	tr := NewTracker()
	tr.Observe("alice", Compute(body(sys("s"), user("alice question"))))
	d := tr.Observe("bob", Compute(body(sys("s"), user("bob question"))))
	if d.Cause != CauseNewConversation {
		t.Errorf("bob's first request diagnosed as %q; keys must not be compared "+
			"against each other", d.Cause)
	}
}

func TestTrackerMemoryIsBounded(t *testing.T) {
	tr := NewTracker()
	for i := 0; i < maxTracked*3; i++ {
		tr.Observe(fmt.Sprintf("k%d", i), Compute(body(user(fmt.Sprintf("q%d", i)))))
	}
	if len(tr.entries) > maxTracked {
		t.Errorf("tracker holds %d entries, cap is %d", len(tr.entries), maxTracked)
	}
}

func TestTrackerIsSafeUnderConcurrency(t *testing.T) {
	tr := NewTracker()
	done := make(chan struct{})
	for w := 0; w < 8; w++ {
		go func(w int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 50; i++ {
				tr.Observe(fmt.Sprintf("k%d", w), Compute(body(user(fmt.Sprintf("q%d", i)))))
			}
		}(w)
	}
	for w := 0; w < 8; w++ {
		<-done
	}
}

// ─── Classification ───────────────────────────────────────────────────────────

func TestClassifyBlamesTheHostWhenHistoryWasIntact(t *testing.T) {
	if got := Classify(CauseAppend, 100000, 1000, true); got != CauseHostChange {
		t.Errorf("got %q, want host_change: intact history that spilled to another backend", got)
	}
	if got := Classify(CauseAppend, 100000, 1000, false); got != CauseEvicted {
		t.Errorf("got %q, want evicted: intact history, same backend, no reuse", got)
	}
}

func TestClassifyLeavesHealthyReuseAlone(t *testing.T) {
	if got := Classify(CauseAppend, 100000, 95000, false); got != CauseAppend {
		t.Errorf("got %q, want append: the cache was reused", got)
	}
}

// A rewrite already explains itself; the host must not be blamed for it.
func TestClassifyDoesNotOverrideARewrite(t *testing.T) {
	if got := Classify(CauseSystem, 100000, 0, true); got != CauseSystem {
		t.Errorf("got %q, want system", got)
	}
}

// Short prompts reprocess in moments and their reuse ratios are noisy, so blaming
// anything for them would bury the expensive cases in false positives.
func TestClassifyIgnoresShallowPrompts(t *testing.T) {
	if got := Classify(CauseAppend, 200, 0, false); got != CauseAppend {
		t.Errorf("got %q, want append: too shallow to attribute", got)
	}
}

func TestBlamesSeparatesFaultsFromHealthyTraffic(t *testing.T) {
	for _, c := range []string{CauseSystem, CauseHead, CauseTail, CauseTruncated,
		CauseHostChange, CauseEvicted} {
		if !Blames(c) {
			t.Errorf("%q should count as a fault", c)
		}
	}
	for _, c := range []string{CauseAppend, CauseNewConversation, ""} {
		if Blames(c) {
			t.Errorf("%q must not count as a fault", c)
		}
	}
}
