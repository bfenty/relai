package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bfenty/relai/internal/db"
	"github.com/bfenty/relai/internal/hostlimit"
	"github.com/bfenty/relai/internal/proxy"
	"github.com/bfenty/relai/internal/router"
	"github.com/bfenty/relai/internal/server/middleware"
	"github.com/bfenty/relai/internal/vrouter"
	"github.com/bfenty/relai/pkg/openai"
)

// OpenAIHandler handles OpenAI-compatible API requests.
type OpenAIHandler struct {
	db               *db.DB
	proxy            *proxy.Proxy
	registry         *router.Registry
	vrouter          *vrouter.Manager
	limiter          *hostlimit.Limiter
	admissionTimeout time.Duration
	admission        map[string]*admissionState
	admissionM       sync.Mutex
}

// NewOpenAIHandler creates a new OpenAI API handler. limiter is the shared
// per-host concurrency limiter (also used by the virtual router manager).
func NewOpenAIHandler(d *db.DB, p *proxy.Proxy, r *router.Registry, v *vrouter.Manager, limiter *hostlimit.Limiter, admissionTimeout ...time.Duration) *OpenAIHandler {
	if limiter == nil {
		limiter = hostlimit.New()
	}
	timeout := 5 * time.Minute
	if len(admissionTimeout) > 0 && admissionTimeout[0] > 0 {
		timeout = admissionTimeout[0]
	}
	return &OpenAIHandler{db: d, proxy: p, registry: r, vrouter: v, limiter: limiter, admissionTimeout: timeout, admission: make(map[string]*admissionState)}
}

type admissionState struct {
	active map[string]int
	queue  []*admissionWaiter
}

type admissionWaiter struct {
	clientKey string
	granted   bool
	grantCh   chan struct{}
}

const maxActiveDirectRequestsPerClient = 1

// HandleCompletions handles POST /v1/chat/completions and POST /v1/completions.
func (h *OpenAIHandler) HandleCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":{"message":"method not allowed"}}`, http.StatusMethodNotAllowed)
		return
	}
	h.proxyAPIRequest(w, r)
}

// HandleEmbeddings handles POST /v1/embeddings.
func (h *OpenAIHandler) HandleEmbeddings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":{"message":"method not allowed"}}`, http.StatusMethodNotAllowed)
		return
	}
	h.proxyAPIRequest(w, r)
}

// HandleRerank handles POST /v1/rerank (Cohere/Jina-compatible).
func (h *OpenAIHandler) HandleRerank(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":{"message":"method not allowed"}}`, http.StatusMethodNotAllowed)
		return
	}
	h.proxyAPIRequest(w, r)
}

