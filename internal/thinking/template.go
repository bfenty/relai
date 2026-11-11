package thinking

import (
	"encoding/json"
	"strings"
)

// ParamTemplate maps an effort level to the body keys a specific backend
// family understands. Backends genuinely differ (llama.cpp and Halogen take
// chat_template_kwargs.enable_thinking, OpenAI-style reasoning servers take
// reasoning_effort), so the mapping is per-router configuration, not code.
//
// A template is a JSON object of level -> params, where params is a JSON
// object merged into the forwarded request body with the same deep-merge
// semantics as model-level params (client leaf values win):
//
//	{
//	  "off":    {"chat_template_kwargs": {"enable_thinking": false}},
//	  "low":    {"chat_template_kwargs": {"enable_thinking": true, "thinking_budget": 512}},
//	  "medium": {"chat_template_kwargs": {"enable_thinking": true}},
//	  "high":   {"chat_template_kwargs": {"enable_thinking": true}, "reasoning_effort": "high"}
//	}
type ParamTemplate map[string]map[string]any

// ParseParamTemplate validates the configured JSON. An empty string is a
// valid, meaningful value: no template, so classification is logged but
// nothing is injected (the shadow-mode default for exotic backends).
func ParseParamTemplate(s string) (ParamTemplate, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "{}" {
		return nil, nil
	}
	var t ParamTemplate
	if err := json.Unmarshal([]byte(s), &t); err != nil {
		return nil, err
	}
	for level := range t {
		if !ValidLevel(level) {
			return nil, &BadResponseError{Reason: "template has unknown level " + level}
		}
	}
	return t, nil
}

// Inject merges the template's params for level into body and returns the
// new body. An unknown level, nil template, or non-object body leaves the
// body byte-for-byte unchanged — injection must never corrupt a request.
func (t ParamTemplate) Inject(body []byte, level string) []byte {
	if t == nil || !ValidLevel(level) {
		return body
	}
	params, ok := t[level]
	if !ok || len(params) == 0 {
		return body
	}
	var bodyMap map[string]any
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		return body
	}
	applyParams(bodyMap, params)
	out, err := json.Marshal(bodyMap)
	if err != nil {
		return body
	}
	return out
}

// applyParams deep-merges params into bodyMap with body (client) values
// winning at the leaf — mirroring proxy.applyModelParams so classifier
// injection can never override an explicit client choice.
func applyParams(body, params map[string]any) {
	for k, pv := range params {
		bv, exists := body[k]
		if !exists {
			body[k] = pv
			continue
		}
		pm, pOK := pv.(map[string]any)
		bm, bOK := bv.(map[string]any)
		if pOK && bOK {
			applyParams(bm, pm)
			continue
		}
		// Scalar: the client value stands.
	}
}

// ClientSpecifiedLevel reports whether the request already carries a thinking
// level the client chose. Precedence rule from the epic: a client-specified
// level ALWAYS wins and the classifier is not consulted.
//
// Detection covers the keys the ecosystem actually uses:
//   - chat_template_kwargs.enable_thinking (llama.cpp / Halogen)
//   - reasoning_effort (OpenAI-compatible reasoning servers)
//   - thinking / thinking_budget (Anthropic-style passthroughs)
func ClientSpecifiedLevel(body []byte) bool {
	var req struct {
		ReasoningEffort    *string        `json:"reasoning_effort"`
		Thinking           map[string]any `json:"thinking"`
		ThinkingBudget     *int           `json:"thinking_budget"`
		ChatTemplateKwargs *struct {
			EnableThinking *bool `json:"enable_thinking"`
		} `json:"chat_template_kwargs"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return false
	}
	if req.ReasoningEffort != nil {
		return true
	}
	if req.ThinkingBudget != nil {
		return true
	}
	if len(req.Thinking) > 0 {
		return true
	}
	if req.ChatTemplateKwargs != nil && req.ChatTemplateKwargs.EnableThinking != nil {
		return true
	}
	return false
}
