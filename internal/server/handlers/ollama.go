package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Ollama native API compatibility (issue #12).
//
// These handlers translate the Ollama wire dialect (/api/chat, /api/generate,
// /api/tags, /api/version) onto the existing OpenAI-dialect proxy path. Model
// names are virtual-router / mapped-model names, exactly as in /v1. Routing,
// admission, rate limits, and logging behave identically because the request
// is replayed through the exact /v1/chat/completions handler.
//
// One wire format, one parser, one emitter:
//
//   - The upstream request ALWAYS asks for SSE (stream:true), so the adapter
//     parses exactly one response format: OpenAI SSE events.
//   - The client's stream preference is honored at the EMIT boundary, not by
//     a second parser: streaming clients receive each Ollama NDJSON line as
//     it is produced; non-streaming clients accumulate the same events and
//     receive one JSON object (the "fold").
//
// Honesty rules (hard-won over five review rounds on MR !28 — a fold must
// never lie):
//   - A stream that ends without [DONE] and without finish_reason reports
//     done_reason "truncate" — never a fabricated "stop".
//   - An in-band SSE error event is an error, never an empty success. Before
//     any bytes reach the client it becomes an Ollama {"error": ...} with a
//     502; after content has streamed it becomes an Ollama error line, which
//     is what real Ollama emits.
//   - A backend that answers stream:true with a single JSON body is
//     translated through the same emitter (real backends do this); anything
//     unparseable surfaces as a 502, never as raw upstream bytes with 200.

// ollamaVersion is reported by /api/version. Clients only use it as a liveness
// probe; keeping it static avoids coupling to release metadata.
const ollamaVersion = "0.1.40"

// OllamaHandler serves the Ollama-native endpoints.
type OllamaHandler struct {
	openai *OpenAIHandler
}

// NewOllamaHandler wires the Ollama endpoints onto the OpenAI handler so both
// share one routing/proxy/logging stack.
func NewOllamaHandler(openai *OpenAIHandler) *OllamaHandler {
	return &OllamaHandler{openai: openai}
}

// ---- request shapes ----

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaOptions struct {
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	TopK        *int     `json:"top_k,omitempty"`
	NumPredict  *int     `json:"num_predict,omitempty"`
	Seed        *int64   `json:"seed,omitempty"`
}

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   *bool           `json:"stream"`
	Format   json.RawMessage `json:"format"`
	Options  ollamaOptions   `json:"options"`
}

type ollamaGenerateRequest struct {
	Model   string          `json:"model"`
	Prompt  string          `json:"prompt"`
	System  string          `json:"system"`
	Suffix  string          `json:"suffix"`
	Stream  *bool           `json:"stream"`
	Format  json.RawMessage `json:"format"`
	Options ollamaOptions   `json:"options"`
}

// ollamaToOpenAIChat builds the OpenAI /v1/chat/completions body. The
// upstream call is ALWAYS streaming (see architecture note above) — the
// client's stream preference is applied when emitting, never when asking.
// Unknown ollama options are ignored silently, per the issue's contract.
func ollamaToOpenAIChat(model string, msgs []ollamaMessage, opts ollamaOptions, format json.RawMessage) map[string]any {
	om := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		om = append(om, map[string]any{"role": m.Role, "content": m.Content})
	}
	body := map[string]any{
		"model":    model,
		"messages": om,
		"stream":   true,
		// Ask OpenAI-compatible upstreams for a final usage chunk so eval
		// counts survive the round trip. Backends that don't know the
		// option ignore it.
		"stream_options": map[string]any{"include_usage": true},
	}
	if opts.Temperature != nil {
		body["temperature"] = *opts.Temperature
	}
	if opts.TopP != nil {
		body["top_p"] = *opts.TopP
	}
	if opts.TopK != nil {
		body["top_k"] = *opts.TopK
	}
	if opts.NumPredict != nil {
		// Ollama: -1 (or 0) means "no limit". OpenAI max_tokens must be
		// positive, so negative/unbounded values are omitted entirely
		// rather than forwarded as max_tokens:-1.
		if *opts.NumPredict > 0 {
			body["max_tokens"] = *opts.NumPredict
		}
	}
	if opts.Seed != nil {
		body["seed"] = *opts.Seed
	}
	// format:"json" -> response_format json_object; a JSON-schema object is
	// wrapped in OpenAI's structured-outputs envelope (raw pass-through is
	// not a valid response_format).
	if len(format) > 0 {
		var s string
		if err := json.Unmarshal(format, &s); err == nil {
			if s == "json" {
				body["response_format"] = map[string]any{"type": "json_object"}
			}
		} else {
			body["response_format"] = map[string]any{
				"type": "json_schema",
				"json_schema": map[string]any{
					"name":   "response",
					"schema": json.RawMessage(format),
				},
			}
		}
	}
	return body
}

