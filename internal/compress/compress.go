// Package compress shrinks oversized message content before it is forwarded
// upstream, without disturbing the KV-cache prefix that makes multi-turn
// conversations cheap.
//
// The rule that makes this safe is that every message is compressed in isolation,
// as a pure function of its own content. A message therefore produces identical
// bytes no matter what surrounds it or how many turns follow it, so a
// conversation's prefix stays byte-stable as it grows and the upstream keeps
// hitting its cache.
//
// This is deliberately weaker than compressors that look across the whole
// conversation: cross-message redundancy is left on the table. That trade is the
// point. Measured on this deployment a warm turn costs ~1.7s against ~29s cold,
// so one busted prefix costs far more than any ratio gained by rewriting history.
// Anything that cannot guarantee prefix stability does not belong here.
package compress

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Mode selects how aggressively content is reduced.
type Mode string

const (
	// ModeOff disables compression.
	ModeOff Mode = ""
	// ModeLossless compacts JSON whitespace. Key order, values and structure are
	// untouched, so the upstream sees semantically identical content.
	ModeLossless Mode = "lossless"
	// ModeAggressive additionally caps long top-level JSON arrays, replacing the
	// tail with a marker recording how many items were elided. Lossy, so it is
	// opt-in per target.
	ModeAggressive Mode = "aggressive"
)

// ParseMode maps stored configuration to a Mode, defaulting to off.
func ParseMode(s string) Mode {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case string(ModeLossless), "json", "compact":
		return ModeLossless
	case string(ModeAggressive), "json-aggressive":
		return ModeAggressive
	default:
		return ModeOff
	}
}

const (
	// MinBytes is the size below which compression is not attempted: small
	// messages cannot save enough to be worth the cycles.
	MinBytes = 512
	// MaxArrayItems is how many leading elements ModeAggressive keeps.
	MaxArrayItems = 25
)

// Stats reports what a pass achieved, for request logging.
type Stats struct {
	MessagesCompressed int
	BytesBefore        int
	BytesAfter         int
}

// Saved returns the number of bytes removed.
func (s Stats) Saved() int { return s.BytesBefore - s.BytesAfter }

// Compressor applies a Mode to message content, memoising results by content
// hash. The cache is an optimisation only: the transform is a pure function, so
// a cold cache produces byte-identical output to a warm one.
type Compressor struct {
	mode  Mode
	mu    sync.RWMutex
	cache map[[32]byte]string
	limit int
}

// New returns a Compressor for the given mode. An unknown mode yields one that
// leaves everything alone.
func New(mode Mode) *Compressor {
	return &Compressor{mode: mode, cache: make(map[[32]byte]string), limit: 4096}
}

// Enabled reports whether this Compressor will change anything.
func (c *Compressor) Enabled() bool { return c != nil && c.mode != ModeOff }

// Content compresses a single message body. The second return reports whether
// the content changed.
func (c *Compressor) Content(s string) (string, bool) {
	if !c.Enabled() || len(s) < MinBytes {
		return s, false
	}
	key := sha256.Sum256([]byte(s))

	c.mu.RLock()
	got, ok := c.cache[key]
	c.mu.RUnlock()
	if ok {
		return got, got != s
	}

	out := c.transform(s)
	// Never let compression make a message larger.
	if len(out) >= len(s) {
		out = s
	}

	c.mu.Lock()
	if len(c.cache) >= c.limit {
		// Cheap reset rather than an LRU: the transform is pure, so dropping the
		// cache costs recomputation and nothing else.
		c.cache = make(map[[32]byte]string, c.limit)
	}
	c.cache[key] = out
	c.mu.Unlock()
	return out, out != s
}

func (c *Compressor) transform(s string) string {
	// Tool output is rarely bare JSON - it usually arrives with a line of framing
	// in front of it ("Results:\n[...]"). Locate the JSON span and rewrite only
	// that, leaving the surrounding text exactly as it was.
	start, end, ok := jsonSpan(s)
	if !ok {
		return s
	}
	span := s[start:end]

	if c.mode == ModeAggressive {
		if capped, changed := capTopLevelArray(span, MaxArrayItems); changed {
			span = capped
		}
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(span)); err != nil {
		return s
	}
	return s[:start] + buf.String() + s[end:]
}

// jsonSpan returns the bounds of the JSON value in s, allowing for text before and
// after it. It looks for the first opening bracket and the matching final closing
// one, and only reports a span that actually parses - so prose containing a stray
// brace is left alone.
func jsonSpan(s string) (int, int, bool) {
	start := strings.IndexAny(s, "[{")
	if start < 0 {
		return 0, 0, false
	}
	open := s[start]
	close := byte(']')
	if open == '{' {
		close = '}'
	}
	end := strings.LastIndexByte(s, close)
	if end < start {
		return 0, 0, false
	}
	end++ // half-open
	if end-start < MinBytes/2 {
		return 0, 0, false // too small to be worth rewriting
	}
	if !json.Valid([]byte(s[start:end])) {
		return 0, 0, false
	}
	return start, end, true
}

// capTopLevelArray keeps the first max elements of a JSON array and records how
// many were dropped. Elements are carried as raw messages so their key order and
// inner formatting survive untouched, which is what keeps the result
// deterministic for a given input.
func capTopLevelArray(s string, max int) (string, bool) {
	if len(s) == 0 || s[0] != '[' {
		return s, false
	}
	var items []json.RawMessage
	if err := json.Unmarshal([]byte(s), &items); err != nil {
		return s, false
	}
	if len(items) <= max {
		return s, false
	}
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, it := range items[:max] {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(it)
	}
	marker, err := json.Marshal(map[string]any{
		"_elided_items": len(items) - max,
		"_note": fmt.Sprintf("%d of %d items omitted by relai compression",
			len(items)-max, len(items)),
	})
	if err != nil {
		return s, false
	}
	buf.WriteByte(',')
	buf.Write(marker)
	buf.WriteByte(']')
	return buf.String(), true
}
