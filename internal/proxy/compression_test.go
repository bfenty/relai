package proxy

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/bfenty/relai/internal/compress"
)

func toolResult(n int) string {
	items := make([]map[string]any, n)
	for i := range items {
		items[i] = map[string]any{
			"file":   fmt.Sprintf("src/mod_%d.py", i),
			"line":   i * 7,
			"symbol": fmt.Sprintf("handler_%d", i),
		}
	}
	b, _ := json.MarshalIndent(items, "", "  ")
	return string(b)
}

func chatBody(t *testing.T, msgs []map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"model": "local", "messages": msgs})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func messagesOf(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var parsed struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return parsed.Messages
}

// The end-to-end guarantee: a conversation's earlier messages are forwarded
// byte-identically as it grows. If this fails, the upstream re-prefills the whole
// context every turn - on this deployment ~29s instead of ~1.7s.
func TestCompressionKeepsGrowingConversationPrefixStable(t *testing.T) {
	c := compress.New(compress.ModeAggressive)
	sys := map[string]any{"role": "system", "content": "You are an agent. " + toolResult(4)}
	big := map[string]any{"role": "user", "content": "Results:\n" + toolResult(120)}

	turn1 := chatBody(t, []map[string]any{sys, big})
	turn3 := chatBody(t, []map[string]any{
		sys, big,
		{"role": "assistant", "content": "ack"},
		{"role": "user", "content": "follow up"},
		{"role": "assistant", "content": "ack2"},
		{"role": "user", "content": "another follow up"},
	})

	out1, _, err := transformRequestWithCompression(turn1, "local", "remote", "{}", "", c, false)
	if err != nil {
		t.Fatalf("turn1: %v", err)
	}
	out3, _, err := transformRequestWithCompression(turn3, "local", "remote", "{}", "", c, false)
	if err != nil {
		t.Fatalf("turn3: %v", err)
	}

	m1, m3 := messagesOf(t, out1), messagesOf(t, out3)
	if len(m3) < len(m1) {
		t.Fatalf("later turn has fewer messages: %d < %d", len(m3), len(m1))
	}
	for i := range m1 {
		a, _ := m1[i]["content"].(string)
		b, _ := m3[i]["content"].(string)
		if a != b {
			t.Fatalf("message %d changed once it became history: %d -> %d bytes",
				i, len(a), len(b))
		}
	}
}

// Compression must not disturb the rest of the transform.
func TestCompressionPreservesModelRewriteAndParams(t *testing.T) {
	c := compress.New(compress.ModeLossless)
	body := chatBody(t, []map[string]any{{"role": "user", "content": toolResult(60)}})

	out, _, err := transformRequestWithCompression(body, "local", "remote", `{"temperature":0.1}`, "", c, false)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed["model"] != "remote" {
		t.Errorf("model not rewritten: %v", parsed["model"])
	}
	if parsed["temperature"] != 0.1 {
		t.Errorf("model params not merged: %v", parsed["temperature"])
	}
}

// A nil compressor leaves the body exactly as the existing path produced it.
func TestNilCompressorMatchesPlainTransform(t *testing.T) {
	body := chatBody(t, []map[string]any{{"role": "user", "content": toolResult(60)}})
	plain, err := transformRequest(body, "local", "remote", "{}")
	if err != nil {
		t.Fatalf("plain: %v", err)
	}
	withNil, _, err := transformRequestWithCompression(body, "local", "remote", "{}", "", nil, false)
	if err != nil {
		t.Fatalf("nil compressor: %v", err)
	}
	if string(plain) != string(withNil) {
		t.Error("nil compressor changed the output")
	}
}

// Multimodal content arrives as an array of parts rather than a string; those must
// pass through untouched rather than being mangled or dropped.
func TestNonStringContentUntouched(t *testing.T) {
	c := compress.New(compress.ModeAggressive)
	parts := []any{
		map[string]any{"type": "text", "text": "describe this"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AAAA"}},
	}
	body := chatBody(t, []map[string]any{{"role": "user", "content": parts}})
	out, _, err := transformRequestWithCompression(body, "local", "remote", "{}", "", c, false)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	msgs := messagesOf(t, out)
	got, ok := msgs[0]["content"].([]any)
	if !ok {
		t.Fatalf("content type changed: %T", msgs[0]["content"])
	}
	if len(got) != 2 {
		t.Fatalf("content parts lost: %d", len(got))
	}
}

// The saving has to be real, or the feature is not worth its risk.
func TestCompressionActuallySaves(t *testing.T) {
	c := compress.New(compress.ModeAggressive)
	body := chatBody(t, []map[string]any{{"role": "user", "content": "Results:\n" + toolResult(120)}})
	out, _, err := transformRequestWithCompression(body, "local", "remote", "{}", "", c, false)
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(out) >= len(body) {
		t.Fatalf("no saving: %d -> %d bytes", len(body), len(out))
	}
	t.Logf("body %d -> %d bytes (%.1f%% smaller)", len(body), len(out),
		100*(1-float64(len(out))/float64(len(body))))
}