// generateMessages turns the generate-dialect prompt fields into chat messages.
func generateMessages(r ollamaGenerateRequest) []ollamaMessage {
	var msgs []ollamaMessage
	if r.System != "" {
		msgs = append(msgs, ollamaMessage{Role: "system", Content: r.System})
	}
	prompt := r.Prompt
	if r.Suffix != "" {
		// Ollama's completion-style suffix: instruct rather than silently splice.
		prompt = prompt + "\n" + r.Suffix
	}
	msgs = append(msgs, ollamaMessage{Role: "user", Content: prompt})
	return msgs
}

// ---- response primitives ----

// writeOllamaLine emits one NDJSON line and flushes — Ollama clients parse
// line-delimited JSON and stall without a per-line flush.
func writeOllamaLine(w http.ResponseWriter, flusher http.Flusher, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return err
	}
	if flusher != nil {
		flusher.Flush()
	}
	return nil
}

// ollamaError writes an Ollama-style error body: {"error": "..."}.
func ollamaError(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func ollamaNow() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ollamaBodyMessage extracts a human message from an OpenAI error body.
func ollamaBodyMessage(body string) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(body), &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return "upstream error"
	}
	return body
}

// ---- emitter: OpenAI chat events -> Ollama payloads ----

// ollamaEmitter converts parsed OpenAI chat events into Ollama payloads.
// One per request. In fold mode (non-streaming client) deltas accumulate
// silently and the single final payload carries the full content; in stream
// mode each delta is its own line and the final line is terminal with empty
// content (matching real Ollama).
type ollamaEmitter struct {
	model    string
	generate bool
	fold     bool
	start    time.Time

	content      strings.Builder
	reasoning    strings.Builder
	finishReason string
	promptN      int
	evalN        int
	firstByte    time.Time
	usageAt      time.Time
	finished     bool
}

func (e *ollamaEmitter) usage(prompt, completion int) {
	e.promptN = prompt
	e.evalN = completion
	e.usageAt = time.Now()
}

func (e *ollamaEmitter) finishReasonSeen(fr string) {
	if fr != "" {
		e.finishReason = fr
	}
}

// delta records a content delta and returns the payload to emit, or nil when
// nothing should be written (finished, empty delta, or fold mode).
func (e *ollamaEmitter) delta(content, reasoning string) any {
	if e.finished || (content == "" && reasoning == "") {
		return nil
	}
	if e.firstByte.IsZero() {
		e.firstByte = time.Now()
	}
	e.content.WriteString(content)
	e.reasoning.WriteString(reasoning)
	if e.fold {
		return nil
	}
	return e.payload(content, reasoning, false, "")
}

// final returns the terminal payload (in fold mode: the complete response),
// or nil if already finished. When the upstream never sent [DONE] nor a
// finish_reason, truncated=true makes done_reason report "truncate" instead
// of claiming a clean "stop".
func (e *ollamaEmitter) final(truncated bool) any {
	if e.finished {
		return nil
	}
	e.finished = true
	doneReason := "stop"
	switch {
	case e.finishReason != "":
		// Upstream stated how it ended (stop|length|content_filter|...) —
		// that always beats the missing-[DONE] heuristic.
		doneReason = e.finishReason
	case truncated:
		doneReason = "truncate"
	}
	return e.payload(e.content.String(), e.reasoning.String(), true, doneReason)
}