// proxyAPIRequest is the shared routing and proxying logic for all OpenAI-compatible endpoints.
func (h *OpenAIHandler) proxyAPIRequest(w http.ResponseWriter, r *http.Request) {
	apiKey := middleware.APIKeyFromContext(r.Context())
	if apiKey == nil {
		http.Error(w, `{"error":{"message":"missing API key"}}`, http.StatusUnauthorized)
		return
	}

	var rawMsg json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&rawMsg); err != nil {
		writeJSONError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	model := extractModelField(rawMsg)
	if model == "" {
		writeJSONError(w, "model is required", http.StatusBadRequest)
		return
	}
	mode := requestMode(r.URL.Path)
	if !apiKey.HasScope(scopeForMode(mode)) {
		writeJSONError(w, "API key scope does not allow this endpoint", http.StatusForbidden)
		return
	}
	proxyCtx := &proxy.RequestContext{
		APIKeyID:       apiKeyLogID(apiKey),
		RequestedModel: model,
		ClientKey:      admissionClientKey(apiKey),
		Interactive:    apiKey != nil && apiKey.Priority > 0,
		// Default so every request logged from here carries a decision. The virtual
		// router overwrites this when it routes. An empty decision then means only one
		// thing - a row written before routing was recorded at all - which lets the
		// analytics exclude those rather than counting their prompts with no cache data.
		RoutingDecision: proxy.RoutingDirect,
	}

	// Virtual router takes priority — allows routing between embedding/rerank backends too
	if rt, err := h.db.Routers.GetByName(model); err == nil && rt != nil {
		r.Body = NewReplayBody(rawMsg)
		if err := h.vrouter.Route(w, r, rt, rawMsg, proxyCtx); err != nil {
			writeJSONError(w, err.Error(), http.StatusBadGateway)
		}
		return
	}

	admissionCtx, cancelAdmission := context.WithTimeout(r.Context(), h.admissionTimeout)
	defer cancelAdmission()
	releaseAdmission, queued, err := h.acquireAdmission(admissionCtx, model, proxyCtx)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer releaseAdmission()
	if queued {
		w.Header().Set("X-Relai-Queued", "true")
	}

	var backend *db.Backend
	var modelMapping *db.ModelMap

	mappings, _ := h.db.ModelMaps.ResolveModel(model)
	if len(mappings) > 0 {
		// Prefer a mapping whose declared mode matches the request, but fall back
		// to any active backend that serves this model even if it isn't tagged
		// for this mode. The mode tag is metadata for reporting (/v1/models), not
		// a hard wall: an explicit request for a known model should still route,
		// matching how virtual routers already behave.
		var fallback *db.ModelMap
		var fallbackBackend *db.Backend
		for i := range mappings {
			b, err := h.db.Backends.Get(mappings[i].BackendID)
			if err != nil || b.Status != "active" {
				continue
			}
			m := mappings[i]
			if mappingSupportsMode(m, mode) {
				backend = b
				modelMapping = &m
				break
			}
			if fallbackBackend == nil {
				fallbackBackend = b
				fallback = &m
			}
		}
		if backend == nil && fallbackBackend != nil {
			backend = fallbackBackend
			modelMapping = fallback
			h.db.SystemEvents.Log(db.LevelWarn, db.EventProxy,
				fmt.Sprintf("model %q routed for mode %q without a matching mode tag", model, mode),
				map[string]any{"model": model, "mode": string(mode), "backend": backend.Name})
		}
		if backend == nil {
			writeJSONError(w, fmt.Sprintf("no active backend serves model %q", model), http.StatusServiceUnavailable)
			return
		}
	} else {
		req := &router.RoutingRequest{
			Model:    model,
			Mode:     mode,
			APIKeyID: apiKey.ID,
		}
		var err error
		backend, err = h.registry.SelectBackend(r.Context(), req)
		if err != nil {
			writeJSONError(w, fmt.Sprintf("no available backend: %v", err), http.StatusServiceUnavailable)
			return
		}
	}

	// API key backend restriction
	if len(apiKey.AllowedBackends) > 0 {
		allowed := false
		for _, id := range apiKey.AllowedBackends {
			if id == backend.ID {
				allowed = true
				break
			}
		}
		if !allowed {
			writeJSONError(w, "API key not authorized for this backend", http.StatusForbidden)
			return
		}
	}

	// API key model restriction
	if len(apiKey.AllowedModels) > 0 {
		allowed := false
		for _, m := range apiKey.AllowedModels {
			if m == model {
				allowed = true
				break
			}
		}
		if !allowed {
			writeJSONError(w, "API key not authorized for this model", http.StatusForbidden)
			return
		}
	}

	releaseActive := h.registry.TrackActive(&router.RoutingTarget{Backend: backend, ModelMap: modelMapping})
	defer releaseActive()

	// Per-host concurrency: share the host's slots with the virtual router
	// path so one machine never serves more requests than it can handle.
	slot := h.limiter.Slot(backend.ID, backend.MaxConcurrent)
	if !slot.TryAcquire() {
		w.Header().Set("X-Relai-Queued", "true")
		trackID := h.proxy.TrackQueued("host:"+backend.Name, model, proxyCtx.ClientKey)
		err := slot.Acquire(admissionCtx)
		h.proxy.RemoveTracked(trackID)
		if err != nil {
			writeJSONError(w, fmt.Sprintf("timeout waiting for host %q capacity", backend.Name), http.StatusServiceUnavailable)
			return
		}
	}
	defer slot.Release()

	r.Body = NewReplayBody(rawMsg)
	if err := h.proxy.ProxyRequest(w, r, backend, modelMapping, proxyCtx); err != nil {
		writeJSONError(w, err.Error(), http.StatusBadGateway)
	}
}

