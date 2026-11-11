package thinking

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type stubClassifier struct {
	decision Decision
	err      error
	delay    time.Duration
}

func (s *stubClassifier) Classify(ctx context.Context, body []byte) (Decision, error) {
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return Decision{}, context.DeadlineExceeded
		}
	}
	return s.decision, s.err
}

func TestFallbackOnClassifierError(t *testing.T) {
	// Epic constraint: a classifier failure must NEVER fail the request —
	// the fallback level is returned and no error escapes.
	c := &stubClassifier{err: errors.New("boom")}
	d, reason := ClassifyWithFallback(context.Background(), c,
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`), 50*time.Millisecond,
		"medium", 0.6)
	if d.Level != "medium" || reason != ReasonError {
		t.Fatalf("got level=%q reason=%q, want medium/error", d.Level, reason)
	}
}

func TestFallbackOnTimeout(t *testing.T) {
	c := &stubClassifier{decision: Decision{Level: "high", Confidence: 0.9}, delay: 200 * time.Millisecond}
	start := time.Now()
	d, reason := ClassifyWithFallback(context.Background(), c,
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`), 30*time.Millisecond,
		"low", 0.6)
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Fatalf("timeout budget not enforced: took %v", elapsed)
	}
	if d.Level != "low" || reason != ReasonTimeout {
		t.Fatalf("got level=%q reason=%q, want low/timeout", d.Level, reason)
	}
}

func TestFallbackOnNilClassifier(t *testing.T) {
	d, reason := ClassifyWithFallback(context.Background(), nil,
		[]byte(`{"messages":[]}`), 50*time.Millisecond, "off", 0)
	if d.Level != "off" || reason != ReasonDisabled {
		t.Fatalf("got level=%q reason=%q, want off/disabled", d.Level, reason)
	}
}

func TestBadResponseFallsBack(t *testing.T) {
	c := &stubClassifier{err: &BadResponseError{Reason: "not JSON"}}
	d, reason := ClassifyWithFallback(context.Background(), c,
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`), time.Second,
		"low", 0)
	if d.Level != "low" || reason != ReasonBadResponse {
		t.Fatalf("got level=%q reason=%q, want low/bad_response", d.Level, reason)
	}
}

func TestLowConfidenceFallsBack(t *testing.T) {
	c := &stubClassifier{decision: Decision{Level: "high", Confidence: 0.4}}
	d, reason := ClassifyWithFallback(context.Background(), c,
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`), time.Second,
		"low", 0.6)
	if d.Level != "low" || reason != ReasonLowConfidence {
		t.Fatalf("got level=%q reason=%q, want low/low_confidence", d.Level, reason)
	}
}

func TestConfidenceFloorZeroAcceptsZeroConfidence(t *testing.T) {
	// A floor of 0 means the operator opted out of the floor: a 0.0-confidence
	// answer is still an answer.
	c := &stubClassifier{decision: Decision{Level: "high", Confidence: 0.0}}
	d, reason := ClassifyWithFallback(context.Background(), c,
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`), time.Second,
		"low", 0)
	if d.Level != "high" || reason != ReasonClassified {
		t.Fatalf("got level=%q reason=%q, want high/classified", d.Level, reason)
	}
}

func TestHTTPClassifierRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		var body sidecarRequest
		json.NewDecoder(r.Body).Decode(&body)
		if body.Text == "" || body.Messages == 0 {
			t.Errorf("empty payload sent: %+v", body)
		}
		w.Write([]byte(`{"level":"high","confidence":0.88}`))
	}))
	defer srv.Close()

	d, err := NewHTTPClassifier(srv.URL).Classify(context.Background(),
		[]byte(`{"model":"m","messages":[{"role":"user","content":"solve P vs NP"}]}`))
	if err != nil || d.Level != "high" || d.Confidence != 0.88 {
		t.Fatalf("got %+v err=%v", d, err)
	}
}

func TestHTTPClassifierRejectsBadLevel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"level":"ultra-mega","confidence":0.9}`))
	}))
	defer srv.Close()

	var bre *BadResponseError
	_, err := NewHTTPClassifier(srv.URL).Classify(context.Background(),
		[]byte(`{"messages":[{"role":"user","content":"x"}]}`))
	if !errors.As(err, &bre) {
		t.Fatalf("expected *BadResponseError, got %T: %v", err, err)
	}
}

func TestHTTPClassifierStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	var se *StatusError
	_, err := NewHTTPClassifier(srv.URL).Classify(context.Background(),
		[]byte(`{"messages":[{"role":"user","content":"x"}]}`))
	if !errors.As(err, &se) {
		t.Fatalf("expected *StatusError, got %T: %v", err, err)
	}
}