func (e *ollamaEmitter) payload(content, reasoning string, done bool, doneReason string) map[string]any {
	p := map[string]any{
		"model":      e.model,
		"created_at": ollamaNow(),
		"done":       done,
	}
	if e.generate {
		p["response"] = content
		if reasoning != "" {
			p["thinking"] = reasoning
		}
	} else {
		msg := map[string]any{"role": "assistant", "content": content}
		if reasoning != "" {
			msg["thinking"] = reasoning
		}
		p["message"] = msg
	}
	if !done {
		return p
	}
	p["done_reason"] = doneReason
	p["prompt_eval_count"] = e.promptN
	p["eval_count"] = e.evalN
	// Honest durations: eval_duration spans first delta -> end; prompt_eval
	// is approximated as time-to-first-token (start -> first delta) since we
	// cannot observe upstream prefill. Zero when nothing streamed.
	var evalDur, promptDur int64
	if !e.firstByte.IsZero() {
		promptDur = e.firstByte.Sub(e.start).Nanoseconds()
		end := e.usageAt
		if end.Before(e.firstByte) {
			end = time.Now()
		}
		evalDur = end.Sub(e.firstByte).Nanoseconds()
		if evalDur < 0 {
			evalDur = 0
		}
	}
	p["total_duration"] = time.Since(e.start).Nanoseconds()
	p["eval_duration"] = evalDur
	p["prompt_eval_duration"] = promptDur
	return p
}

// ---- adapter: ResponseWriter shim for proxyAPIRequest ----

// ollamaAdapter is the ResponseWriter handed to proxyAPIRequest. Because the
// upstream request always asks for SSE, it parses exactly one format: OpenAI
// SSE events, translated through the emitter. Emitted payloads go to the
// live client (stream mode) or are captured in out (fold mode). Headers are
// always deferred: the client must never see upstream status/Content-Type,
// only Ollama-shaped ones.
type ollamaAdapter struct {
	live http.ResponseWriter
	em   *ollamaEmitter
	fold bool

	out     bytes.Buffer // fold-mode sink; also buffers upstream error/JSON bodies
	folded  bool         // fold mode: final payload captured in out
	ownHdr  http.Header  // isolates upstream headers from the client
	status  int
	sawHdr  bool // WriteHeader observed (vs. implicit 200)
	sawSSE  bool // upstream Content-Type was text/event-stream
	sse     []byte
	sawDone bool
	errMsg  string // in-band SSE error event message
	wrote   bool   // stream mode: response headers already sent to client
}

// Header returns an isolated map: proxy.go copies the upstream response
// header map into the live ResponseWriter before WriteHeader, so Header()
// must NOT be the live map or upstream values leak into the Ollama response.
func (a *ollamaAdapter) Header() http.Header { return a.ownHdr }

func (a *ollamaAdapter) WriteHeader(code int) {
	if a.sawHdr {
		return
	}
	a.status = code
	a.sawHdr = true
	// proxy.go copied upstream headers into ownHdr by now; note whether this
	// is a real event-stream before those headers are discarded at emit time.
	a.sawSSE = strings.Contains(a.ownHdr.Get("Content-Type"), "text/event-stream")
}

func (a *ollamaAdapter) Write(p []byte) (int, error) {
	if !a.sawHdr {
		a.WriteHeader(http.StatusOK)
	}
	// Everything that is not a healthy SSE stream is buffered: error bodies
	// (mapped in finish) and the JSON-instead-of-SSE case (translated there).
	if a.status >= 400 || !a.sawSSE {
		return a.out.Write(p)
	}
	a.sse = append(a.sse, p...)
	for {
		i := bytes.IndexByte(a.sse, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(a.sse[:i]), "\r")
		a.sse = a.sse[i+1:]
		if err := a.handleSSELine(line); err != nil {
			return len(p), err
		}
	}
	return len(p), nil
}

