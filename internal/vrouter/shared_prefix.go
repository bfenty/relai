package vrouter

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/bfenty/relai/internal/db"
)

// Shared-prefix placement.
//
// Session affinity keeps a conversation on the host that holds ITS cache. It says
// nothing about a conversation's first turn, which is placed by load alone. Yet
// first turns are rarely cold in substance: an agent framework spawns sub-agents
// that all open with the same tool definitions and system prompt, routinely 10-30k
// tokens. At this hardware's ~250 tok/s prefill that is one to two minutes of GPU
// time per sub-agent, paid again on every host the siblings get spread across.
//
// So the router remembers, per shared prefix, the host that last processed it (its
// "home"), and a conversation with no host of its own yet - or one spilling off a
// busy one - goes there when the home can take it. This only helps an engine that
// reuses a common prompt prefix across requests (llama.cpp slots and prompt cache,
// Halogen's prefix cache); the `prefix_home` routing decision's cache-reuse rate in
// analytics shows whether it does.

// minSharedPrefixBytes is the smallest shared prefix worth steering for: ~1k
// tokens, a few seconds of prefill. Below it load balance matters more.
const minSharedPrefixBytes = 4096

// maxPrefixHomes bounds the table. Distinct system prompts are few; this is a
// guard against a client that stamps something unique into every one.
const maxPrefixHomes = 4096

// prefixHomeTTL is how long a home is trusted without being refreshed. An idle
// engine's cache gets recycled by other traffic; past this, placing by load is as
// good a guess.
const prefixHomeTTL = time.Hour

// sharedPrefix identifies the part of a request that other conversations can
// share: the tool definitions and the leading system/developer messages, which
// chat templates render first.
type sharedPrefix struct {
	key   string // hash of the prefix; "" when there is none worth steering for
	bytes int    // its size, for pricing a reprocess
}

type prefixHome struct {
	modelMapID int64
	lastSeen   time.Time
}

type prefixHomes struct {
	mu    sync.Mutex
	homes map[string]prefixHome // "routerID|key" → home
}

func newPrefixHomes() *prefixHomes {
	return &prefixHomes{homes: make(map[string]prefixHome)}
}

func homeKey(routerID int64, key string) string {
	return fmt.Sprintf("%d|%s", routerID, key)
}

func (p *prefixHomes) get(routerID int64, key string) int64 {
	if p == nil || key == "" {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	h, ok := p.homes[homeKey(routerID, key)]
	if !ok || time.Since(h.lastSeen) > prefixHomeTTL {
		return 0
	}
	return h.modelMapID
}

func (p *prefixHomes) set(routerID int64, key string, modelMapID int64) {
	if p == nil || key == "" || modelMapID == 0 {
		return
	}
	k := homeKey(routerID, key)
	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.homes[k]; !ok && len(p.homes) >= maxPrefixHomes {
		p.evictOldestLocked()
	}
	p.homes[k] = prefixHome{modelMapID: modelMapID, lastSeen: now}
}

func (p *prefixHomes) evictOldestLocked() {
	var oldestKey string
	var oldest time.Time
	for k, h := range p.homes {
		if oldestKey == "" || h.lastSeen.Before(oldest) {
			oldestKey, oldest = k, h.lastSeen
		}
	}
	delete(p.homes, oldestKey)
}

// prune drops homes not refreshed within prefixHomeTTL.
func (p *prefixHomes) prune() {
	if p == nil {
		return
	}
	cutoff := time.Now().Add(-prefixHomeTTL)
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, h := range p.homes {
		if h.lastSeen.Before(cutoff) {
			delete(p.homes, k)
		}
	}
}

// sharedPrefixOf hashes a request's tools and leading system/developer messages.
// Tools are compacted first, so whitespace differences between otherwise
// identical definitions don't split a prefix in two.
func sharedPrefixOf(tools json.RawMessage, messages []routingMessage) sharedPrefix {
	h := sha256.New()
	size := 0
	if len(tools) > 0 && !bytes.Equal(bytes.TrimSpace(tools), []byte("null")) {
		var compact bytes.Buffer
		if err := json.Compact(&compact, tools); err == nil {
			tools = compact.Bytes()
		}
		h.Write([]byte("tools:"))
		h.Write(tools)
		size += len(tools)
	}
	for _, msg := range messages {
		if msg.Role != "system" && msg.Role != "developer" {
			break
		}
		content := stableContentBytes(msg.Content)
		fmt.Fprintf(h, "\x00%s:", msg.Role)
		h.Write(content)
		size += len(content)
	}
	if size < minSharedPrefixBytes {
		return sharedPrefix{}
	}
	return sharedPrefix{key: hex.EncodeToString(h.Sum(nil)[:16]), bytes: size}
}

// tryAcquirePrefixHome takes the shared prefix's home host when that is the
// better first placement, returning its index or -1 to fall back to load-based
// placement. The home must have a free slot; and if it is already working while
// another host sits idle, it is used only when rebuilding the prefix elsewhere
// would cost more than spillImmediateBudget - otherwise an idle machine is the
// better use of the fleet.
func (m *Manager) tryAcquirePrefixHome(routerID int64, states []*memberState, members []db.RouterMember, excluded map[int]bool, shared sharedPrefix) int {
	home := m.prefixHomes.get(routerID, shared.key)
	if home == 0 || len(states) != len(members) {
		return -1
	}
	for i, mem := range members {
		if mem.ModelMapID != home {
			continue
		}
		if (excluded != nil && excluded[i]) || states[i].getHealth() == "offline" {
			return -1
		}
		active, _, capacity := states[i].slot.Load()
		if active >= capacity {
			return -1
		}
		if active > 0 && anyOtherMemberIdle(states, i, excluded) {
			rate := m.hostPrefillRate(states[i])
			rebuild := time.Duration(float64(shared.bytes) / bytesPerToken / rate * float64(time.Second))
			if rebuild < spillImmediateBudget {
				return -1
			}
		}
		if !states[i].slot.TryAcquire() {
			return -1
		}
		return i
	}
	return -1
}

// anyOtherMemberIdle reports whether some member other than skip has nothing
// running at all.
func anyOtherMemberIdle(states []*memberState, skip int, excluded map[int]bool) bool {
	for i, s := range states {
		if i == skip || (excluded != nil && excluded[i]) || s.getHealth() == "offline" {
			continue
		}
		if active, _, capacity := s.slot.Load(); active == 0 && capacity > 0 {
			return true
		}
	}
	return false
}