func (h *OpenAIHandler) acquireAdmission(ctx context.Context, model string, proxyCtx *proxy.RequestContext) (func(), bool, error) {
	clientKey := queueClientKey(proxyCtx)
	key := model + ":" + clientKey

	h.admissionM.Lock()
	state := h.admissionStateLocked(model)
	if len(state.queue) == 0 && state.active[clientKey] < maxActiveDirectRequestsPerClient {
		state.active[clientKey]++
		h.admissionM.Unlock()
		return func() { h.releaseAdmission(model, clientKey) }, false, nil
	}

	waiter := &admissionWaiter{
		clientKey: clientKey,
		grantCh:   make(chan struct{}),
	}
	state.queue = append(state.queue, waiter)
	h.dispatchAdmissionsLocked(state)
	h.admissionM.Unlock()

	trackID := h.proxy.TrackQueued("model:"+model, model, clientKey)
	select {
	case <-waiter.grantCh:
		h.proxy.RemoveTracked(trackID)
		return func() { h.releaseAdmission(model, clientKey) }, true, nil
	case <-ctx.Done():
		h.admissionM.Lock()
		granted := waiter.granted
		if !granted {
			state.queue = removeAdmissionWaiter(state.queue, waiter)
		}
		h.admissionM.Unlock()
		h.proxy.RemoveTracked(trackID)
		if granted {
			return func() { h.releaseAdmission(model, clientKey) }, true, nil
		}
		return nil, true, fmt.Errorf("timeout waiting for model admission: %s", key)
	}
}

func (h *OpenAIHandler) admissionStateLocked(model string) *admissionState {
	state := h.admission[model]
	if state == nil {
		state = &admissionState{active: make(map[string]int)}
		h.admission[model] = state
	}
	return state
}

func (h *OpenAIHandler) releaseAdmission(model, clientKey string) {
	h.admissionM.Lock()
	defer h.admissionM.Unlock()
	state := h.admission[model]
	if state == nil {
		return
	}
	if state.active[clientKey] > 1 {
		state.active[clientKey]--
	} else {
		delete(state.active, clientKey)
	}
	h.dispatchAdmissionsLocked(state)
}

func (h *OpenAIHandler) dispatchAdmissionsLocked(state *admissionState) {
	if state == nil || len(state.queue) == 0 {
		return
	}
	remaining := state.queue[:0]
	for _, waiter := range state.queue {
		if state.active[waiter.clientKey] >= maxActiveDirectRequestsPerClient {
			remaining = append(remaining, waiter)
			continue
		}
		state.active[waiter.clientKey]++
		waiter.granted = true
		close(waiter.grantCh)
	}
	state.queue = remaining
}

func removeAdmissionWaiter(queue []*admissionWaiter, target *admissionWaiter) []*admissionWaiter {
	for i, waiter := range queue {
		if waiter == target {
			copy(queue[i:], queue[i+1:])
			queue[len(queue)-1] = nil
			return queue[:len(queue)-1]
		}
	}
	return queue
}

func queueClientKey(proxyCtx *proxy.RequestContext) string {
	if proxyCtx == nil || proxyCtx.ClientKey == "" {
		return "anonymous"
	}
	return proxyCtx.ClientKey
}

