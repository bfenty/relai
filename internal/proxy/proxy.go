package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/bfenty/relai/internal/compress"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bfenty/relai/internal/db"
	"github.com/bfenty/relai/internal/events"
	"github.com/bfenty/relai/internal/prefix"
	"github.com/bfenty/relai/internal/queue"
)

// Proxy forwards requests to backend LLM servers.
type Proxy struct {
	compressors sync.Map // compress.Mode -> *compress.Compressor, shared so the
	// content-addressed cache survives across requests
	capsMu   sync.Mutex                 // guards caps
	caps     map[string]capabilityEntry // baseURL -> probed capabilities
	db       *db.DB
	bus      *events.Bus
	tracker  *queue.Tracker
	speeds   *queue.SpeedLearner // learned per-host tok/s; nil-safe
	client   *http.Client
	timeouts Timeouts
}

// RequestContext carries caller-facing metadata into request logging.
type RequestContext struct {
	APIKeyID       *int64
	RequestedModel string
	ClientKey      string

	// Interactive tells the proxy to tag the upstream request with
	// X-Halogen-Priority: 1. Set when the calling API key carries priority>0;
	// Halogen's admission reserve (HALOGEN_ADMISSION_RESERVE) then admits these
	// ahead of the bulk queue. Other backends ignore the header.
	Interactive bool

	// Routing metadata, filled in by the virtual router before proxying so the
	// request log can record how the target was chosen. Without these a stale
	// affinity or a silent fallthrough is invisible: the request simply looks
	// slow, and at deep context a fallthrough costs a full prompt reprocess.
	RouterName      string
	Compression     string // router-level default, used when the mapping sets none
	CompressedBytes int    // bytes removed from this request by compression
	ModelMapID      *int64
	SessionKey      string // already a hash; stored truncated
	RoutingDecision string // see the routing* constants
	WaitMs          int    // time spent waiting for a member before proxying

	// Prefix diagnostics, filled in by the virtual router. cached_tokens on the
	// response says whether the KV prefix was reused; these say what broke it.
	PrefixCause      string
	PrefixDivergedAt int
	PrefixRetained   float64
	PrefixMessages   int

	// Adaptive thinking (issue #10), filled in by the adaptive_thinking
	// strategy before proxying. ThinkingParamsJSON carries the per-backend
	// param body to merge into the forwarded request (enforce mode only —
	// shadow mode leaves it empty and the body is byte-for-byte unchanged).
	// The rest is observability for request_logs.
	ThinkingLevel      string   `json:"-"`
	ThinkingConfidence *float64 `json:"-"`
	ClassifierMs       *int     `json:"-"`
	ThinkingReason     string   `json:"-"`
	ThinkingParamsJSON string   `json:"-"`
}

// Routing decisions recorded on each request.
const (
	RoutingStickyHit   = "sticky_hit"   // the session's own host was free
	RoutingStickyWait  = "sticky_wait"  // waited for the session's host, then got it
	RoutingStickySpill = "sticky_spill" // gave up on the session's host, went elsewhere
	RoutingFresh       = "fresh"        // no session yet; placed by load
	RoutingDirect      = "direct"       // not routed through a virtual router
)

// Timeouts separates long prompt evaluation from a stalled stream. A response may
// take a long time to start for large-context reasoning, but after it begins an
// absence of SSE progress is a failed request and must free the backend slot.
type Timeouts struct {
	ResponseHeader time.Duration
	StreamIdle     time.Duration
}

func DefaultTimeouts() Timeouts {
	return Timeouts{
		ResponseHeader: 15 * time.Minute,
		StreamIdle:     2 * time.Minute,
	}
}

// New creates a proxy. Optional timeouts preserve the defaults for tests and
// callers that do not need deployment-specific policy.
func New(d *db.DB, bus *events.Bus, t *queue.Tracker, configured ...Timeouts) *Proxy {
	timeouts := DefaultTimeouts()
	if len(configured) > 0 {
		if configured[0].ResponseHeader > 0 {
			timeouts.ResponseHeader = configured[0].ResponseHeader
		}
		if configured[0].StreamIdle > 0 {
			timeouts.StreamIdle = configured[0].StreamIdle
		}
	}
	return &Proxy{
		db:       d,
		bus:      bus,
		tracker:  t,
		timeouts: timeouts,
		client: &http.Client{
			// A Client.Timeout applies to the entire SSE lifetime; leave it unset
			// so productive long streams can continue past the header deadline.
			Transport: &http.Transport{
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   10,
				IdleConnTimeout:       90 * time.Second,
				ResponseHeaderTimeout: timeouts.ResponseHeader,
			},
		},
	}
}