func (a *ollamaAdapter) handleSSELine(line string) error {
	if !strings.HasPrefix(line, "data:") {
		return nil
	}
	data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if data == "[DONE]" {
		a.sawDone = true
		return a.emit(a.em.final(false), true)
	}
	var chunk struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Choices []struct {
			Delta struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return nil // tolerate chunks we don't understand
	}
	// An in-band error event is NOT a completion. Record it; finish()
	// decides between an HTTP error (nothing streamed yet) and an Ollama
	// error line (content already delivered).
	if chunk.Error != nil {
		if a.errMsg == "" {
			a.errMsg = chunk.Error.Message
		}
		return nil
	}
	if chunk.Usage != nil {
		a.em.usage(chunk.Usage.PromptTokens, chunk.Usage.CompletionTokens)
	}
	for _, c := range chunk.Choices {
		if c.FinishReason != nil {
			a.em.finishReasonSeen(*c.FinishReason)
		}
		if err := a.emit(a.em.delta(c.Delta.Content, c.Delta.ReasoningContent), false); err != nil {
			return err
		}
	}
	return nil
}

// emit routes one payload to the sink: live NDJSON line (stream mode) or the
// fold buffer (non-stream mode, where only the final payload arrives).
// terminal=true forces response headers even for a nil payload, so a stream
// that produced no visible delta still gets an NDJSON content-type.
func (a *ollamaAdapter) emit(v any, terminal bool) error {
	if v == nil {
		if terminal && !a.fold && !a.wrote {
			a.live.Header().Set("Content-Type", "application/x-ndjson")
			a.live.WriteHeader(http.StatusOK)
			a.wrote = true
		}
		return nil
	}
	if a.fold {
		a.out.Reset()
		a.folded = true
		return json.NewEncoder(&a.out).Encode(v)
	}
	if !a.wrote {
		a.live.Header().Set("Content-Type", "application/x-ndjson")
		a.live.WriteHeader(http.StatusOK)
		a.wrote = true
	}
	flusher, _ := a.live.(http.Flusher)
	return writeOllamaLine(a.live, flusher, v)
}

