package vrouter

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/bfenty/relai/internal/db"
	"github.com/bfenty/relai/internal/proxy"
	"github.com/bfenty/relai/internal/thinking"
)

// Adaptive thinking (issue #10, epic #9).
//
// The adaptive_thinking strategy composes with, rather than modifies, the
// existing placement logic: acquireMember's default branch already gives it
// sticky placement, so this file only adds the classifier decision flow and
// never touches sticky/cache-aware/round-robin/least-connections code paths.
//
// Hard constraint from the epic: the classifier is advisory. Every failure
// mode (no sidecar, timeout, non-200, malformed JSON, low confidence)
// resolves to the router's default level and the request proceeds. A request
// is never failed because the classifier was unavailable.

// defaultClassifierTimeout is the strict budget for the sidecar call when a
// router does not configure one. 50ms per the epic: the classifier must not
// become a visible fraction of request latency.
const defaultClassifierTimeout = 50 * time.Millisecond

// classifiers caches one HTTP client per sidecar URL so connection pooling
// survives across requests. Keyed by URL; a router config change to a new
// URL simply starts using (and caching) the new entry.
var classifiers sync.Map // url -> *thinking.HTTPClassifier

// classifierFor is a var so tests can inject stub classifiers without a
// live sidecar; production always uses the cached HTTP client path.
var classifierFor = func(url string) thinking.Classifier {
	if url == "" {
		return nil
	}
	if c, ok := classifiers.Load(url); ok {
		return c.(*thinking.HTTPClassifier)
	}
	c := thinking.NewHTTPClassifier(url)
	actual, _ := classifiers.LoadOrStore(url, c)
	return actual.(*thinking.HTTPClassifier)
}

// applyAdaptiveThinking runs the classifier decision flow for one request
// and stashes the outcome on the proxy context. It is called only for
// routers whose strategy is adaptive_thinking; every other router never
// reaches this code and stays byte-for-byte identical.
//
// Decision flow (issue #10 deliverable 3 + 4):
//  1. client-specified level  -> passthrough, classifier not consulted
//  2. no sidecar configured   -> default level, reason=disabled
//  3. classify under the strict timeout budget
//  4. timeout/error/bad response/low confidence -> default level + reason
//  5. shadow mode (default)   -> log the decision, inject nothing
//  6. enforce mode            -> stash per-backend params for injection
func (m *Manager) applyAdaptiveThinking(ctx context.Context, router *db.Router, body []byte, proxyCtx *proxy.RequestContext) {
	if proxyCtx == nil {
		return
	}

	// Precedence: a client that named its own thinking level always wins and
	// the classifier is not consulted at all.
	if thinking.ClientSpecifiedLevel(body) {
		proxyCtx.ThinkingReason = thinking.ReasonClientSet
		return
	}

	classifier := classifierFor(router.ThinkingSidecarURL)
	timeout := time.Duration(router.ThinkingTimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = defaultClassifierTimeout
	}

	start := time.Now()
	decision, reason := thinking.ClassifyWithFallback(ctx, classifier, body, timeout,
		router.ThinkingDefaultLevel, router.ThinkingConfidenceFloor)
	elapsed := int(time.Since(start).Milliseconds())

	proxyCtx.ThinkingLevel = decision.Level
	conf := decision.Confidence
	proxyCtx.ThinkingConfidence = &conf
	proxyCtx.ClassifierMs = &elapsed
	proxyCtx.ThinkingReason = reason

	// Shadow mode (the default for new adaptive routers): classify and log,
	// inject nothing. Enforce mode injects ONLY an actual classification —
	// a fallback decision (disabled/timeout/error/low-confidence) means the
	// sidecar never spoke, and inventing params from a default would make a
	// sidecar outage load-bearing, violating the advisory constraint.
	if router.ThinkingMode != "enforce" {
		if reason == thinking.ReasonClassified {
			proxyCtx.ThinkingReason = thinking.ReasonShadow
		}
		return
	}
	if reason != thinking.ReasonClassified || decision.Level == "" {
		return
	}
	template, err := thinking.ParseParamTemplate(router.ThinkingParamTemplateJSON)
	if err != nil || template == nil {
		proxyCtx.ThinkingReason = thinking.ReasonNoTemplate
		return
	}
	params := template[decision.Level]
	if len(params) == 0 {
		proxyCtx.ThinkingReason = thinking.ReasonNoTemplate
		return
	}
	raw, err := json.Marshal(params)
	if err != nil {
		proxyCtx.ThinkingReason = thinking.ReasonNoTemplate
		return
	}
	proxyCtx.ThinkingParamsJSON = string(raw)
}

// logThinkingDecision records the classifier outcome as a system event so
// shadow-mode data can be compared against heuristic and actual usage
// (epic rollout step 1) without digging through request_logs.
func (m *Manager) logThinkingDecision(router *db.Router, proxyCtx *proxy.RequestContext) {
	if m.db == nil || m.db.SystemEvents == nil || proxyCtx.ThinkingReason == "" {
		return
	}
	meta := map[string]any{
		"router_id":   router.ID,
		"router_name": router.Name,
		"strategy":    normalizeStrategy(router.Strategy),
		"reason":      proxyCtx.ThinkingReason,
	}
	if proxyCtx.ThinkingLevel != "" {
		meta["level"] = proxyCtx.ThinkingLevel
	}
	if proxyCtx.ThinkingConfidence != nil {
		meta["confidence"] = *proxyCtx.ThinkingConfidence
	}
	if proxyCtx.ClassifierMs != nil {
		meta["classifier_ms"] = *proxyCtx.ClassifierMs
	}
	m.db.SystemEvents.Log(db.LevelInfo, db.EventProxy, "adaptive thinking decision", meta)
}