// SetSpeedLearner attaches the per-backend throughput learner. The proxy folds
// each completed request's measured prefill/decode speed into it, which is what
// lets the queue view estimate progress while a prompt is still processing.
func (p *Proxy) SetSpeedLearner(l *queue.SpeedLearner) { p.speeds = l }

// SpeedLearner returns the attached throughput learner (may be nil).
func (p *Proxy) SpeedLearner() *queue.SpeedLearner {
	if p == nil {
		return nil
	}
	return p.speeds
}

// TrackQueued registers a request waiting inside Relai before upstream proxying.
func (p *Proxy) TrackQueued(routerName, model, clientKey string) string {
	if p == nil || p.tracker == nil {
		return ""
	}
	return p.tracker.AddQueued(routerName, model, clientKey)
}

// RemoveTracked removes a request from the live tracker.
func (p *Proxy) RemoveTracked(id string) {
	if p == nil || p.tracker == nil || id == "" {
		return
	}
	p.tracker.Remove(id)
}

// ProxyRequest forwards a request to a backend and streams the response.
func (p *Proxy) ProxyRequest(w http.ResponseWriter, r *http.Request, backend *db.Backend, modelMapping *db.ModelMap, ctx *RequestContext) error {
	start := time.Now()

	// Build the target URL — model's BaseURL overrides the backend's when set
	baseURL := backend.BaseURL
	if modelMapping != nil && modelMapping.BaseURL != "" {
		baseURL = modelMapping.BaseURL
	}
	targetURL := strings.TrimRight(baseURL, "/") + r.URL.Path

	// Create the request
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}
	defer r.Body.Close()

	// Determine client-facing model name (before body is rewritten)
	modelName := extractModelFromBody(body)
	if ctx != nil && ctx.RequestedModel != "" {
		modelName = ctx.RequestedModel
	}
	if modelMapping != nil && modelName == "" {
		modelName = modelMapping.LocalModel
	}

	// Register in live queue — no content stored
	trackID := p.tracker.Add(backend.ID, backend.Name, modelName)
	defer p.tracker.Remove(trackID)

	upstreamCtx, cancelUpstream := context.WithCancel(r.Context())
	defer cancelUpstream()
	targetReq, err := http.NewRequestWithContext(upstreamCtx, r.Method, targetURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	// Copy relevant headers
	for key, values := range r.Header {
		if key == "Authorization" || key == "Host" || key == "Content-Length" {
			continue
		}
		for _, v := range values {
			targetReq.Header.Add(key, v)
		}
	}

	// Halogen admission priority is relai's decision, never the client's:
	// a caller cannot claim a reserved slot by sending the header itself.
	targetReq.Header.Del("X-Halogen-Priority")
	if ctx != nil && ctx.Interactive {
		targetReq.Header.Set("X-Halogen-Priority", "1")
	}

	// Set backend auth
	if backend.APIKey != "" {
		targetReq.Header.Set("Authorization", "Bearer "+backend.APIKey)
	}
	targetReq.Header.Set("Content-Type", "application/json")
	targetReq.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))

	// Transform model name and merge model-level params
	var inputCostPerM, outputCostPerM float64
	// Only streaming chat completions can carry prompt_progress events, so only
	// those requests trigger the (cached) Halogen capability probe. Embedding,
	// rerank, and passthrough traffic never touches it.
	halogenProgress := isChatCompletionsPath(r.URL.Path) && bodyWantsStreaming(body) &&
		p.supportsPromptProgress(baseURL)
	if modelMapping != nil {
		inputCostPerM = modelMapping.InputCostPerMillion
		outputCostPerM = modelMapping.OutputCostPerMillion
		// Compression mode comes from the mapping, falling back to the router that
		// selected it, so it can be set per target or for a whole router at once.
		mode := compress.ParseMode(modelMapping.Compression)
		if mode == compress.ModeOff && ctx != nil {
			mode = compress.ParseMode(ctx.Compression)
		}
		var cstats compress.Stats
		var thinkingParams string
		if ctx != nil {
			thinkingParams = ctx.ThinkingParamsJSON
		}
		body, cstats, err = transformRequestWithCompression(body, modelMapping.LocalModel,
			modelMapping.RemoteModel, modelMapping.Params, thinkingParams, p.compressorFor(mode), halogenProgress)
		if ctx != nil {
			ctx.CompressedBytes = cstats.Saved()
		}
		if err != nil {
			return fmt.Errorf("transform request: %w", err)
		}
		targetReq.Body = io.NopCloser(bytes.NewReader(body))
		targetReq.ContentLength = int64(len(body))
		targetReq.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
	}

	// Prompt size estimate for live throughput: the forwarded body minus JSON
	// overhead, at the ~4 bytes/token heuristic used elsewhere here. The exact
	// count only arrives in the final usage chunk, which is far too late to
	// drive a progress bar.
	estPromptTokens := (len(body) - 100) / 4
	if estPromptTokens < 0 {
		estPromptTokens = 0
	}
	p.tracker.Update(trackID, queue.Update{EstPromptTokens: estPromptTokens, EstPromptTokensOk: estPromptTokens > 0})

	// Execute the request
	resp, err := p.client.Do(targetReq)
	if err != nil {
		latency := time.Since(start).Milliseconds()
		p.logRequest(ctx, backend, r, modelName, 0, 0, 0, 0, 0, 0, http.StatusBadGateway, latency, err.Error(), 0, 0, nil, 0, 0)
		return fmt.Errorf("proxy request: %w", err)
	}
	defer resp.Body.Close()

	// Copy response headers
	for key, values := range resp.Header {
		for _, v := range values {
			if key != "Transfer-Encoding" && key != "Content-Length" {
				w.Header().Add(key, v)
			}
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Stream the response
	var responseReader io.Reader = resp.Body
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		// SSE streaming - parse for token counts and stop only after genuine
		// post-header inactivity, not after an arbitrary total wall-clock time.
		responseReader = &StreamingReader{
			Reader: &idleTimeoutReader{
				reader:  resp.Body,
				timeout: p.timeouts.StreamIdle,
				cancel:  cancelUpstream,
			},
			Proxy:           p,
			Backend:         backend,
			Request:         r,
			Model:           modelName,
			StartTime:       start,
			InputCostPerM:   inputCostPerM,
			OutputCostPerM:  outputCostPerM,
			StatusCode:      resp.StatusCode,
			Context:         ctx,
			TrackID:         trackID,
			Tracker:         p.tracker,
			EstPromptTokens: estPromptTokens,
			StripProgress:   halogenProgress,
		}
	} else {
		// Non-streaming - read full response for token counts
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			latency := time.Since(start).Milliseconds()
			p.logRequest(ctx, backend, r, modelName, 0, 0, 0, 0, 0, 0, resp.StatusCode, latency, err.Error(), 0, 0, nil, 0, 0)
			_, _ = w.Write([]byte(`{"error":"failed to read response"}`))
			return err
		}

		tokens := extractTokenCounts(respBody, inputCostPerM, outputCostPerM)
		latency := time.Since(start).Milliseconds()
		errorMsg := ""
		if resp.StatusCode >= 400 {
			errorMsg = fmt.Sprintf("backend returned status %d", resp.StatusCode)
		}
		// Non-streaming responses interleave prefill and generation in one
		// opaque wait, so no phase split is recorded.
		p.logRequest(ctx, backend, r, modelName, tokens.PromptTokens, tokens.CompletionTokens, tokens.ThinkingTokens, tokens.TotalTokens, tokens.CachedTokens, tokens.Cost, resp.StatusCode, latency, errorMsg, 0, 0, nil, tokens.DraftN, tokens.DraftNAccepted)
		_, _ = w.Write(respBody)
	}

	_, err = io.Copy(w, responseReader)
	return err
}

