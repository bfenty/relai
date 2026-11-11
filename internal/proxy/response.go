package proxy

import (
	"encoding/json"
	"github.com/bfenty/relai/internal/compress"
	"strings"
)

// ChatCompletionResponse represents the OpenAI chat completion response structure.
type ChatCompletionResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Usage   *Usage   `json:"usage,omitempty"`
	Timings *Timings `json:"timings,omitempty"`
}

// CompletionTokensDetails holds per-category completion token breakdown (OpenAI o-series / some llama.cpp builds).
type CompletionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// Usage holds token usage information.
type Usage struct {
	PromptTokens            int                      `json:"prompt_tokens"`
	CompletionTokens        int                      `json:"completion_tokens"`
	TotalTokens             int                      `json:"total_tokens"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
}

// Timings is the engine's per-request report. Halogen (and llama.cpp-style
// servers) attach it to the non-streamed body and the stream's usage/finish
// chunk. Only the speculation counters are consumed today: draft_n counts the
// drafter's proposals, draft_n_accepted the ones verified — their ratio is the
// MTP health signal.
type Timings struct {
	DraftN         int `json:"draft_n"`
	DraftNAccepted int `json:"draft_n_accepted"`
}

// PromptTokensDetails carries the share of the prompt that was served from the
// upstream's KV cache. This is the signal for whether session affinity is working:
// a conversation routed back to the host holding its context reports nearly all of
// its prompt as cached, while one that lost its host reports zero and pays a full
// reprocess.
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// ChatCompletionRequest represents the OpenAI chat completion request.
type ChatCompletionRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream,omitempty"`
}

// Message represents a chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// transformRequest rewrites the model name, merges model-level params, and injects
// stream_options so streaming responses include a final usage event.
func transformRequest(body []byte, localModel, remoteModel, params string) ([]byte, error) {
	out, _, err := transformRequestWithCompression(body, localModel, remoteModel, params, "", nil, false)
	return out, err
}

// transformRequestWithCompression additionally shrinks message content. Compression
// runs here, at forward time, and never touches the body the router keyed on - the
// session key is computed from the original request, so affinity is unaffected by
// whether a target compresses.
//
// thinkingParams is the adaptive-thinking param body for this request's chosen
// level (empty when the router has no classifier, runs in shadow mode, or the
// client already specified a level).
func transformRequestWithCompression(body []byte, localModel, remoteModel, params, thinkingParams string, c *compress.Compressor, returnProgress bool) ([]byte, compress.Stats, error) {
	var stats compress.Stats
	var bodyMap map[string]any
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		return body, stats, nil
	}

	// Rewrite model name
	if m, ok := bodyMap["model"].(string); ok && m == localModel {
		bodyMap["model"] = remoteModel
	}

	// Merge model-level params. Scalars: model wins. Nested objects: deep-merged
	// so clients can override individual leaf keys (e.g. chat_template_kwargs.enable_thinking).
	if params != "" && params != "{}" {
		var paramsMap map[string]any
		if err := json.Unmarshal([]byte(params), &paramsMap); err == nil {
			applyModelParams(bodyMap, paramsMap)
		}
	}

	// Adaptive-thinking injection (issue #10). The virtual router stashed the
	// per-backend param body for the chosen level on the request context in
	// enforce mode; shadow mode and every non-adaptive router leave it empty,
	// so this is a no-op for them. Merged AFTER model params and with client
	// leaf values winning — a client that named its own thinking level was
	// never classified in the first place, and this ordering keeps that
	// precedence true even if a future caller wires injection differently.
	if thinkingParams != "" && thinkingParams != "{}" {
		var thinkingMap map[string]any
		if err := json.Unmarshal([]byte(thinkingParams), &thinkingMap); err == nil {
			applyModelParams(bodyMap, thinkingMap)
		}
	}

	if c.Enabled() {
		stats = compressMessages(bodyMap, c)
	}

	// Inject stream_options so we get a final usage chunk (most OpenAI-compatible servers support this)
	if streaming, ok := bodyMap["stream"].(bool); ok && streaming {
		if _, hasOpts := bodyMap["stream_options"]; !hasOpts {
			bodyMap["stream_options"] = map[string]any{"include_usage": true}
		}
		// Halogen backends stream real prompt progress (prefill token counts and
		// host time before the first token) when asked. The client never sees
		// these chunks — StreamingReader consumes them and feeds the queue
		// tracker — so the flag is relai's business, not the client's. A client
		// that set the field itself is left alone.
		if returnProgress {
			if _, has := bodyMap["return_progress"]; !has {
				bodyMap["return_progress"] = true
			}
		}
	}

	out, err := json.Marshal(bodyMap)
	return out, stats, err
}

func transformModelName(body []byte, localModel, remoteModel string) ([]byte, error) {
	return transformRequest(body, localModel, remoteModel, "{}")
}

// extractModelFromBody extracts the model name from a JSON body.
func extractModelFromBody(body []byte) string {
	var req ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err == nil {
		return req.Model
	}
	return ""
}

