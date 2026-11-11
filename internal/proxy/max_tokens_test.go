package proxy

import (
	"encoding/json"
	"testing"
)

func transformed(t *testing.T, body, params string) map[string]any {
	t.Helper()
	out, err := transformRequest([]byte(body), "local", "remote", params)
	if err != nil {
		t.Fatalf("transformRequest: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return got
}

// A model-level max_tokens is a ceiling, not an override. A client asking for fewer
// tokens must get fewer - otherwise a 20-token request returns a full-length answer
// with finish_reason "stop", and callers lose all cost and latency control.
func TestClientMaxTokensBelowModelCeilingWins(t *testing.T) {
	got := transformed(t,
		`{"model":"local","messages":[],"max_tokens":20}`,
		`{"max_tokens":4096}`)
	if got["max_tokens"] != float64(20) {
		t.Errorf("client max_tokens was overridden: got %v, want 20", got["max_tokens"])
	}
}

// A client asking for more than the model allows is still capped by the model.
func TestClientMaxTokensAboveModelCeilingIsCapped(t *testing.T) {
	got := transformed(t,
		`{"model":"local","messages":[],"max_tokens":100000}`,
		`{"max_tokens":4096}`)
	if got["max_tokens"] != float64(4096) {
		t.Errorf("model ceiling not applied: got %v, want 4096", got["max_tokens"])
	}
}

// With no client value, the model default still applies.
func TestModelMaxTokensAppliesWhenClientOmitsIt(t *testing.T) {
	got := transformed(t,
		`{"model":"local","messages":[]}`,
		`{"max_tokens":4096}`)
	if got["max_tokens"] != float64(4096) {
		t.Errorf("model default not applied: got %v, want 4096", got["max_tokens"])
	}
}

// The newer OpenAI field name gets the same treatment.
func TestMaxCompletionTokensIsAlsoACeiling(t *testing.T) {
	got := transformed(t,
		`{"model":"local","messages":[],"max_completion_tokens":20}`,
		`{"max_completion_tokens":4096}`)
	if got["max_completion_tokens"] != float64(20) {
		t.Errorf("client max_completion_tokens was overridden: got %v, want 20", got["max_completion_tokens"])
	}
}

// Non-length scalars keep the existing "model wins" precedence, so admins can still
// pin things like temperature.
func TestNonCeilingScalarsStillLetModelWin(t *testing.T) {
	got := transformed(t,
		`{"model":"local","messages":[],"temperature":0.9}`,
		`{"temperature":0.1}`)
	if got["temperature"] != 0.1 {
		t.Errorf("model should still win for temperature: got %v, want 0.1", got["temperature"])
	}
}