func (p *Proxy) logRequest(ctx *RequestContext, backend *db.Backend, r *http.Request, model string, promptTokens, completionTokens, thinkingTokens, totalTokens, cachedTokens int, cost float64, statusCode int, latencyMs int64, errorMsg string, prefillMs, decodeMs int, decodeStartAt *time.Time, draftN, draftAccept int) {
	var apiKeyID *int64
	if ctx != nil {
		apiKeyID = ctx.APIKeyID
	}
	input := db.LogInput{
		APIKeyID:       apiKeyID,
		BackendID:      &backend.ID,
		Model:          model,
		Endpoint:       r.URL.Path,
		RequestTokens:  promptTokens,
		ResponseTokens: completionTokens,
		ThinkingTokens: thinkingTokens,
		TotalTokens:    totalTokens,
		Cost:           cost,
		StatusCode:     statusCode,
		LatencyMs:      int(latencyMs),
		ErrorMessage:   errorMsg,
	}
	if ctx != nil {
		input.ModelMapID = ctx.ModelMapID
		input.RouterName = ctx.RouterName
		input.SessionKey = truncateSessionKey(ctx.SessionKey)
		input.RoutingDecision = ctx.RoutingDecision
		input.WaitMs = ctx.WaitMs
		input.CompressedBytes = ctx.CompressedBytes
		// The request-time diagnosis knows whether history was rewritten; only now,
		// with the backend's reported reuse in hand, can an intact history that still
		// missed the cache be attributed to the host rather than the client.
		input.PrefixCause = prefix.Classify(ctx.PrefixCause, promptTokens, cachedTokens,
			ctx.RoutingDecision == RoutingStickySpill)
		input.PrefixDivergedAt = ctx.PrefixDivergedAt
		input.PrefixRetained = ctx.PrefixRetained
		input.PrefixMessages = ctx.PrefixMessages
		input.ThinkingLevel = stringPtrIfNotEmpty(ctx.ThinkingLevel)
		input.ThinkingConfidence = ctx.ThinkingConfidence
		input.ClassifierMs = ctx.ClassifierMs
		input.ThinkingReason = ctx.ThinkingReason
	}
	input.CachedTokens = cachedTokens
	input.PrefillMs = prefillMs
	input.DecodeMs = decodeMs
	input.DecodeStartAt = decodeStartAt
	input.DraftN = draftN
	input.DraftNAccepted = draftAccept
	_, _ = p.db.Logs.Create(input)
	if apiKeyID != nil {
		_ = p.db.APIKeys.UpdateUsage(*apiKeyID, cost)
	}

	// Update backend latency
	if latencyMs > 0 {
		p.db.Backends.UpdateHealth(backend.ID, "active", int(latencyMs))
	}

	// Publish stat delta for live Dashboard updates.
	if p.bus != nil {
		delta := events.StatDelta{
			Requests: 1,
			Cost:     cost,
			Tokens:   int64(totalTokens),
			Latency:  float64(latencyMs),
		}
		if statusCode >= 400 && errorMsg != "" {
			delta.Errors = 1
			p.bus.PublishError(events.ErrorLogEntry{
				Model:        model,
				ErrorMessage: errorMsg,
				StatusCode:   statusCode,
				CreatedAt:    time.Now().UTC().Format(time.RFC3339),
			})
		}
		p.bus.PublishStat(delta)
	}
}

