package vrouter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bfenty/relai/internal/db"
	"github.com/bfenty/relai/internal/proxy"
	"github.com/bfenty/relai/internal/thinking"
)

type stubThinking struct {
	decision thinking.Decision
	err      error
	called   bool
}

func (s *stubThinking) Classify(ctx context.Context, body []byte) (thinking.Decision, error) {
	s.called = true
	return s.decision, s.err
}

func withStubClassifier(t *testing.T, c thinking.Classifier) {
	t.Helper()
	prev := classifierFor
	classifierFor = func(url string) thinking.Classifier {
		if url == "" {
			return nil
		}
		return c
	}
	t.Cleanup(func() { classifierFor = prev })
}

const adaptiveTemplate = `{"high":{"chat_template_kwargs":{"enable_thinking":true}},"off":{"chat_template_kwargs":{"enable_thinking":false}}}`

func adaptiveRouter(t *testing.T, mode string) *db.Router {
	t.Helper()
	return &db.Router{
		ID: 1, Name: "adaptive-test", Strategy: "adaptive_thinking",
		ThinkingSidecarURL: "http://classifier.test/classify",
		ThinkingTimeoutMs:  50, ThinkingDefaultLevel: "low",
		ThinkingConfidenceFloor: 0.6, ThinkingMode: mode,
		ThinkingParamTemplateJSON: adaptiveTemplate,
	}
}

func TestAdaptiveShadowModeLogsButInjectsNothing(t *testing.T) {
	withStubClassifier(t, &stubThinking{decision: thinking.Decision{Level: "high", Confidence: 0.9}})
	m := testManager()
	ctx := &proxy.RequestContext{}
	m.applyAdaptiveThinking(context.Background(), adaptiveRouter(t, "shadow"),
		[]byte(`{"messages":[{"role":"user","content":"hard question"}]}`), ctx)

	if ctx.ThinkingLevel != "high" {
		t.Errorf("shadow must still record the level, got %q", ctx.ThinkingLevel)
	}
	if ctx.ThinkingReason != thinking.ReasonShadow {
		t.Errorf("reason = %q, want shadow", ctx.ThinkingReason)
	}
	if ctx.ThinkingParamsJSON != "" {
		t.Errorf("shadow mode must not inject, got %q", ctx.ThinkingParamsJSON)
	}
	if ctx.ThinkingConfidence == nil || *ctx.ThinkingConfidence != 0.9 {
		t.Error("confidence not recorded")
	}
	if ctx.ClassifierMs == nil {
		t.Error("classifier latency not recorded")
	}
}

func TestAdaptiveEnforceModeStashesParams(t *testing.T) {
	withStubClassifier(t, &stubThinking{decision: thinking.Decision{Level: "high", Confidence: 0.9}})
	m := testManager()
	ctx := &proxy.RequestContext{}
	m.applyAdaptiveThinking(context.Background(), adaptiveRouter(t, "enforce"),
		[]byte(`{"messages":[{"role":"user","content":"hard question"}]}`), ctx)

	if ctx.ThinkingReason != thinking.ReasonClassified {
		t.Errorf("reason = %q, want classified", ctx.ThinkingReason)
	}
	if ctx.ThinkingParamsJSON == "" {
		t.Fatal("enforce mode must stash params for injection")
	}
	if !strings.Contains(ctx.ThinkingParamsJSON, "enable_thinking") {
		t.Errorf("params missing template content: %s", ctx.ThinkingParamsJSON)
	}
}

func TestAdaptiveClientSpecifiedWinsClassifierUntouched(t *testing.T) {
	stub := &stubThinking{decision: thinking.Decision{Level: "high", Confidence: 0.99}}
	withStubClassifier(t, stub)
	m := testManager()
	ctx := &proxy.RequestContext{}
	m.applyAdaptiveThinking(context.Background(), adaptiveRouter(t, "enforce"),
		[]byte(`{"messages":[{"role":"user","content":"hi"}],"reasoning_effort":"low"}`), ctx)

	if stub.called {
		t.Error("classifier consulted despite client-specified level")
	}
	if ctx.ThinkingReason != thinking.ReasonClientSet || ctx.ThinkingParamsJSON != "" {
		t.Errorf("got reason=%q params=%q, want client_specified/empty", ctx.ThinkingReason, ctx.ThinkingParamsJSON)
	}
}

