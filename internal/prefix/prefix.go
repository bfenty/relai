// Package prefix diagnoses why a request missed the backend's KV cache.
//
// At deep context a prompt that misses the cache costs a full reprocess - at the
// ~259 tok/s prefill this hardware sustains, a 180k-token conversation is roughly
// eleven minutes of GPU time. cached_tokens already tells us a miss happened, but
// not why, which leaves the expensive question unanswered: was the client's history
// rewritten, did the router move the conversation to a cold host, or did the host
// simply recycle the slot?
//
// This package answers the first of those. It fingerprints each request as a vector
// of per-message hashes - never the content itself, so nothing here changes what
// relai stores - and compares a new request against recent ones. The longest common
// prefix is exactly the portion the backend can still reuse, so where the vectors
// first differ is where the cache was invalidated, and everything after it is the
// cost.
package prefix

import (
	"encoding/json"
	"hash/fnv"
	"sync"
)

// Fingerprint is one request reduced to per-message hashes.
//
// CumBytes[i] is the total size of messages 0..i. Retention is measured in bytes
// rather than message count because message sizes vary by orders of magnitude - a
// rewrite of one 40 KB tool result costs far more than a rewrite of ten short turns,
// and a count would rank them the same way.
type Fingerprint struct {
	Hashes   []uint64
	CumBytes []int64
	Roles    []string
}

func (f Fingerprint) Len() int { return len(f.Hashes) }

func (f Fingerprint) bytes() int64 {
	if len(f.CumBytes) == 0 {
		return 0
	}
	return f.CumBytes[len(f.CumBytes)-1]
}

// bytesThrough returns the size of the first n messages.
func (f Fingerprint) bytesThrough(n int) int64 {
	if n <= 0 || len(f.CumBytes) == 0 {
		return 0
	}
	if n > len(f.CumBytes) {
		n = len(f.CumBytes)
	}
	return f.CumBytes[n-1]
}

// maxMessages caps what a single fingerprint retains. Conversations longer than this
// are rare, and the cap keeps the tracker's memory bounded regardless of traffic.
const maxMessages = 2048

