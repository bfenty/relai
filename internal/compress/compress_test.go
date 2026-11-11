package compress

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func bigJSONArray(n int) string {
	items := make([]map[string]any, n)
	for i := range items {
		items[i] = map[string]any{
			"file":    fmt.Sprintf("src/mod_%d.py", i),
			"line":    i * 7,
			"symbol":  fmt.Sprintf("handler_%d", i),
			"snippet": "def handler(): return None  # boilerplate",
		}
	}
	b, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(b)
}

// The property the whole package exists for: a message compresses to identical
// bytes regardless of what surrounds it. Without this, a conversation's prefix
// changes as it grows and the upstream loses its KV cache - which costs far more
// than compression saves.
func TestCompressionIsPrefixStable(t *testing.T) {
	for _, mode := range []Mode{ModeLossless, ModeAggressive} {
		t.Run(string(mode), func(t *testing.T) {
			payload := bigJSONArray(120)

			// Turn 1: the payload is the newest message.
			c1 := New(mode)
			asLive, _ := c1.Content(payload)

			// Turn 5: the same payload is now buried in history, on a fresh
			// process with a cold cache.
			c2 := New(mode)
			for i := range 4 {
				c2.Content(bigJSONArray(10 + i)) // unrelated traffic in between
			}
			asHistory, _ := c2.Content(payload)

			if asLive != asHistory {
				t.Fatalf("same content compressed differently by position: %d vs %d bytes",
					len(asLive), len(asHistory))
			}
		})
	}
}

// Same input, same output - across separate Compressors and a warm cache.
func TestDeterministic(t *testing.T) {
	payload := bigJSONArray(80)
	a, _ := New(ModeAggressive).Content(payload)
	b, _ := New(ModeAggressive).Content(payload)
	warm := New(ModeAggressive)
	warm.Content(payload)
	cached, _ := warm.Content(payload)
	if a != b || a != cached {
		t.Fatalf("non-deterministic output: fresh=%d fresh2=%d cached=%d", len(a), len(b), len(cached))
	}
}

// Lossless mode must preserve meaning exactly: same values, same key order.
func TestLosslessPreservesSemantics(t *testing.T) {
	payload := bigJSONArray(40)
	out, changed := New(ModeLossless).Content(payload)
	if !changed {
		t.Fatal("expected pretty-printed JSON to compact")
	}
	if len(out) >= len(payload) {
		t.Errorf("no saving: %d -> %d", len(payload), len(out))
	}
	var before, after []map[string]any
	if err := json.Unmarshal([]byte(payload), &before); err != nil {
		t.Fatalf("input unparseable: %v", err)
	}
	if err := json.Unmarshal([]byte(out), &after); err != nil {
		t.Fatalf("output unparseable: %v", err)
	}
	if len(before) != len(after) {
		t.Fatalf("lossless mode dropped items: %d -> %d", len(before), len(after))
	}
	for i := range before {
		if fmt.Sprint(before[i]) != fmt.Sprint(after[i]) {
			t.Fatalf("item %d changed", i)
		}
	}
}

// Aggressive mode caps long arrays and says so, rather than silently truncating.
func TestAggressiveCapsArraysAndRecordsElision(t *testing.T) {
	payload := bigJSONArray(120)
	out, changed := New(ModeAggressive).Content(payload)
	if !changed {
		t.Fatal("expected a long array to be capped")
	}
	var items []json.RawMessage
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("output unparseable: %v", err)
	}
	if len(items) != MaxArrayItems+1 {
		t.Fatalf("expected %d kept items plus a marker, got %d", MaxArrayItems, len(items))
	}
	if !strings.Contains(out, "_elided_items") {
		t.Error("no elision marker in output")
	}
	if !strings.Contains(out, "95 of 120 items omitted") {
		t.Errorf("marker does not record the counts: %s", out[len(out)-160:])
	}
}