func TestAdaptiveDisabledWithoutSidecar(t *testing.T) {
	withStubClassifier(t, &stubThinking{decision: thinking.Decision{Level: "high", Confidence: 0.99}})
	m := testManager()
	router := adaptiveRouter(t, "enforce")
	router.ThinkingSidecarURL = ""
	ctx := &proxy.RequestContext{}
	m.applyAdaptiveThinking(context.Background(), router,
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`), ctx)

	if ctx.ThinkingReason != thinking.ReasonDisabled || ctx.ThinkingLevel != "low" {
		t.Errorf("got reason=%q level=%q, want disabled/low (default)", ctx.ThinkingReason, ctx.ThinkingLevel)
	}
	if ctx.ThinkingParamsJSON != "" {
		t.Error("disabled classifier must not inject")
	}
}

func TestAdaptiveClassifierFailureNeverBlocks(t *testing.T) {
	// The epic's hard constraint, at the strategy seam: sidecar down -> the
	// request proceeds with the default level and no injection in shadow mode.
	withStubClassifier(t, &stubThinking{err: context.DeadlineExceeded})
	m := testManager()
	ctx := &proxy.RequestContext{}
	m.applyAdaptiveThinking(context.Background(), adaptiveRouter(t, "shadow"),
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`), ctx)

	if ctx.ThinkingReason != thinking.ReasonTimeout || ctx.ThinkingLevel != "low" {
		t.Errorf("got reason=%q level=%q, want timeout/low", ctx.ThinkingReason, ctx.ThinkingLevel)
	}
}

func TestAdaptiveEnforceWithoutTemplateIsNoop(t *testing.T) {
	withStubClassifier(t, &stubThinking{decision: thinking.Decision{Level: "high", Confidence: 0.9}})
	m := testManager()
	router := adaptiveRouter(t, "enforce")
	router.ThinkingParamTemplateJSON = ""
	ctx := &proxy.RequestContext{}
	m.applyAdaptiveThinking(context.Background(), router,
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`), ctx)

	if ctx.ThinkingParamsJSON != "" || ctx.ThinkingReason != thinking.ReasonNoTemplate {
		t.Errorf("got params=%q reason=%q, want empty/no_template", ctx.ThinkingParamsJSON, ctx.ThinkingReason)
	}
}

func TestAdaptiveEnforceLevelMissingFromTemplateIsNoop(t *testing.T) {
	withStubClassifier(t, &stubThinking{decision: thinking.Decision{Level: "medium", Confidence: 0.9}})
	m := testManager()
	ctx := &proxy.RequestContext{}
	m.applyAdaptiveThinking(context.Background(), adaptiveRouter(t, "enforce"),
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`), ctx)

	if ctx.ThinkingParamsJSON != "" || ctx.ThinkingReason != thinking.ReasonNoTemplate {
		t.Errorf("got params=%q reason=%q, want empty/no_template for level absent from template",
			ctx.ThinkingParamsJSON, ctx.ThinkingReason)
	}
}

func TestAdaptiveDefaultTimeoutApplied(t *testing.T) {
	// A router with thinking_timeout_ms=0 must still enforce the strict
	// default budget rather than calling the sidecar unbounded.
	withStubClassifier(t, &stubThinking{decision: thinking.Decision{Level: "high", Confidence: 0.9}})
	m := testManager()
	router := adaptiveRouter(t, "shadow")
	router.ThinkingTimeoutMs = 0
	start := time.Now()
	ctx := &proxy.RequestContext{}
	m.applyAdaptiveThinking(context.Background(), router,
		[]byte(`{"messages":[{"role":"user","content":"hi"}]}`), ctx)
	if time.Since(start) > defaultClassifierTimeout*4 {
		t.Fatalf("default budget not applied: %v", time.Since(start))
	}
	if ctx.ThinkingReason != thinking.ReasonShadow {
		t.Errorf("reason = %q", ctx.ThinkingReason)
	}
}