// Compute fingerprints a chat-completions request body. A body that cannot be parsed
// as a message array yields an empty fingerprint, which every comparison treats as
// "no information" rather than as a difference.
func Compute(body []byte) Fingerprint {
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
			// Tool calls are part of the prefix the backend caches, so they have to
			// be hashed too; a retried call with different arguments invalidates the
			// cache just as a changed message would.
			ToolCalls json.RawMessage `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil || len(req.Messages) == 0 {
		return Fingerprint{}
	}

	n := len(req.Messages)
	if n > maxMessages {
		n = maxMessages
	}
	f := Fingerprint{
		Hashes:   make([]uint64, 0, n),
		CumBytes: make([]int64, 0, n),
		Roles:    make([]string, 0, n),
	}
	var cum int64
	for _, m := range req.Messages[:n] {
		h := fnv.New64a()
		h.Write([]byte(m.Role))
		h.Write([]byte{0})
		h.Write(m.Content)
		h.Write([]byte{0})
		h.Write(m.ToolCalls)
		cum += int64(len(m.Role) + len(m.Content) + len(m.ToolCalls))
		f.Hashes = append(f.Hashes, h.Sum64())
		f.CumBytes = append(f.CumBytes, cum)
		f.Roles = append(f.Roles, m.Role)
	}
	return f
}

// Causes of a cache miss, ordered roughly by how actionable they are.
const (
	// CauseAppend means history was untouched and only new messages were added.
	// This is the healthy shape: a miss alongside it is not the client's doing.
	CauseAppend = "append"
	// CauseSystem means a system or developer message changed, invalidating the
	// entire prompt. Almost always a timestamp, a session id or a reordered block
	// injected into the system prompt, and almost always worth fixing.
	CauseSystem = "system"
	// CauseHead means an early non-system message was rewritten.
	CauseHead = "head"
	// CauseTail means only the last message or two changed - a retried tool call or
	// an edited final turn. Cheap, and usually legitimate.
	CauseTail = "tail"
	// CauseTruncated means the client dropped messages from the front, so the prompt
	// is a suffix of what it sent before. A sliding context window does this, and it
	// forfeits the whole cached prefix every time it slides.
	CauseTruncated = "truncated"
	// CauseNewConversation means nothing recent resembles this prompt. Cold by
	// nature, not a fault.
	CauseNewConversation = "new_conversation"
)

// Diff is the verdict for one request.
type Diff struct {
	Cause string
	// DivergedAt is the index of the first message that differs, which is also the
	// number of messages the backend can still reuse.
	DivergedAt int
	PrevLen    int
	NewLen     int
	// Retained is the fraction of the previous prompt's bytes that survive as a
	// reusable prefix - the ceiling on what cached_tokens can report.
	Retained float64
	// NewBytes is the total size of the current request's messages. Together
	// with Retained it estimates what the conversation needs resident in the
	// backend's KV pool (see #19: session footprint tracking).
	NewBytes int64
}

// Compare diagnoses cur against the closest prior fingerprint prev.
func Compare(prev, cur Fingerprint) Diff {
	if prev.Len() == 0 || cur.Len() == 0 {
		return Diff{Cause: CauseNewConversation, NewLen: cur.Len(), PrevLen: prev.Len()}
	}

	lcp := commonPrefix(prev, cur)
	d := Diff{DivergedAt: lcp, PrevLen: prev.Len(), NewLen: cur.Len()}
	if prev.bytes() > 0 {
		d.Retained = float64(prev.bytesThrough(lcp)) / float64(prev.bytes())
	}

	switch {
	case lcp == prev.Len():
		// Everything previously sent is still there; the rest is new.
		d.Cause = CauseAppend
		d.Retained = 1
	case lcp == 0:
		// Nothing matches from the start, which has three very different meanings.
		//
		// The costliest is an unstable system prompt: divergence at index 0 forfeits
		// the entire prefix however long the conversation has grown. It is told apart
		// from an unrelated prompt by what follows - if the conversation after the
		// opening still lines up, this is the same conversation with a rewritten
		// system message, not a different one.
		if isSystemRole(prev.Roles, 0) && isSystemRole(cur.Roles, 0) &&
			commonPrefixFrom(prev, cur, 1) >= 1 {
			d.Cause = CauseSystem
			d.Retained = 0
			return d
		}
		// Next, a sliding context window, which also shares no opening and would
		// otherwise be indistinguishable from an unrelated prompt.
		if off := suffixOffset(prev, cur); off > 0 {
			d.Cause = CauseTruncated
			d.Retained = 0
			return d
		}
		d.Cause = CauseNewConversation
	case isSystemRole(prev.Roles, lcp) || isSystemRole(cur.Roles, lcp):
		d.Cause = CauseSystem
	case lcp >= prev.Len()-2:
		d.Cause = CauseTail
	default:
		d.Cause = CauseHead
	}
	return d
}

// Costly reports whether a diff describes a rewrite worth surfacing. Appends are
// healthy, and a new conversation has no prefix to lose.
func (d Diff) Costly() bool {
	switch d.Cause {
	case CauseAppend, CauseNewConversation:
		return false
	default:
		return true
	}
}

func commonPrefix(a, b Fingerprint) int {
	n := a.Len()
	if b.Len() < n {
		n = b.Len()
	}
	for i := 0; i < n; i++ {
		if a.Hashes[i] != b.Hashes[i] {
			return i
		}
	}
	return n
}

// commonPrefixFrom counts matching messages starting at the given offset in both
// fingerprints, used to decide whether two prompts are the same conversation once a
// rewritten opening is set aside.
func commonPrefixFrom(a, b Fingerprint, off int) int {
	n := a.Len() - off
	if m := b.Len() - off; m < n {
		n = m
	}
	for i := 0; i < n; i++ {
		if a.Hashes[off+i] != b.Hashes[off+i] {
			return i
		}
	}
	if n < 0 {
		return 0
	}
	return n
}

// suffixOffset reports how many leading messages of prev were dropped for cur to
// become a suffix of it, or 0 if cur is not a suffix.
func suffixOffset(prev, cur Fingerprint) int {
	if cur.Len() == 0 || cur.Len() > prev.Len() {
		return 0
	}
	for off := 1; off+cur.Len() <= prev.Len()+1 && off < prev.Len(); off++ {
		match := true
		for i := 0; i < cur.Len() && off+i < prev.Len(); i++ {
			if prev.Hashes[off+i] != cur.Hashes[i] {
				match = false
				break
			}
		}
		// Require a few messages of agreement so an incidental single-message match
		// is not mistaken for a window slide.
		if match && cur.Len() >= 2 {
			return off
		}
	}
	return 0
}

func isSystemRole(roles []string, i int) bool {
	if i < 0 || i >= len(roles) {
		return false
	}
	return roles[i] == "system" || roles[i] == "developer"
}

// ─── Tracker ──────────────────────────────────────────────────────────────────

// maxTracked bounds how many recent conversations are held. Each entry is a few KB
// at worst, so this stays comfortably small while covering far more concurrent
// conversations than the backends can serve.
const maxTracked = 256

// Tracker remembers recent fingerprints and matches new requests against them.
//
// It deliberately does not key off relai's session key. That key folds in the system
// prompt, so a client that puts a timestamp in its system message gets a brand new
// key on every request - which is precisely the fault worth catching, and would be
// invisible to anything that trusted the key. Matching by best common prefix finds
// the predecessor regardless.
type Tracker struct {
	mu      sync.Mutex
	entries []entry
	seq     uint64
}

type entry struct {
	key  string
	fp   Fingerprint
	used uint64
}

func NewTracker() *Tracker { return &Tracker{} }

// Observe records a request and returns the diagnosis for it. The key scopes the
// search - two clients with unrelated conversations should not be compared - and an
// empty key still works, just against a wider pool.
func (t *Tracker) Observe(key string, fp Fingerprint) Diff {
	if fp.Len() == 0 {
		return Diff{Cause: CauseNewConversation}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seq++

	best := Diff{Cause: CauseNewConversation, NewLen: fp.Len()}
	bestIdx := -1
	bestScore := 0
	for i := range t.entries {
		if t.entries[i].key != key {
			continue
		}
		d := Compare(t.entries[i].fp, fp)
		// Prefer the predecessor sharing the most messages; that is the one whose
		// KV prefix the backend would actually have been holding.
		score := d.DivergedAt
		if d.Cause == CauseTruncated && score == 0 {
			score = 1 // a detected window slide beats no match at all
		}
		if score > bestScore || (bestIdx == -1 && d.Cause != CauseNewConversation) {
			bestScore, bestIdx, best = score, i, d
		}
	}

	if bestIdx >= 0 {
		// Replace the matched predecessor: the conversation has moved on, and keeping
		// the older vector would only re-diagnose the same divergence next turn.
		t.entries[bestIdx].fp = fp
		t.entries[bestIdx].used = t.seq
		best.NewBytes = fp.bytes()
		return best
	}
	t.insert(entry{key: key, fp: fp, used: t.seq})
	best.NewBytes = fp.bytes()
	return best
}

func (t *Tracker) insert(e entry) {
	if len(t.entries) < maxTracked {
		t.entries = append(t.entries, e)
		return
	}
	oldest := 0
	for i := range t.entries {
		if t.entries[i].used < t.entries[oldest].used {
			oldest = i
		}
	}
	t.entries[oldest] = e
}

// ─── Classification against the observed outcome ──────────────────────────────

// Causes that only become visible once the response reports cached_tokens. A prompt
// whose history was never touched can still miss the cache, and the reason matters:
// one is the router's doing and one is the backend's.
const (
	// CauseHostChange means intact history that landed on a different backend than
	// the conversation had been using, so no prefix was there to reuse.
	CauseHostChange = "host_change"
	// CauseEvicted means intact history, the same backend, and still no reuse - the
	// slot holding the prefix was recycled. This is the signal that the backend is
	// juggling more conversations than it has slots or cache to hold.
	CauseEvicted = "evicted"
)

// Thresholds mirror the routing analytics so the two views agree on what counts as a
// cold, deep request.
const (
	coldReuseThreshold = 0.5
	deepTokenThreshold = 2000
)

// Classify refines a request-time diagnosis using the reuse the backend actually
// reported. A rewrite is its own explanation and passes through unchanged; an intact
// history that still missed the cache is attributed to the host instead of the
// client, which is the distinction that decides who can fix it.
func Classify(cause string, promptTokens, cachedTokens int, spilled bool) string {
	if cause != CauseAppend {
		return cause
	}
	// Shallow prompts are cheap to reprocess and their reuse ratios are noisy, so
	// they are not worth attributing.
	if promptTokens < deepTokenThreshold {
		return cause
	}
	if float64(cachedTokens)/float64(promptTokens) >= coldReuseThreshold {
		return cause
	}
	if spilled {
		return CauseHostChange
	}
	return CauseEvicted
}

// Blames reports whether a cause identifies a specific, fixable fault, as opposed to
// healthy traffic or a conversation seen for the first time.
func Blames(cause string) bool {
	switch cause {
	case CauseSystem, CauseHead, CauseTail, CauseTruncated, CauseHostChange, CauseEvicted:
		return true
	default:
		return false
	}
}