// extractTokenCounts parses a response body for token usage, computing cost from per-million rates.
func extractTokenCounts(body []byte, inputCostPerM, outputCostPerM float64) TokenCounts {
	var resp ChatCompletionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return TokenCounts{}
	}
	if resp.Usage == nil {
		return TokenCounts{}
	}
	prompt := resp.Usage.PromptTokens
	completion := resp.Usage.CompletionTokens
	thinking := 0
	if resp.Usage.CompletionTokensDetails != nil {
		thinking = resp.Usage.CompletionTokensDetails.ReasoningTokens
	}
	// Fallback: count <think>...</think> content in choices as rough estimate
	if thinking == 0 {
		thinking = extractThinkingTokenEstimate(body)
	}
	cost := float64(prompt)/1_000_000*inputCostPerM + float64(completion)/1_000_000*outputCostPerM
	cached := 0
	if resp.Usage.PromptTokensDetails != nil {
		cached = resp.Usage.PromptTokensDetails.CachedTokens
	}
	var draftN, draftAccept int
	if resp.Timings != nil {
		draftN, draftAccept = resp.Timings.DraftN, resp.Timings.DraftNAccepted
	}
	return TokenCounts{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		ThinkingTokens:   thinking,
		TotalTokens:      resp.Usage.TotalTokens,
		CachedTokens:     cached,
		Cost:             cost,
		DraftN:           draftN,
		DraftNAccepted:   draftAccept,
	}
}

// extractThinkingTokenEstimate estimates thinking tokens from the response body.
// Checks reasoning_content first (llama.cpp / vllm style), then falls back to <think> tags.
func extractThinkingTokenEstimate(body []byte) int {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return 0
	}
	var choices []struct {
		Message struct {
			ReasoningContent string `json:"reasoning_content"`
			Content          string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw["choices"], &choices); err != nil || len(choices) == 0 {
		return 0
	}
	if rc := choices[0].Message.ReasoningContent; rc != "" {
		return len(rc) / 4
	}
	// Fallback: <think>...</think> inline
	content := choices[0].Message.Content
	start := strings.Index(content, "<think>")
	end := strings.Index(content, "</think>")
	if start != -1 && end > start {
		return (end - (start + 7)) / 4
	}
	return 0
}

// parseSSEEvent parses a Server-Sent Event line for token usage.
func parseSSEEvent(data string) *TokenCounts {
	// SSE events come as: data: {"usage": {...}}
	data = strings.TrimSpace(data)
	if !strings.HasPrefix(data, "data: ") {
		return nil
	}
	data = strings.TrimPrefix(data, "data: ")

	if data == "[DONE]" {
		return nil
	}

	var resp ChatCompletionResponse
	if err := json.Unmarshal([]byte(data), &resp); err != nil {
		return nil
	}
	if resp.Usage == nil {
		return nil
	}
	return &TokenCounts{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}
}

// applyModelParams merges model-level params into the request body map.
// Scalars: model always wins (enforced config).
// Nested objects: deep-merged with client values taking precedence at the leaf,
// so the client can override individual flags like chat_template_kwargs.enable_thinking.
// ceilingParams are output-length limits. For these a model-level value is a cap,
// not an override: a client asking for fewer tokens must get fewer. Overriding them
// upward silently ignores the client's limit, which breaks cost and latency control
// (a caller asking for 20 tokens would receive a full-length generation, and
// finish_reason would read "stop" rather than "length").
var ceilingParams = map[string]bool{
	"max_tokens":            true,
	"max_completion_tokens": true,
}

func applyModelParams(body, params map[string]any) {
	for k, modelVal := range params {
		clientVal, exists := body[k]
		if exists {
			bodyObj, bodyIsObj := clientVal.(map[string]any)
			paramObj, paramIsObj := modelVal.(map[string]any)
			if bodyIsObj && paramIsObj {
				merged := make(map[string]any, len(paramObj)+len(bodyObj))
				for mk, mv := range paramObj {
					merged[mk] = mv
				}
				for bk, bv := range bodyObj { // client wins within the nested object
					merged[bk] = bv
				}
				body[k] = merged
				continue
			}
			if ceilingParams[k] {
				if lower, ok := lowerNumber(clientVal, modelVal); ok {
					body[k] = lower
					continue
				}
			}
		}
		body[k] = modelVal // scalar or key absent: model wins
	}
}

// lowerNumber returns whichever of the two JSON numbers is smaller. Reports false if
// either is not a number, in which case the caller falls back to its normal precedence.
func lowerNumber(a, b any) (any, bool) {
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	if !aok || !bok {
		return nil, false
	}
	if af <= bf {
		return a, true
	}
	return b, true
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64: // encoding/json decodes all numbers to float64
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// FormatError creates a standard error response.
func FormatError(message string, code int) []byte {
	resp := map[string]interface{}{
		"error": map[string]interface{}{
			"message": message,
			"type":    "relai_error",
			"code":    code,
		},
	}
	data, _ := json.Marshal(resp)
	return data
}

// compressMessages rewrites each message's content independently. Messages are
// handled one at a time and never in relation to each other, which is what keeps a
// growing conversation's prefix byte-stable.
func compressMessages(bodyMap map[string]any, c *compress.Compressor) compress.Stats {
	var stats compress.Stats
	msgs, ok := bodyMap["messages"].([]any)
	if !ok {
		return stats
	}
	for _, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		content, ok := m["content"].(string)
		if !ok {
			continue // multimodal content parts are left alone
		}
		out, changed := c.Content(content)
		stats.BytesBefore += len(content)
		stats.BytesAfter += len(out)
		if changed {
			m["content"] = out
			stats.MessagesCompressed++
		}
	}
	return stats
}