// StreamingReader wraps the response body, parses SSE events for token counts, and logs on EOF.
type StreamingReader struct {
	io.Reader
	Proxy           *Proxy
	Backend         *db.Backend
	Request         *http.Request
	Model           string
	StartTime       time.Time
	InputCostPerM   float64
	OutputCostPerM  float64
	StatusCode      int
	Context         *RequestContext
	TrackID         string
	Tracker         *queue.Tracker
	EstPromptTokens int
	// StripProgress removes engine prompt_progress chunks from the client
	// stream. They carry no choices and would confuse strict OpenAI clients;
	// the reader consumes them for the queue tracker instead.
	StripProgress     bool
	lineBuf           []byte
	outBuf            []byte
	lastUsage         *TokenCounts
	lastDraftN        int
	lastDraftAccept   int
	reasoningBytes    int
	seenReasoning     bool
	seenContent       bool
	generatedDeltas   int
	prefillMsRecorded bool
	firstDeltaAt      time.Time
	done              bool
}

func (s *StreamingReader) Read(p []byte) (n int, err error) {
	if len(s.outBuf) > 0 {
		n = copy(p, s.outBuf)
		s.outBuf = s.outBuf[n:]
		return n, nil
	}
	n, err = s.Reader.Read(p)
	if n > 0 {
		if !s.StripProgress {
			// Fast path: nothing to filter, just scan complete lines.
			s.lineBuf = append(s.lineBuf, p[:n]...)
			for {
				idx := bytes.IndexByte(s.lineBuf, '\n')
				if idx < 0 {
					break
				}
				line := strings.TrimRight(string(s.lineBuf[:idx]), "\r")
				s.lineBuf = s.lineBuf[idx+1:]
				s.processLine(line)
			}
			return n, err
		}
		// Stripping mode: re-emit only the lines the client should see.
		src := p[:n]
		s.lineBuf = append(s.lineBuf, src...)
		for {
			idx := bytes.IndexByte(s.lineBuf, '\n')
			if idx < 0 {
				break
			}
			line := s.lineBuf[:idx]
			s.lineBuf = s.lineBuf[idx+1:]
			s.processLine(strings.TrimRight(string(line), "\r"))
			if isProgressLine(line) {
				continue
			}
			s.outBuf = append(s.outBuf, line...)
			s.outBuf = append(s.outBuf, '\n')
		}
		if len(s.outBuf) > 0 {
			m := copy(p, s.outBuf)
			s.outBuf = s.outBuf[m:]
			return m, nil
		}
		// Every byte read was stripped; report a zero-byte read without
		// swallowing a real EOF.
		if err == nil {
			return 0, nil
		}
		return 0, err
	}
	if err == io.EOF && !s.done {
		s.done = true
		s.logOnComplete()
	}
	return n, err
}

