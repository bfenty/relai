package proxy

import (
	"encoding/json"
	"testing"
)

// Issue #10 flow-level proof at the forward-time seam: the body that will be
// sent upstream must actually carry the classifier's params in enforce mode,
// and must be untouched when no params are stashed (shadow mode, disabled
// classifier, every non-adaptive router).

func TestTransformInjectsThinkingParams(t *testing.T) {
	body := []byte(`{"model":"local","messages":[{"role":"user","content":"hi"}]}`)
	out, _, err := transformRequestWithCompression(body, "local", "remote",
		`{"temperature":0.3}`,
		`{"chat_template_kwargs":{"enable_thinking":true,"thinking_budget":4096}}`, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	kw, ok := got["chat_template_kwargs"].(map[string]any)
	if !ok {
		t.Fatalf("thinking params not injected: %s", out)
	}
	if kw["enable_thinking"] != true || kw["thinking_budget"] != float64(4096) {
		t.Errorf("wrong injected params: %v", kw)
	}
	if got["temperature"] != 0.3 {
		t.Errorf("model params lost: %v", got["temperature"])
	}
}

func TestTransformThinkingParamsDoNotOverrideClientLeaves(t *testing.T) {
	body := []byte(`{"model":"local","messages":[],"chat_template_kwargs":{"thinking_budget":10}}`)
	out, _, err := transformRequestWithCompression(body, "local", "remote", "{}",
		`{"chat_template_kwargs":{"enable_thinking":true,"thinking_budget":9999}}`, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(out, &got)
	kw := got["chat_template_kwargs"].(map[string]any)
	if kw["thinking_budget"] != float64(10) {
		t.Errorf("client leaf overwritten: %v", kw)
	}
	if kw["enable_thinking"] != true {
		t.Errorf("new leaf not added: %v", kw)
	}
}

func TestTransformEmptyThinkingParamsIsByteNeutral(t *testing.T) {
	body := []byte(`{"model":"local","messages":[{"role":"user","content":"hi"}]}`)
	base, _, _ := transformRequestWithCompression(body, "local", "remote", "{}", "", nil, false)
	withEmpty, _, _ := transformRequestWithCompression(body, "local", "remote", "{}", "{}", nil, false)
	if string(base) != string(withEmpty) {
		t.Errorf("empty thinking params changed the body:\n%s\n%s", base, withEmpty)
	}
}