// Short messages and prose are left exactly as they are.
func TestLeavesSmallAndNonJSONAlone(t *testing.T) {
	c := New(ModeAggressive)
	small := `{"a":1}`
	if out, changed := c.Content(small); changed || out != small {
		t.Errorf("small message altered: %q", out)
	}
	prose := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 40)
	if out, changed := c.Content(prose); changed || out != prose {
		t.Error("prose altered")
	}
	broken := "{" + strings.Repeat("not json ", 100)
	if out, changed := c.Content(broken); changed || out != broken {
		t.Error("invalid JSON altered")
	}
}

// A disabled compressor is a no-op.
func TestOffModeDoesNothing(t *testing.T) {
	payload := bigJSONArray(120)
	c := New(ParseMode(""))
	if c.Enabled() {
		t.Fatal("off mode reports enabled")
	}
	if out, changed := c.Content(payload); changed || out != payload {
		t.Error("off mode modified content")
	}
}

// Compression must never inflate a message.
func TestNeverGrows(t *testing.T) {
	// Already-compact JSON has no whitespace to remove.
	compact := `[` + strings.Repeat(`{"a":1},`, 200) + `{"a":1}]`
	out, _ := New(ModeLossless).Content(compact)
	if len(out) > len(compact) {
		t.Errorf("output grew: %d -> %d", len(compact), len(out))
	}
}

func TestParseMode(t *testing.T) {
	cases := map[string]Mode{
		"": ModeOff, "off": ModeOff, "nonsense": ModeOff,
		"lossless": ModeLossless, "json": ModeLossless, "COMPACT": ModeLossless,
		"aggressive": ModeAggressive, "json-aggressive": ModeAggressive,
	}
	for in, want := range cases {
		if got := ParseMode(in); got != want {
			t.Errorf("ParseMode(%q) = %q, want %q", in, got, want)
		}
	}
}

// The cache is shared across goroutines; concurrent use must stay correct.
func TestConcurrentUseIsConsistent(t *testing.T) {
	c := New(ModeAggressive)
	payload := bigJSONArray(120)
	want, _ := New(ModeAggressive).Content(payload)

	var wg sync.WaitGroup
	errs := make([]string, 32)
	for i := range 32 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, _ := c.Content(payload)
			if got != want {
				errs[i] = fmt.Sprintf("worker %d got %d bytes, want %d", i, len(got), len(want))
			}
		}(i)
	}
	wg.Wait()
	for _, e := range errs {
		if e != "" {
			t.Error(e)
		}
	}
}

// Tool output usually arrives framed by text ("Results:\n[...]"). The JSON inside
// must still be compressed, and the framing preserved byte-for-byte.
func TestCompressesJSONEmbeddedInText(t *testing.T) {
	payload := "Search results follow:\n" + bigJSONArray(120) + "\nEnd of results."
	out, changed := New(ModeAggressive).Content(payload)
	if !changed {
		t.Fatal("embedded JSON was not compressed")
	}
	if !strings.HasPrefix(out, "Search results follow:\n") {
		t.Error("leading text not preserved")
	}
	if !strings.HasSuffix(out, "\nEnd of results.") {
		t.Error("trailing text not preserved")
	}
	if len(out) >= len(payload) {
		t.Errorf("no saving: %d -> %d", len(payload), len(out))
	}
}

// Prose containing a stray bracket must not be mistaken for JSON.
func TestProseWithBracketsUntouched(t *testing.T) {
	prose := strings.Repeat("See item [1] and note {this} carefully. ", 40)
	out, changed := New(ModeAggressive).Content(prose)
	if changed || out != prose {
		t.Error("prose with brackets was altered")
	}
}

// Embedded compression must stay position-independent too.
func TestEmbeddedJSONIsPrefixStable(t *testing.T) {
	payload := "Results:\n" + bigJSONArray(120)
	a, _ := New(ModeAggressive).Content(payload)
	fresh := New(ModeAggressive)
	fresh.Content("Other:\n" + bigJSONArray(30))
	b, _ := fresh.Content(payload)
	if a != b {
		t.Fatalf("embedded compression varied by context: %d vs %d bytes", len(a), len(b))
	}
}