// isProgressLine reports whether an SSE data line is a prompt_progress chunk
// (no choices, carries the progress object). Cheap prefix checks first; the
// JSON unmarshal only runs on the rare candidate.
func isProgressLine(line []byte) bool {
	if !bytes.HasPrefix(line, []byte("data: ")) {
		return false
	}
	if !bytes.Contains(line, []byte(`"prompt_progress"`)) {
		return false
	}
	return true
}

func (s *StreamingReader) processLine(line string) {
	if !strings.HasPrefix(line, "data: ") {
		return
	}
	data := strings.TrimPrefix(line, "data: ")
	if data == "[DONE]" {
		return
	}
	var chunk struct {
		Usage *struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			TotalTokens         int `json:"total_tokens"`
			PromptTokensDetails *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details,omitempty"`
		} `json:"usage"`
		Choices []struct {
			Delta struct {
				ReasoningContent *string `json:"reasoning_content"`
				Content          *string `json:"content"`
			} `json:"delta"`
		} `json:"choices"`
		PromptProgress *struct {
			Total     int `json:"total"`
			Cache     int `json:"cache"`
			Processed int `json:"processed"`
			TimeMs    int `json:"time_ms"`
		} `json:"prompt_progress"`
		Timings *Timings `json:"timings"`
	}
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return
	}
	if chunk.PromptProgress != nil {
		// Engine-truthful prefill progress (Halogen). Feed the tracker with
		// exact totals; the queue view prefers these over its estimate.
		pp := chunk.PromptProgress
		if pp.Total > 0 {
			s.Tracker.Update(s.TrackID, queue.Update{
				PromptTotal:     pp.Total,
				PromptProcessed: pp.Processed,
				PromptCached:    pp.Cache,
				PromptEngineMs:  pp.TimeMs,
			})
		}
		return
	}
	if chunk.Timings != nil && chunk.Timings.DraftN > 0 {
		// Halogen attaches timings to the finish and usage chunks; the last
		// non-zero report wins (they carry the same counters).
		s.lastDraftN = chunk.Timings.DraftN
		s.lastDraftAccept = chunk.Timings.DraftNAccepted
	}
	if chunk.Usage != nil {
		cached := 0
		if chunk.Usage.PromptTokensDetails != nil {
			cached = chunk.Usage.PromptTokensDetails.CachedTokens
		}
		s.lastUsage = &TokenCounts{
			PromptTokens:     chunk.Usage.PromptTokens,
			CompletionTokens: chunk.Usage.CompletionTokens,
			TotalTokens:      chunk.Usage.TotalTokens,
			CachedTokens:     cached,
		}
	}
	if len(chunk.Choices) > 0 {
		d := chunk.Choices[0].Delta
		hasReasoning := d.ReasoningContent != nil && *d.ReasoningContent != ""
		hasContent := d.Content != nil && *d.Content != ""
		if hasReasoning {
			s.reasoningBytes += len(*d.ReasoningContent)
			if !s.seenReasoning {
				s.seenReasoning = true
				s.Tracker.Transition(s.TrackID, queue.PhaseThinking)
			}
		}
		if hasContent && !s.seenContent {
			s.seenContent = true
			s.Tracker.Transition(s.TrackID, queue.PhaseStreaming)
		}
		if hasReasoning || hasContent {
			// llama.cpp-style servers emit roughly one token per delta, so the
			// delta count is a live token counter without a tokenizer.
			s.generatedDeltas++
			if s.firstDeltaAt.IsZero() {
				s.firstDeltaAt = time.Now()
				// Prefill is over: the host has consumed the whole prompt and is
				// generating. Prefill duration is start-to-first-token — response
				// headers arrive before evaluation finishes, so they bound nothing.
				if !s.prefillMsRecorded {
					s.prefillMsRecorded = true
					s.Tracker.Update(s.TrackID, queue.Update{
						PromptMs:        s.firstDeltaAt.Sub(s.StartTime).Milliseconds(),
						FirstTokenAt:    s.firstDeltaAt,
						GeneratedTokens: s.generatedDeltas,
					})
				}
			}
			if !s.prefillMsRecorded {
				s.Tracker.Update(s.TrackID, queue.Update{GeneratedTokens: s.generatedDeltas, FirstTokenAt: s.firstDeltaAt})
			} else {
				s.Tracker.Update(s.TrackID, queue.Update{GeneratedTokens: s.generatedDeltas})
			}
		}
	}
}