// translateBufferedJSON handles a backend that answered stream:true with a
// single JSON chat-completion body: feed it through the same emitter as one
// delta plus the terminal line, so both client modes get valid Ollama output.
func (a *ollamaAdapter) translateBufferedJSON() error {
	var resp struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(a.out.Bytes(), &resp); err != nil || len(resp.Choices) == 0 {
		// The live response is now committed with an error; mark it so
		// finish() never writes a second body into the same response.
		a.wrote = true
		ollamaError(a.live, "upstream returned an untranslatable body", http.StatusBadGateway)
		return nil
	}
	a.em.usage(resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
	a.em.finishReasonSeen(resp.Choices[0].FinishReason)
	m := resp.Choices[0].Message
	if err := a.emit(a.em.delta(m.Content, m.ReasoningContent), false); err != nil {
		return err
	}
	return a.emit(a.em.final(false), true)
}

// finish is called once after proxyAPIRequest returns. It maps everything
// that did not produce a clean stream into an Ollama-shaped response, then
// (fold mode) writes the single captured body.
func (a *ollamaAdapter) finish() error {
	status := a.status
	if !a.sawHdr {
		status = http.StatusOK
	}

	// Error status from relai or the backend: rewrite as an Ollama error.
	if status >= 400 {
		ollamaError(a.live, ollamaBodyMessage(a.out.String()), status)
		return nil
	}

	switch {
	case a.errMsg != "" && !a.wrote && !a.folded:
		// In-band SSE error before anything reached the client: honest 502,
		// never an empty success.
		ollamaError(a.live, firstNonEmpty(a.errMsg, "upstream error"), http.StatusBadGateway)
		return nil

	case !a.sawSSE && !a.wrote && !a.folded:
		// 200 with a non-SSE body: a backend that ignored stream:true.
		if err := a.translateBufferedJSON(); err != nil {
			return err
		}

	case a.errMsg != "" && !a.em.finished:
		// In-band SSE error after content already streamed: an Ollama error
		// line is what real Ollama emits. (Fold mode never sets wrote, so
		// this is the stream-after-content case only.)
		a.em.finished = true
		if err := a.emit(map[string]any{"error": a.errMsg}, true); err != nil {
			return err
		}

	default:
		// Stream ended without [DONE]: close it honestly — truncated only
		// when the upstream never said how it ended.
		if err := a.emit(a.em.final(!a.sawDone && a.em.finishReason == ""), true); err != nil {
			return err
		}
	}

	if a.fold {
		if !a.folded {
			// A committed live error (e.g. untranslatable body) already owns
			// the response; never append a second body to it.
			if a.wrote {
				return nil
			}
			ollamaError(a.live, "upstream produced no completion", http.StatusBadGateway)
			return nil
		}
		a.live.Header().Set("Content-Type", "application/json")
		a.live.WriteHeader(http.StatusOK)
		_, err := a.live.Write(a.out.Bytes())
		return err
	}
	return nil
}

// ---- shared dispatch ----

// dispatch rewrites the request into an OpenAI chat/completions call and runs
// it through the exact same proxy path /v1 uses (virtual routers, admission,
// host limits, logging). The upstream call always streams; the adapter emits
// Ollama NDJSON (stream) or folds it into one JSON body (non-stream).
// generate selects the /api/generate response shape ("response" field) over
// the /api/chat shape ("message" object).
func (h *OllamaHandler) dispatch(w http.ResponseWriter, r *http.Request, openaiBody map[string]any, stream, generate bool, model string) {
	body, err := json.Marshal(openaiBody)
	if err != nil {
		ollamaError(w, "failed to encode request", http.StatusInternalServerError)
		return
	}

	// Clone with the OpenAI path so requestMode() and any path-based logic
	// behave as if the client had called /v1/chat/completions.
	r2 := r.Clone(r.Context())
	r2.Body = NewReplayBody(body)
	r2.ContentLength = int64(len(body))
	r2.URL.Path = "/v1/chat/completions"
	r2.URL.RawPath = ""

	aw := &ollamaAdapter{
		live:   w,
		fold:   !stream,
		ownHdr: http.Header{},
		em: &ollamaEmitter{
			model:    model,
			generate: generate,
			fold:     !stream,
			start:    time.Now(),
		},
	}
	h.openai.proxyAPIRequest(aw, r2)
	// finish() emits the terminal line / folded body / mapped error.
	_ = aw.finish()
}

// ---- handlers ----

// HandleChat serves POST /api/chat (Ollama chat dialect).
func (h *OllamaHandler) HandleChat(w http.ResponseWriter, r *http.Request) {
	var req ollamaChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ollamaError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Model == "" {
		ollamaError(w, "model is required", http.StatusBadRequest)
		return
	}
	if len(req.Messages) == 0 {
		ollamaError(w, "messages is required", http.StatusBadRequest)
		return
	}
	stream := req.Stream == nil || *req.Stream // Ollama defaults to streaming
	body := ollamaToOpenAIChat(req.Model, req.Messages, req.Options, req.Format)
	h.dispatch(w, r, body, stream, false, req.Model)
}

// HandleGenerate serves POST /api/generate (Ollama generate dialect).
func (h *OllamaHandler) HandleGenerate(w http.ResponseWriter, r *http.Request) {
	var req ollamaGenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ollamaError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Model == "" {
		ollamaError(w, "model is required", http.StatusBadRequest)
		return
	}
	if req.Prompt == "" {
		ollamaError(w, "prompt is required", http.StatusBadRequest)
		return
	}
	stream := req.Stream == nil || *req.Stream
	body := ollamaToOpenAIChat(req.Model, generateMessages(req), req.Options, req.Format)
	h.dispatch(w, r, body, stream, true, req.Model)
}

// HandleTags serves GET /api/tags — virtual routers presented as models.
func (h *OllamaHandler) HandleTags(w http.ResponseWriter, r *http.Request) {
	routers, err := h.openai.db.Routers.List()
	if err != nil {
		ollamaError(w, "failed to list models", http.StatusInternalServerError)
		return
	}
	models := make([]map[string]any, 0, len(routers))
	for _, rt := range routers {
		models = append(models, map[string]any{
			"name":        rt.Name,
			"model":       rt.Name,
			"modified_at": rt.UpdatedAt.Format(time.RFC3339),
			"size":        0,
			"digest":      "",
			"details": map[string]any{
				"parent_model":       "",
				"format":             "gguf",
				"family":             "relai",
				"parameter_size":     "",
				"quantization_level": "",
			},
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
}

// HandleVersion serves GET /api/version.
func (h *OllamaHandler) HandleVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"version": ollamaVersion})
}