// HandleModels handles GET /v1/models.
func (h *OpenAIHandler) HandleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":{"message":"method not allowed"}}`, http.StatusMethodNotAllowed)
		return
	}
	if apiKey := middleware.APIKeyFromContext(r.Context()); apiKey == nil || !apiKey.HasScope("models:read") {
		writeJSONError(w, "API key scope does not allow this endpoint", http.StatusForbidden)
		return
	}

	mappings, err := h.db.ModelMaps.List()
	if err != nil {
		writeJSONError(w, "failed to list models", http.StatusInternalServerError)
		return
	}

	// Dedupe by local model name; report the type so clients can distinguish
	// chat/embedding/rerank models. A non-chat type wins if the same name is
	// served by multiple backends with differing types.
	seen := map[string]int{} // local_model -> index in data
	var data []openai.Model
	mapsByName := map[string][]int64{} // local_model -> model-map IDs
	for _, m := range mappings {
		mt := modelTypeForMapping(m)
		if idx, ok := seen[m.LocalModel]; ok {
			if data[idx].Type == "chat" && mt != "chat" {
				data[idx].Type = mt
			}
			mapsByName[m.LocalModel] = append(mapsByName[m.LocalModel], m.ID)
			continue
		}
		seen[m.LocalModel] = len(data)
		mapsByName[m.LocalModel] = []int64{m.ID}
		data = append(data, openai.Model{
			ID:      m.LocalModel,
			Object:  "model",
			Created: 1700000000,
			OwnedBy: "relai",
			Type:    mt,
		})
	}

	if len(data) == 0 {
		// Fall back to listing backend models when no mappings exist
		backends, _ := h.db.Backends.ListActive()
		for _, b := range backends {
			maps, _ := h.db.Backends.GetModels(b.ID)
			for _, m := range maps {
				if _, ok := seen[m.LocalModel]; ok {
					mapsByName[m.LocalModel] = append(mapsByName[m.LocalModel], m.ID)
					continue
				}
				seen[m.LocalModel] = len(data)
				mapsByName[m.LocalModel] = []int64{m.ID}
				data = append(data, openai.Model{
					ID:      m.LocalModel,
					Object:  "model",
					Created: 1700000000,
					OwnedBy: b.Name,
					Type:    modelTypeForMapping(m),
				})
			}
		}
	}

	// Always include virtual routers — owned_by "relai/router" lets clients distinguish them
	routers, _ := h.db.Routers.List()
	for _, rt := range routers {
		t := rt.ModelType
		if t == "" {
			t = "chat"
		}
		members, _ := h.db.Routers.GetMembers(rt.ID)
		var memberMaps []int64
		for _, mem := range members {
			memberMaps = append(memberMaps, mem.ModelMapID)
		}
		seen[rt.Name] = len(data)
		mapsByName[rt.Name] = memberMaps
		data = append(data, openai.Model{
			ID:      rt.Name,
			Object:  "model",
			Created: rt.CreatedAt.Unix(),
			OwnedBy: "relai/router",
			Type:    t,
		})
	}

	// Attach the context window each model's backends advertise. Honest
	// worst-case semantics: a model served by several backends reports the
	// SMALLEST advertised window, and a model with no reporting backend
	// omits the field rather than fabricating one.
	for i := range data {
		if ctx := h.resolveContextLength(mapsByName[data[i].ID]); ctx > 0 {
			data[i].ContextLength = ctx
		}
	}

	result := openai.ModelList{Object: "list", Data: data}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result) //nolint
}

// resolveContextLength returns the smallest context window advertised by the
// backends behind the given model-map IDs, or 0 when none report one.
// Resolution is map -> backend base URL -> cached capability probe, so warm
// backends cost nothing and cold ones at most one /health (or /props) request
// per capabilityTTL.
func (h *OpenAIHandler) resolveContextLength(mapIDs []int64) int {
	if h.proxy == nil {
		return 0
	}
	best := 0
	for _, id := range mapIDs {
		mm, err := h.db.ModelMaps.Get(id)
		if err != nil {
			continue
		}
		baseURL := mm.BaseURL
		if baseURL == "" {
			b, err := h.db.Backends.Get(mm.BackendID)
			if err != nil {
				continue
			}
			baseURL = b.BaseURL
		}
		if ctx := h.proxy.ContextLength(baseURL); ctx > 0 && (best == 0 || ctx < best) {
			best = ctx
		}
	}
	return best
}