func (s *StreamingReader) logOnComplete() {
	tokens := TokenCounts{}
	if s.lastUsage != nil {
		tokens = *s.lastUsage
	}
	// Reconcile the live view with the real usage the upstream finally
	// reported. The prefill rate divides by freshly evaluated tokens only —
	// cached tokens were never processed — so the corrected estimate carries
	// prompt minus cache hits.
	if tokens.PromptTokens > 0 {
		fresh := tokens.PromptTokens - tokens.CachedTokens
		if fresh < 0 {
			fresh = 0
		}
		s.Tracker.Update(s.TrackID, queue.Update{EstPromptTokens: fresh, EstPromptTokensOk: true})
	}
	if tokens.CompletionTokens > 0 {
		s.Tracker.Update(s.TrackID, queue.Update{GeneratedTokens: tokens.CompletionTokens})
	}
	s.recordSpeed(tokens)
	thinkingTokens := s.reasoningBytes / 4
	latency := time.Since(s.StartTime).Milliseconds()
	cost := float64(tokens.PromptTokens)/1_000_000*s.InputCostPerM +
		float64(tokens.CompletionTokens)/1_000_000*s.OutputCostPerM
	errorMsg := ""
	if s.StatusCode >= 400 {
		errorMsg = fmt.Sprintf("backend returned status %d", s.StatusCode)
	}
	s.Proxy.logRequest(s.Context, s.Backend, s.Request, s.Model,
		tokens.PromptTokens, tokens.CompletionTokens,
		thinkingTokens, tokens.TotalTokens, tokens.CachedTokens,
		cost, s.StatusCode, latency, errorMsg,
		s.prefillMs(), s.decodeMs(), s.decodeStartAt(),
		s.lastDraftN, s.lastDraftAccept)
}

// decodeStartAt is the wall-clock instant of the first streamed token, or nil
// when the decode phase was never observed.
func (s *StreamingReader) decodeStartAt() *time.Time {
	if s.firstDeltaAt.IsZero() {
		return nil
	}
	t := s.firstDeltaAt
	return &t
}