func TestHTTPClassifierRejectsNonChatBody(t *testing.T) {
	// No messages array -> nothing to grade; error, and the fallback policy
	// turns that into the default level, never a crash.
	if _, err := NewHTTPClassifier("http://127.0.0.1:1/none").Classify(context.Background(),
		[]byte(`{"input":"embed me"}`)); err == nil {
		t.Fatal("expected error for body without messages")
	}
}

func TestClientSpecifiedLevel(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{`{"messages":[],"thinking":{"type":"enabled"}}`, true},
		{`{"messages":[],"reasoning_effort":"low"}`, true},
		{`{"messages":[],"thinking_budget":1024}`, true},
		{`{"messages":[],"chat_template_kwargs":{"enable_thinking":true}}`, true},
		{`{"messages":[],"chat_template_kwargs":{"enable_thinking":false}}`, true}, // explicit false is still a choice
		{`{"messages":[],"temperature":0.2}`, false},
		{`{"messages":[],"thinking":{}}`, false},             // empty object = no named level
		{`{"messages":[],"chat_template_kwargs":{}}`, false}, // unrelated kwarg
		{`not json`, false},
	}
	for _, tc := range cases {
		if got := ClientSpecifiedLevel([]byte(tc.body)); got != tc.want {
			t.Errorf("ClientSpecifiedLevel(%s) = %v, want %v", tc.body, got, tc.want)
		}
	}
}

func TestBuildSidecarRequestTruncatesAndStringifies(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":[{"type":"text","text":"hello"}}]}`)
	if _, err := buildSidecarRequest(body); err == nil {
		t.Fatal("expected error for malformed JSON")
	}

	long := make([]byte, 0, maxTextBytes+2000)
	for i := 0; i < maxTextBytes+1000; i++ {
		long = append(long, 'x')
	}
	payload, err := buildSidecarRequest(mustMarshal(t, map[string]any{
		"messages": []map[string]any{{"role": "user", "content": string(long)}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Text) > maxTextBytes {
		t.Fatalf("text not truncated: %d bytes", len(payload.Text))
	}
	if payload.Messages != 1 {
		t.Errorf("messages = %d", payload.Messages)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestInjectDeepMergeClientWins(t *testing.T) {
	// The invariant the enforce path relies on: injected params land, but a
	// client that already set a leaf keeps its own value.
	tmpl, err := ParseParamTemplate(`{"high":{"thinking":{"type":"enabled","budget_tokens":9999},"top_p":0.9}}`)
	if err != nil {
		t.Fatal(err)
	}
	out := tmpl.Inject([]byte(`{"thinking":{"type":"disabled","budget_tokens":100},"temperature":0.5}`), "high")
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	th := got["thinking"].(map[string]any)
	if th["type"] != "disabled" {
		t.Errorf("client leaf overwritten: type=%v", th["type"])
	}
	if th["budget_tokens"] != float64(100) {
		t.Errorf("client leaf overwritten: budget=%v", th["budget_tokens"])
	}
	if got["top_p"] != 0.9 {
		t.Errorf("new leaf not added: %v", got["top_p"])
	}
	if got["temperature"] != 0.5 {
		t.Error("unrelated key clobbered")
	}
}

func TestInjectUnknownLevelIsNoop(t *testing.T) {
	tmpl, _ := ParseParamTemplate(`{"high":{"thinking":{"type":"enabled"}}}`)
	body := []byte(`{"messages":[]}`)
	if out := tmpl.Inject(body, "medium"); string(out) != string(body) {
		t.Errorf("body changed for unconfigured level: %s", out)
	}
	if out := tmpl.Inject(body, "not-a-level"); string(out) != string(body) {
		t.Errorf("body changed for invalid level: %s", out)
	}
	var nilTmpl ParamTemplate
	if out := nilTmpl.Inject(body, "high"); string(out) != string(body) {
		t.Errorf("nil template changed body: %s", out)
	}
}

func TestParseParamTemplateValidates(t *testing.T) {
	tmpl, err := ParseParamTemplate(`{"low":{"thinking":{"type":"disabled"}},"bogus":{}}`)
	if err == nil {
		t.Fatal("expected error for unknown level key")
	}
	if tmpl != nil {
		t.Fatal("expected nil template on error")
	}
	tmpl, err = ParseParamTemplate("")
	if tmpl != nil || err != nil {
		t.Fatalf("empty template should be (nil, nil), got %v %v", tmpl, err)
	}
	tmpl, err = ParseParamTemplate(`{"high":{"thinking":{"type":"enabled"}}}`)
	if err != nil || len(tmpl) != 1 {
		t.Fatalf("valid template rejected: %v %v", tmpl, err)
	}
}