// HandleModelGet handles GET /v1/models/:id.
func (h *OpenAIHandler) HandleModelGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":{"message":"method not allowed"}}`, http.StatusMethodNotAllowed)
		return
	}
	if apiKey := middleware.APIKeyFromContext(r.Context()); apiKey == nil || !apiKey.HasScope("models:read") {
		writeJSONError(w, "API key scope does not allow this endpoint", http.StatusForbidden)
		return
	}

	modelID := strings.TrimPrefix(r.URL.Path, "/v1/models/")
	if modelID == "" {
		writeJSONError(w, "model ID is required", http.StatusBadRequest)
		return
	}

	mappings, _ := h.db.ModelMaps.List()
	var matchIDs []int64
	var matchType string
	for _, m := range mappings {
		if m.LocalModel == modelID {
			matchIDs = append(matchIDs, m.ID)
			if matchType == "" {
				matchType = modelTypeForMapping(m)
			}
		}
	}
	if len(matchIDs) > 0 {
		result := openai.Model{
			ID:      modelID,
			Object:  "model",
			Created: 1700000000,
			OwnedBy: "relai",
			Type:    matchType,
		}
		if ctx := h.resolveContextLength(matchIDs); ctx > 0 {
			result.ContextLength = ctx
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result) //nolint
		return
	}

	// Virtual routers are addressable as models too.
	if rt, err := h.db.Routers.GetByName(modelID); err == nil && rt != nil {
		t := rt.ModelType
		if t == "" {
			t = "chat"
		}
		result := openai.Model{
			ID:      modelID,
			Object:  "model",
			Created: rt.CreatedAt.Unix(),
			OwnedBy: "relai/router",
			Type:    t,
		}
		if members, err := h.db.Routers.GetMembers(rt.ID); err == nil {
			var memberMaps []int64
			for _, mem := range members {
				memberMaps = append(memberMaps, mem.ModelMapID)
			}
			if ctx := h.resolveContextLength(memberMaps); ctx > 0 {
				result.ContextLength = ctx
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result) //nolint
		return
	}

	writeJSONError(w, fmt.Sprintf("model %q not found", modelID), http.StatusNotFound)
}

func writeJSONError(w http.ResponseWriter, message string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(proxy.FormatError(message, status))
}

func scopeForMode(mode db.ModelMode) string {
	switch mode {
	case "embed":
		return "embeddings"
	case "rerank":
		return "rerank"
	default:
		return "inference"
	}
}

func apiKeyLogID(apiKey *db.APIKey) *int64 {
	if apiKey == nil || apiKey.ID == 0 {
		return nil
	}
	return &apiKey.ID
}

func admissionClientKey(apiKey *db.APIKey) string {
	if apiKey == nil || apiKey.ID == 0 {
		return "session"
	}
	return fmt.Sprintf("api_key:%d", apiKey.ID)
}

// extractModelField extracts the "model" field from any OpenAI-compatible request body.
func extractModelField(raw json.RawMessage) string {
	var v struct {
		Model string `json:"model"`
	}
	json.Unmarshal(raw, &v) //nolint
	return v.Model
}

func requestMode(path string) db.ModelMode {
	switch {
	case strings.Contains(path, "embeddings") || strings.Contains(path, "embed"):
		return db.ModelModeEmbed
	case strings.Contains(path, "rerank"):
		return db.ModelModeRerank
	default:
		return db.ModelModeChat
	}
}

// modelTypeForMapping returns a mapping's reporting type ("chat", "embedding",
// or "rerank"), preferring the stored model_type and falling back to its modes.
func modelTypeForMapping(m db.ModelMap) string {
	if m.ModelType != "" {
		return m.ModelType
	}
	for _, mode := range m.Modes {
		switch mode {
		case db.ModelModeEmbed:
			return "embedding"
		case db.ModelModeRerank:
			return "rerank"
		}
	}
	return "chat"
}

func mappingSupportsMode(mapping db.ModelMap, mode db.ModelMode) bool {
	if len(mapping.Modes) == 0 {
		return mode == db.ModelModeChat
	}
	for _, m := range mapping.Modes {
		if m == mode {
			return true
		}
	}
	return false
}

// ReplayBody wraps a byte slice so the proxy can read the already-decoded body.
type ReplayBody struct {
	*bytes.Reader
}

func NewReplayBody(data []byte) *ReplayBody {
	return &ReplayBody{bytes.NewReader(data)}
}

func (r *ReplayBody) Close() error { return nil }