// prefillMs / decodeMs are the persisted phase timings: send-to-first-token and
// first-token-to-end. Zero when the phase was never observed.
func (s *StreamingReader) prefillMs() int {
	if s.firstDeltaAt.IsZero() {
		return 0
	}
	return int(s.firstDeltaAt.Sub(s.StartTime).Milliseconds())
}

func (s *StreamingReader) decodeMs() int {
	if s.firstDeltaAt.IsZero() {
		return 0
	}
	return int(time.Since(s.firstDeltaAt).Milliseconds())
}

// minMeasuredPrefillTokens skips learner samples with too few freshly evaluated
// tokens. Prefill time carries fixed overhead (scheduling, batch wait), so a
// request that only evaluated a handful of new tokens reports a rate far below
// the host's real throughput and would drag the EWMA down.
const minMeasuredPrefillTokens = 32

// recordSpeed folds this request's measured throughput into the per-backend
// learner. Prefill speed uses the real prompt token count when the upstream
// reported usage, falling back to the body estimate; decode speed uses real
// completion tokens when present, else the counted deltas.
//
// Prefill counts only tokens the host actually evaluated: usage.prompt_tokens
// includes KV-cache hits, and under sticky routing most of a prompt can be
// cached, so dividing the full count by the (short) prefill time reports cache
// reads as GPU throughput — observed ~16k tok/s against a true ~343. A request
// with no measurable fresh prefill teaches the learner nothing and is skipped.
func (s *StreamingReader) recordSpeed(tokens TokenCounts) {
	p := s.Proxy
	if p == nil || p.speeds == nil || s.Backend == nil {
		return
	}
	var sample queue.SpeedSample
	prompt := tokens.PromptTokens
	if prompt <= 0 {
		prompt = s.EstPromptTokens
	}
	fresh := prompt - tokens.CachedTokens
	if ms := s.prefillMs(); ms > 0 && fresh > minMeasuredPrefillTokens {
		sample.PromptTokS = float64(fresh) / (float64(ms) / 1000.0)
	}
	completion := tokens.CompletionTokens
	if completion <= 0 {
		completion = s.generatedDeltas
	}
	if !s.firstDeltaAt.IsZero() {
		if secs := time.Since(s.firstDeltaAt).Seconds(); secs > 0 && completion > 0 {
			sample.DecodeTokS = float64(completion) / secs
		}
	}
	p.speeds.Record(s.Backend.ID, sample)
}

// TokenCounts holds extracted token information.
type TokenCounts struct {
	PromptTokens     int
	CompletionTokens int
	ThinkingTokens   int
	TotalTokens      int
	CachedTokens     int
	Cost             float64
	DraftN           int
	DraftNAccepted   int
}

// truncateSessionKey keeps enough of the (already hashed) session key to group a
// conversation's requests together in analytics without storing the whole digest.
func truncateSessionKey(k string) string {
	const max = 24
	if len(k) <= max {
		return k
	}
	return k[:max]
}

// stringPtrIfNotEmpty keeps unclassified rows NULL in request_logs: an empty
// thinking level means "no classifier ran for this request", which analytics
// must be able to tell apart from a classified level.
func stringPtrIfNotEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// isChatCompletionsPath matches the OpenAI and Ollama chat endpoints (with or
// without an /api prefix or version segment).
func isChatCompletionsPath(path string) bool {
	return strings.HasSuffix(path, "/chat/completions") || strings.HasSuffix(path, "/api/chat")
}

// bodyWantsStreaming reports whether the request body asks for a streamed
// response. Unparseable bodies count as "no" — the transform will fail anyway.
func bodyWantsStreaming(body []byte) bool {
	var probe struct {
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return false
	}
	return probe.Stream
}

// compressorFor returns the shared Compressor for a mode. Sharing matters only for
// the cache hit rate: the transform is pure, so a fresh Compressor would produce
// identical bytes.
func (p *Proxy) compressorFor(mode compress.Mode) *compress.Compressor {
	if mode == compress.ModeOff {
		return nil
	}
	if v, ok := p.compressors.Load(mode); ok {
		return v.(*compress.Compressor)
	}
	c := compress.New(mode)
	actual, _ := p.compressors.LoadOrStore(mode, c)
	return actual.(*compress.Compressor)
}
