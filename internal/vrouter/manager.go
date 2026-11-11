package vrouter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bfenty/relai/internal/db"
	"github.com/bfenty/relai/internal/hostlimit"
	"github.com/bfenty/relai/internal/prefix"
	"github.com/bfenty/relai/internal/proxy"
)

// Manager routes requests for virtual router models across their member backends.
type Manager struct {
	db    *db.DB
	proxy *proxy.Proxy

	// limiter holds the per-host (backend) concurrency slots shared with the
	// direct proxy path: all models on one machine compete for the same slots.
	limiter *hostlimit.Limiter

	mu      sync.RWMutex
	members map[int64]*memberState // model_map_id → state

	sessMu   sync.RWMutex
	sessions map[string]sessionEntry

	cacheMu sync.RWMutex
	cache   map[string]cacheEntry

	cacheStatusMu sync.RWMutex
	cacheStatuses map[int64]cacheStatus // model-map ID → latest backend /cache snapshot

	admissionMu sync.Mutex
	admissions  map[int64]*admissionState

	// prefixes diagnoses why a prompt missed the backend's KV cache. It holds only
	// per-message hashes, never prompt content.
	prefixes *prefix.Tracker

	// Per-router round-robin cursor (router ID → atomic counter).
	rrMu      sync.RWMutex
	rrCursors map[int64]*atomic.Uint64

	healthClient *http.Client
}

// memberState tracks health for one model mapping endpoint. Concurrency is
// not per member: all members on the same host (backend) share one
// hostlimit.Slot, so a machine serving several models never runs more
// concurrent requests than its host capacity allows.
type memberState struct {
	mu       sync.Mutex
	health   string // "active" | "degraded" | "offline"
	failures int
	slot     *hostlimit.Slot // shared per-host semaphore
}

type sessionEntry struct {
	modelMapID int64
	lastSeen   time.Time
}

type cacheEntry struct {
	response  []byte
	expiresAt time.Time
}

// cacheStatus is a best-effort snapshot from a Halogen-compatible /cache endpoint.
// It is never treated as health: unavailable or malformed telemetry simply falls
// back to the existing least-connections behavior.
type cacheStatus struct {
	available      bool
	tokenHitRate   float64
	poolUsageRatio float64
	updatedAt      time.Time

	// Informational Halogen /cache telemetry. Parsed for the admin UI only;
	// routing decisions continue to use tokenHitRate/poolUsageRatio so a
	// backend that adds or renames fields cannot change behavior.
	poolPositions     int64
	poolUsed          int64
	entries           int
	maxEntries        int
	entryBytes        int64
	evicted           int64
	dropped           int64
	superseded        int64
	promptTokensSaved int64
	hitRate           float64

	// Engine-side queue depth, from the backend's /health beside the /cache
	// poll (Halogen: queued + longest_wait_s). relai's own admission queue is
	// separate; this is what the engine itself is holding back.
	engineQueued    int
	engineWaitSecs  float64
	engineQueueSeen bool
}

// CacheTelemetry is the admin-facing snapshot of one model map's KV-cache
// telemetry, as last polled from the backend's /cache endpoint.
type CacheTelemetry struct {
	ModelMapID        int64   `json:"model_map_id"`
	Available         bool    `json:"available"`
	TokenHitRate      float64 `json:"token_hit_rate"`
	PoolUsageRatio    float64 `json:"pool_usage_ratio"`
	PoolPositions     int64   `json:"pool_positions"`
	PoolUsed          int64   `json:"pool_used"`
	Entries           int     `json:"entries"`
	MaxEntries        int     `json:"max_entries"`
	EntryBytes        int64   `json:"entry_bytes"`
	Evicted           int64   `json:"evicted"`
	Dropped           int64   `json:"dropped"`
	Superseded        int64   `json:"superseded"`
	PromptTokensSaved int64   `json:"prompt_tokens_saved"`
	HitRate           float64 `json:"hit_rate"`
	// Engine-side queue snapshot (Halogen /health): requests the engine is
	// holding in its own queue and the longest wait so far. EngineQueueSeen
	// distinguishes "engine reports no queue" from "engine doesn't report".
	EngineQueued    int     `json:"engine_queued"`
	EngineWaitSecs  float64 `json:"engine_wait_s"`
	EngineQueueSeen bool    `json:"engine_queue_seen"`
	AgeSeconds      int64   `json:"age_seconds"`
}

// CacheTelemetrySnapshot returns the latest polled KV-cache telemetry per model
// map. Stale entries (older than 5 minutes) report Available=false so the UI can
// distinguish "backend went quiet" from "backend never exposed /cache".
func (m *Manager) CacheTelemetrySnapshot() []CacheTelemetry {
	if m == nil {
		return nil
	}
	m.cacheStatusMu.RLock()
	defer m.cacheStatusMu.RUnlock()
	out := make([]CacheTelemetry, 0, len(m.cacheStatuses))
	for id, st := range m.cacheStatuses {
		age := int64(time.Since(st.updatedAt).Seconds())
		out = append(out, CacheTelemetry{
			ModelMapID:        id,
			Available:         st.available && age <= 300,
			TokenHitRate:      st.tokenHitRate,
			PoolUsageRatio:    st.poolUsageRatio,
			PoolPositions:     st.poolPositions,
			PoolUsed:          st.poolUsed,
			Entries:           st.entries,
			MaxEntries:        st.maxEntries,
			EntryBytes:        st.entryBytes,
			Evicted:           st.evicted,
			Dropped:           st.dropped,
			Superseded:        st.superseded,
			PromptTokensSaved: st.promptTokensSaved,
			HitRate:           st.hitRate,
			EngineQueued:      st.engineQueued,
			EngineWaitSecs:    st.engineWaitSecs,
			EngineQueueSeen:   st.engineQueueSeen,
			AgeSeconds:        age,
		})
	}
	return out
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

// MemberStatus is the live view returned by GetStatus.
type MemberStatus struct {
	MemberID       int64  `json:"member_id"`
	ModelMapID     int64  `json:"model_map_id"`
	LocalModel     string `json:"local_model"`
	BackendName    string `json:"backend_name"`
	Health         string `json:"health"`
	ActiveRequests int    `json:"active_requests"`
	QueueDepth     int    `json:"queue_depth"`
}

func NewManager(d *db.DB, p *proxy.Proxy, limiter *hostlimit.Limiter) *Manager {
	if limiter == nil {
		limiter = hostlimit.New()
	}
	return &Manager{
		db:            d,
		proxy:         p,
		limiter:       limiter,
		members:       make(map[int64]*memberState),
		sessions:      make(map[string]sessionEntry),
		cache:         make(map[string]cacheEntry),
		cacheStatuses: make(map[int64]cacheStatus),
		admissions:    make(map[int64]*admissionState),
		rrCursors:     make(map[int64]*atomic.Uint64),
		prefixes:      prefix.NewTracker(),
		healthClient:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (m *Manager) rrCursor(routerID int64) *atomic.Uint64 {
	m.rrMu.RLock()
	c := m.rrCursors[routerID]
	m.rrMu.RUnlock()
	if c != nil {
		return c
	}
	m.rrMu.Lock()
	defer m.rrMu.Unlock()
	if c = m.rrCursors[routerID]; c == nil {
		c = &atomic.Uint64{}
		m.rrCursors[routerID] = c
	}
	return c
}

// Start launches background goroutines; ctx should live for the server lifetime.
func (m *Manager) Start(ctx context.Context) {
	go m.healthMonitorLoop(ctx)
	go m.sessionCleanupLoop(ctx)
	go m.cacheCleanupLoop(ctx)
}

// Route handles an incoming request destined for a virtual router model.
// body is the already-read request body (rawMsg from the OpenAI handler).
func (m *Manager) Route(w http.ResponseWriter, r *http.Request, router *db.Router, body []byte, proxyCtx *proxy.RequestContext) error {
	var req struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &req)

	// Semantic cache hit (non-streaming only)
	if router.CacheEnabled && !req.Stream {
		key := cacheKey(router.ID, body)
		if cached := m.cacheGet(key); cached != nil {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Relai-Cache", "hit")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(cached)
			return nil
		}
	}

	dbMembers, err := m.db.Routers.GetMembers(router.ID)
	if err != nil || len(dbMembers) == 0 {
		return fmt.Errorf("router %q has no members", router.Name)
	}
	states := m.getOrCreateStates(dbMembers)
	sessKey := computeSessionKey(r, body)
	// Diagnose the prompt's prefix before proxying. The tracker is keyed by the
	// caller rather than the session key, because the session key folds in the system
	// prompt: a client that stamps a timestamp into its system message would get a
	// fresh key every turn and hide the very fault worth catching.
	prefixDiff := m.prefixes.Observe(prefixKey(r, proxyCtx), prefix.Compute(body))

	timeout := time.Duration(router.QueueTimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	admissionCapacity := healthyAdmissionCapacity(states)
	releaseAdmission, queued, err := m.acquireAdmission(ctx, router, proxyCtx, admissionCapacity)
	if err != nil {
		return err
	}
	defer releaseAdmission()
	if queued {
		w.Header().Set("X-Relai-Queued", "true")
	}

	var decision string
	selStart := time.Now()
	sel, err := m.selectRoutableMember(ctx, router, states, dbMembers, sessKey, len(body), &decision)
	if err != nil {
		return err
	}
	if proxyCtx != nil {
		proxyCtx.RouterName = router.Name
		proxyCtx.Compression = router.Compression
		proxyCtx.SessionKey = sessKey
		proxyCtx.RoutingDecision = decision
		proxyCtx.WaitMs = int(time.Since(selStart).Milliseconds())
		proxyCtx.PrefixCause = prefixDiff.Cause
		proxyCtx.PrefixDivergedAt = prefixDiff.DivergedAt
		proxyCtx.PrefixRetained = prefixDiff.Retained
		proxyCtx.PrefixMessages = prefixDiff.NewLen
		id := sel.member.ModelMapID
		proxyCtx.ModelMapID = &id
	}
	defer sel.state.release()

	// Adaptive thinking (issue #10): only the new strategy reaches this; every
	// pre-existing strategy skips it entirely. Runs after placement so the
	// classifier budget never competes with the sticky-wait budget, and
	// before proxying so the decision can be injected at forward time.
	if normalizeStrategy(router.Strategy) == "adaptive_thinking" {
		m.applyAdaptiveThinking(r.Context(), router, body, proxyCtx)
		m.logThinkingDecision(router, proxyCtx)
	}

	r.Body = io.NopCloser(bytes.NewReader(body))
	m.annotateSelection(w, router, sel)
	m.logSelection(router, sel, sessKey != "")

	var proxyErr error
	if router.CacheEnabled && !req.Stream {
		cw := newCaptureWriter(w)
		proxyErr = m.proxy.ProxyRequest(cw, r, sel.backend, sel.modelMap, proxyCtx)
		if proxyErr == nil && cw.status < 400 {
			ttl := time.Duration(router.CacheTTLSeconds) * time.Second
			m.cacheSet(cacheKey(router.ID, body), cw.body.Bytes(), ttl)
		}
	} else {
		proxyErr = m.proxy.ProxyRequest(w, r, sel.backend, sel.modelMap, proxyCtx)
	}

	if proxyErr != nil {
		sel.state.mu.Lock()
		sel.state.failures++
		failures := sel.state.failures
		sel.state.mu.Unlock()
		if failures >= 3 {
			sel.state.setHealth("degraded")
			go m.healthCheckMember(sel.modelMap)
		}
		return proxyErr
	}

	// Success: reset failure count and update session affinity
	sel.state.mu.Lock()
	sel.state.failures = 0
	sel.state.mu.Unlock()
	if sessKey != "" {
		if normalizeStrategy(router.Strategy) == "cache_aware_sticky" {
			m.setPersistentSession(router.ID, sessKey, sel.member.ModelMapID)
		} else {
			m.setSession(sessKey, sel.member.ModelMapID)
		}
		m.recordFootprint(router.ID, sessKey, sel.member.ModelMapID, prefixDiff)
	}
	return nil
}

// recordFootprint persists the conversation's KV footprint estimate (#19) after
// a successful turn: retained_bytes approximates what the warm host must keep
// resident (current prompt size scaled by the prefix-retention fraction from
// the same request's prefix diff). Database trouble must never block routing,
// so errors are dropped — the footprint is an optimization input, not state the
// request path depends on.
func (m *Manager) recordFootprint(routerID int64, sessKey string, modelMapID int64, diff prefix.Diff) {
	if m.db == nil || m.db.SessionFootprints == nil || sessKey == "" || diff.NewBytes <= 0 {
		return
	}
	retained := diff.NewBytes
	if diff.Retained > 0 && diff.Retained < 1 {
		retained = int64(float64(diff.NewBytes) * diff.Retained)
	}
	_ = m.db.SessionFootprints.Upsert(routerID, persistentSessionKey(sessKey), modelMapID, retained)
}

// GetStatus returns the live per-member health and concurrency view for a router.
func (m *Manager) GetStatus(routerID int64) ([]MemberStatus, error) {
	dbMembers, err := m.db.Routers.GetMembers(routerID)
	if err != nil {
		return nil, err
	}
	result := make([]MemberStatus, 0, len(dbMembers))
	for _, mem := range dbMembers {
		modelMap, err := m.db.ModelMaps.Get(mem.ModelMapID)
		if err != nil {
			continue
		}
		backend, err := m.db.Backends.Get(modelMap.BackendID)
		if err != nil {
			continue
		}
		s := m.getOrCreateState(mem.ModelMapID)
		health := s.getHealth()
		active, waiters, _ := s.slot.Load()

		result = append(result, MemberStatus{
			MemberID:       mem.ID,
			ModelMapID:     mem.ModelMapID,
			LocalModel:     modelMap.LocalModel,
			BackendName:    backend.Name,
			Health:         health,
			ActiveRequests: active,
			QueueDepth:     waiters,
		})
	}
	return result, nil
}

// ─── Member selection ────────────────────────────────────────────────────────

func (m *Manager) acquireAdmission(ctx context.Context, router *db.Router, proxyCtx *proxy.RequestContext, capacity int) (func(), bool, error) {
	if capacity < 1 {
		capacity = 1
	}
	clientKey := admissionClientKey(proxyCtx)
	state := m.admissionState(router.ID)

	m.admissionMu.Lock()
	if len(state.queue) == 0 && activeAdmissionTotal(state) < capacity && state.active[clientKey] < capacity {
		state.active[clientKey]++
		m.admissionMu.Unlock()
		return func() { m.releaseAdmission(router.ID, clientKey, capacity) }, false, nil
	}

	waiter := &admissionWaiter{
		clientKey: clientKey,
		grantCh:   make(chan struct{}),
	}
	state.queue = append(state.queue, waiter)
	m.dispatchAdmissionsLocked(state, capacity)
	m.admissionMu.Unlock()

	trackID := m.trackQueuedAdmission(router, proxyCtx, clientKey)
	select {
	case <-waiter.grantCh:
		m.untrackQueuedAdmission(trackID)
		return func() { m.releaseAdmission(router.ID, clientKey, capacity) }, true, nil
	case <-ctx.Done():
		m.admissionMu.Lock()
		granted := waiter.granted
		if !granted {
			state.queue = removeAdmissionWaiter(state.queue, waiter)
		}
		m.admissionMu.Unlock()
		m.untrackQueuedAdmission(trackID)
		if granted {
			return func() { m.releaseAdmission(router.ID, clientKey, capacity) }, true, nil
		}
		return nil, true, fmt.Errorf("timeout waiting for fair router admission")
	}
}

// healthyAdmissionCapacity sums the concurrency capacity of each distinct
// healthy host. Members sharing a host contribute its capacity only once.
func healthyAdmissionCapacity(states []*memberState) int {
	seen := make(map[*hostlimit.Slot]bool)
	capacity := 0
	for _, state := range states {
		if state.getHealth() == "offline" || state.slot == nil || seen[state.slot] {
			continue
		}
		seen[state.slot] = true
		_, _, c := state.slot.Load()
		capacity += c
	}
	if capacity < 1 {
		return 1
	}
	return capacity
}

func (m *Manager) admissionState(routerID int64) *admissionState {
	m.admissionMu.Lock()
	defer m.admissionMu.Unlock()
	state := m.admissions[routerID]
	if state == nil {
		state = &admissionState{active: make(map[string]int)}
		m.admissions[routerID] = state
	}
	return state
}

func admissionClientKey(proxyCtx *proxy.RequestContext) string {
	if proxyCtx == nil || proxyCtx.ClientKey == "" {
		return "anonymous"
	}
	return proxyCtx.ClientKey
}

func (m *Manager) releaseAdmission(routerID int64, clientKey string, capacity int) {
	if capacity < 1 {
		capacity = 1
	}
	m.admissionMu.Lock()
	defer m.admissionMu.Unlock()
	state := m.admissions[routerID]
	if state == nil {
		return
	}
	if state.active[clientKey] > 1 {
		state.active[clientKey]--
	} else {
		delete(state.active, clientKey)
	}
	m.dispatchAdmissionsLocked(state, capacity)
}

func (m *Manager) dispatchAdmissionsLocked(state *admissionState, capacity int) {
	if state == nil || len(state.queue) == 0 {
		return
	}
	if capacity < 1 {
		capacity = 1
	}

	for activeAdmissionTotal(state) < capacity && len(state.queue) > 0 {
		bestIdx := -1
		bestActive := 0
		for i, waiter := range state.queue {
			active := state.active[waiter.clientKey]
			if active >= capacity {
				continue
			}
			if bestIdx == -1 || active < bestActive {
				bestIdx = i
				bestActive = active
			}
		}
		if bestIdx == -1 {
			return
		}
		waiter := state.queue[bestIdx]
		state.queue = removeAdmissionWaiterAt(state.queue, bestIdx)
		state.active[waiter.clientKey]++
		waiter.granted = true
		close(waiter.grantCh)
	}
}

func activeAdmissionTotal(state *admissionState) int {
	total := 0
	for _, active := range state.active {
		total += active
	}
	return total
}

func removeAdmissionWaiter(queue []*admissionWaiter, target *admissionWaiter) []*admissionWaiter {
	for i, waiter := range queue {
		if waiter == target {
			return removeAdmissionWaiterAt(queue, i)
		}
	}
	return queue
}

func removeAdmissionWaiterAt(queue []*admissionWaiter, idx int) []*admissionWaiter {
	copy(queue[idx:], queue[idx+1:])
	queue[len(queue)-1] = nil
	return queue[:len(queue)-1]
}

func (m *Manager) trackQueuedAdmission(router *db.Router, proxyCtx *proxy.RequestContext, clientKey string) string {
	if m.proxy == nil {
		return ""
	}
	model := router.Name
	if proxyCtx != nil && proxyCtx.RequestedModel != "" {
		model = proxyCtx.RequestedModel
	}
	return m.proxy.TrackQueued("router:"+router.Name, model, clientKey)
}

func (m *Manager) untrackQueuedAdmission(trackID string) {
	if m.proxy == nil {
		return
	}
	m.proxy.RemoveTracked(trackID)
}

type selectedMember struct {
	index    int
	state    *memberState
	member   db.RouterMember
	modelMap *db.ModelMap
	backend  *db.Backend
}

func (m *Manager) selectRoutableMember(ctx context.Context, router *db.Router, states []*memberState, members []db.RouterMember, sessKey string, promptBytes int, decision *string) (*selectedMember, error) {
	excluded := make(map[int]bool)
	for len(excluded) < len(members) {
		idx, err := m.acquireMember(ctx, router, states, members, sessKey, excluded, promptBytes, decision)
		if err != nil {
			return nil, err
		}
		state := states[idx]
		member := members[idx]

		modelMap, err := m.db.ModelMaps.Get(member.ModelMapID)
		if err != nil {
			state.release()
			excluded[idx] = true
			m.logSkippedSelection(router, member, nil, nil, fmt.Sprintf("model map not found: %v", err))
			continue
		}
		backend, err := m.db.Backends.Get(modelMap.BackendID)
		if err != nil {
			state.release()
			excluded[idx] = true
			m.logSkippedSelection(router, member, modelMap, nil, fmt.Sprintf("backend not found: %v", err))
			continue
		}
		if reason := unroutableTargetReason(modelMap, backend); reason != "" {
			state.release()
			state.setHealth("offline")
			excluded[idx] = true
			m.logSkippedSelection(router, member, modelMap, backend, reason)
			continue
		}

		return &selectedMember{
			index:    idx,
			state:    state,
			member:   member,
			modelMap: modelMap,
			backend:  backend,
		}, nil
	}
	return nil, fmt.Errorf("router %q has no routable members", router.Name)
}

func unroutableTargetReason(modelMap *db.ModelMap, backend *db.Backend) string {
	if backend.Status != "active" {
		return "backend status is " + backend.Status
	}
	switch modelMap.Status {
	case "error", "offline", "inactive":
		return "model map status is " + modelMap.Status
	default:
		return ""
	}
}

func (m *Manager) annotateSelection(w http.ResponseWriter, router *db.Router, sel *selectedMember) {
	w.Header().Set("X-Relai-Router", router.Name)
	w.Header().Set("X-Relai-Router-Member", strconv.FormatInt(sel.member.ID, 10))
	w.Header().Set("X-Relai-Model-Map", strconv.FormatInt(sel.modelMap.ID, 10))
	w.Header().Set("X-Relai-Backend", sel.backend.Name)
}

func (m *Manager) logSelection(router *db.Router, sel *selectedMember, hasSessionKey bool) {
	if m.db == nil || m.db.SystemEvents == nil {
		return
	}
	m.db.SystemEvents.Log(db.LevelInfo, db.EventProxy, "virtual router selected member", map[string]any{
		"router_id":           router.ID,
		"router_name":         router.Name,
		"strategy":            normalizeStrategy(router.Strategy),
		"member_id":           sel.member.ID,
		"member_index":        sel.index,
		"model_map_id":        sel.modelMap.ID,
		"local_model":         sel.modelMap.LocalModel,
		"remote_model":        sel.modelMap.RemoteModel,
		"backend_id":          sel.backend.ID,
		"backend_name":        sel.backend.Name,
		"session_key_present": hasSessionKey,
	})
}

func (m *Manager) logSkippedSelection(router *db.Router, member db.RouterMember, modelMap *db.ModelMap, backend *db.Backend, reason string) {
	if m.db == nil || m.db.SystemEvents == nil {
		return
	}
	meta := map[string]any{
		"router_id":    router.ID,
		"router_name":  router.Name,
		"strategy":     normalizeStrategy(router.Strategy),
		"member_id":    member.ID,
		"model_map_id": member.ModelMapID,
		"reason":       reason,
	}
	if modelMap != nil {
		meta["local_model"] = modelMap.LocalModel
		meta["remote_model"] = modelMap.RemoteModel
		meta["model_map_status"] = modelMap.Status
	}
	if backend != nil {
		meta["backend_id"] = backend.ID
		meta["backend_name"] = backend.Name
		meta["backend_status"] = backend.Status
	}
	m.db.SystemEvents.Log(db.LevelWarn, db.EventProxy, "virtual router skipped unavailable member", meta)
}

func (m *Manager) acquireMember(ctx context.Context, router *db.Router, states []*memberState, members []db.RouterMember, sessKey string, excluded map[int]bool, promptBytes int, decision *string) (int, error) {
	setDecisionIfEmpty(decision, proxy.RoutingFresh)
	switch normalizeStrategy(router.Strategy) {
	case "round_robin":
		return m.acquireRoundRobin(ctx, router.ID, states, excluded)
	case "least_connections":
		return m.acquireLeastConnections(ctx, router.ID, states, sessKey, excluded)
	default: // "sticky", "cache_aware_sticky", and anything else
		// Phase 1 — wait for the sticky member (full timeout).
		// On Strix Halo, reprocessing the prompt on a cold machine costs more than waiting.
		if sessKey != "" {
			stickyID := m.getSession(sessKey)
			if normalizeStrategy(router.Strategy) == "cache_aware_sticky" {
				stickyID = m.getPersistentSession(router.ID, sessKey)
			}
			if stickyID != 0 {
				for i, mem := range members {
					if mem.ModelMapID != stickyID {
						continue
					}
					if excluded != nil && excluded[i] {
						break
					}
					if states[i].getHealth() == "offline" {
						break // sticky is down, fall through to any healthy member
					}
					// Free right now: take it, no decision to make.
					if states[i].slot.TryAcquire() {
						setDecision(decision, proxy.RoutingStickyHit)
						return i, nil
					}

					// Sticky is busy. How long it is worth waiting depends on two things:
					// what a bounce would cost, and whether a bounce would even help.
					//
					// #18 fast path: when an alternative host is genuinely free AND the
					// conversation's footprint says the bounce is cheap (small retained
					// bytes), spill immediately instead of queueing behind the warm
					// host for a budget we already know we will not need. Whales keep
					// the old wait-first behavior: their re-prefill is the expensive
					// thing being protected.
					if anyOtherMemberFree(states, i, excluded) && m.shouldSpillImmediately(router.ID, sessKey, promptBytes) {
						setDecision(decision, proxy.RoutingStickySpill)
						break
					}
					budget := stickyWaitBudget(promptBytes)
					if !anyOtherMemberFree(states, i, excluded) {
						// Every alternative is busy too. Bouncing would pay a full
						// reprocess AND still queue, so wait for the warm machine.
						budget = 0 // 0 == no cap, use the caller's deadline
					}

					waitCtx, cancel := ctx, context.CancelFunc(func() {})
					if budget > 0 {
						waitCtx, cancel = context.WithTimeout(ctx, budget)
					}
					err := states[i].acquire(waitCtx)
					cancel()
					if err == nil {
						setDecision(decision, proxy.RoutingStickyWait)
						return i, nil
					}
					// The caller's own deadline expired — surface that rather than
					// silently routing to a cold machine.
					if ctx.Err() != nil {
						return -1, fmt.Errorf("timed out waiting for sticky backend (prompt reprocessing avoided)")
					}
					// Only our shorter budget expired: a free machine is waiting and
					// reprocessing this prompt is cheaper than queueing any longer.
					setDecision(decision, proxy.RoutingStickySpill)
					break
				}
			}
		}

		// Phase 2 — smart first placement. Use global load, with round-robin
		// tie-breaking so independent sessions spread across members. The opt-in
		// cache-aware strategy prefers a host with observed KV-pool headroom, but
		// falls back to the same least-connections logic if telemetry is absent.
		if normalizeStrategy(router.Strategy) == "cache_aware_sticky" {
			return m.acquireCacheAwareLeastLoaded(ctx, router.ID, states, members, excluded)
		}
		return m.acquireLeastConnections(ctx, router.ID, states, sessKey, excluded)
	}
}

// spillImmediateBudget is the re-prefill cost below which a conversation is
// spilled to a free host at once instead of waiting for its warm host (#18).
// 15s: at ~200 tok/s conservative rate this covers conversations up to ~3k
// tokens — cheap to re-prefill, and the queue behind a busy warm host is
// almost always worse than that.
const spillImmediateBudget = 15 * time.Second

// shouldSpillImmediately reports whether a sticky-busy conversation should skip
// the wait budget because its estimated re-prefill on a free host is trivial.
// Uses the persisted footprint from the previous turn (#19) when available,
// falling back to this request's prompt size for a conversation's first busy
// turn. Missing data biases toward waiting — the conservative side.
func (m *Manager) shouldSpillImmediately(routerID int64, sessKey string, promptBytes int) bool {
	if promptBytes <= 0 {
		// Unknown prompt size: bias toward waiting, same as missing footprint.
		return false
	}
	bytes := int64(promptBytes)
	if m.db != nil && m.db.SessionFootprints != nil && sessKey != "" {
		if fb, err := m.db.SessionFootprints.Get(routerID, persistentSessionKey(sessKey)); err == nil && fb > 0 {
			bytes = fb
		}
	}
	cost := time.Duration(float64(bytes) / bytesPerToken / prefillTokensPerSecond * float64(time.Second))
	return cost <= spillImmediateBudget
}

// prefillTokensPerSecond is the observed prompt-processing rate for this class of
// host. Measured on Strix Halo (gfx1151, Qwen3.8-Flash-Next UD-IQ4_XS): roughly
// 200-340 tok/s depending on prompt length, so the conservative end is used - it
// biases the wait budget upward, which favours keeping a warm cache.
const prefillTokensPerSecond = 200.0

// bytesPerToken converts a raw request body size to an approximate token count.
// Deliberately crude: this only has to be right to within a factor of two to pick
// the right side of the wait-or-bounce decision.
const bytesPerToken = 4.0

// maxStickyWait caps the budget so a pathological prompt cannot pin a request to a
// busy host for longer than the caller would tolerate. The caller's own deadline
// still applies on top of this.
const maxStickyWait = 5 * time.Minute

// stickyWaitBudget returns how long it is worth waiting for the host that already
// holds this conversation's KV cache, given that the alternative is reprocessing the
// prompt somewhere cold. Waiting beats bouncing right up until the wait exceeds the
// reprocess cost, so the budget is that cost.
//
// The effect is that depth decides: a long conversation waits minutes rather than
// re-prefill, while a freshly spawned sub-agent - which has no cache worth keeping -
// gives up in seconds and lands on a free machine.
func stickyWaitBudget(promptBytes int) time.Duration {
	if promptBytes <= 0 {
		return time.Second
	}
	tokens := float64(promptBytes) / bytesPerToken
	d := time.Duration(tokens / prefillTokensPerSecond * float64(time.Second))
	if d < time.Second {
		d = time.Second
	}
	if d > maxStickyWait {
		d = maxStickyWait
	}
	return d
}

// anyOtherMemberFree reports whether some member other than skip could take this
// request right now. If nothing else is free, bouncing off a busy sticky host buys
// nothing: the request pays a full prompt reprocess and then queues anyway.
func anyOtherMemberFree(states []*memberState, skip int, excluded map[int]bool) bool {
	for i, s := range states {
		if i == skip {
			continue
		}
		if excluded != nil && excluded[i] {
			continue
		}
		if s.getHealth() == "offline" {
			continue
		}
		active, _, capacity := s.slot.Load()
		if active < capacity {
			return true
		}
	}
	return false
}

// setDecision records how the target was chosen, for the request log.
func setDecision(dst *string, v string) {
	if dst != nil {
		*dst = v
	}
}

func setDecisionIfEmpty(dst *string, v string) {
	if dst != nil && *dst == "" {
		*dst = v
	}
}

func normalizeStrategy(strategy string) string {
	normalized := strings.TrimSpace(strings.ToLower(strategy))
	switch normalized {
	case "round-robin":
		return "round_robin"
	case "cache-aware-sticky":
		return "cache_aware_sticky"
	case "least-busy":
		return "least_connections"
	case "least-connections":
		return "least_connections"
	case "adaptive-thinking":
		return "adaptive_thinking"
	case "":
		return "sticky"
	default:
		return normalized
	}
}

// acquireLeastConnections chooses the healthy member with the least global load.
// If all members are busy it queues at the router layer until one becomes free.
func (m *Manager) acquireLeastConnections(ctx context.Context, routerID int64, states []*memberState, sessKey string, excluded map[int]bool) (int, error) {
	if idx := m.tryAcquireLeastLoaded(routerID, states, sessKey, excluded); idx >= 0 {
		return idx, nil
	}

	// All eligible members are busy. Race the release notifications and let
	// cancellation/timeout remove this request from each member's waiter count.
	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	type win struct{ idx int }
	winner := make(chan win, 1)
	var wg sync.WaitGroup
	started := false

	for i, s := range states {
		if excluded != nil && excluded[i] {
			continue
		}
		if s.getHealth() == "offline" {
			continue
		}
		started = true
		wg.Add(1)
		go func(idx int, state *memberState) {
			defer wg.Done()
			if err := state.acquire(raceCtx); err != nil {
				return
			}
			select {
			case winner <- win{idx}:
				// won — caller owns the semaphore
			default:
				state.release() // lost the race, give it back
			}
		}(i, s)
	}
	if !started {
		return -1, fmt.Errorf("no healthy router members available")
	}

	select {
	case w := <-winner:
		return w.idx, nil
	case <-ctx.Done():
		cancel()
		wg.Wait()
		return -1, fmt.Errorf("timeout: all backends busy")
	}
}

func (m *Manager) tryAcquireLeastLoaded(routerID int64, states []*memberState, sessKey string, excluded map[int]bool) int {
	_ = sessKey
	bestActive := 0
	bestWaiters := 0
	candidates := make([]int, 0, len(states))

	for i, s := range states {
		if excluded != nil && excluded[i] {
			continue
		}
		if s.getHealth() == "offline" {
			continue
		}
		active, waiters, capacity := s.slot.Load()
		if active >= capacity {
			continue // host is at its concurrency limit
		}

		if len(candidates) == 0 ||
			active < bestActive ||
			(active == bestActive && waiters < bestWaiters) {
			candidates = candidates[:0]
			candidates = append(candidates, i)
			bestActive = active
			bestWaiters = waiters
			continue
		}
		if active == bestActive && waiters == bestWaiters {
			candidates = append(candidates, i)
		}
	}

	if len(candidates) == 0 {
		return -1
	}
	best := candidates[int((m.rrCursor(routerID).Add(1)-1)%uint64(len(candidates)))]

	s := states[best]
	if s.getHealth() == "offline" || !s.slot.TryAcquire() {
		return -1
	}
	return best
}

// acquireCacheAwareLeastLoaded performs a normal least-connections placement when
// /cache telemetry is missing. When snapshots are available, it preserves normal
// concurrency limits but breaks otherwise-equal ties toward a less-full KV pool.
func (m *Manager) acquireCacheAwareLeastLoaded(ctx context.Context, routerID int64, states []*memberState, members []db.RouterMember, excluded map[int]bool) (int, error) {
	if idx := m.tryAcquireCacheAware(routerID, states, members, excluded); idx >= 0 {
		return idx, nil
	}
	return m.acquireLeastConnections(ctx, routerID, states, "", excluded)
}

func (m *Manager) tryAcquireCacheAware(routerID int64, states []*memberState, members []db.RouterMember, excluded map[int]bool) int {
	if len(states) != len(members) {
		return -1
	}
	now := time.Now()
	bestScore := 0.0
	bestActive := 0
	bestWaiters := 0
	candidates := make([]int, 0, len(states))
	observed := false
	for i, state := range states {
		if excluded != nil && excluded[i] || state.getHealth() == "offline" {
			continue
		}
		active, waiters, capacity := state.slot.Load()
		if active >= capacity {
			continue
		}
		status, ok := m.cacheStatusFor(members[i].ModelMapID)
		if !ok || now.Sub(status.updatedAt) > 90*time.Second {
			continue
		}
		observed = true
		// Low occupancy leaves room for the first request; a higher historical
		// hit-rate is a small tiebreaker, never a claim that this new prompt is warm.
		score := status.poolUsageRatio - (status.tokenHitRate * 0.10)
		if len(candidates) == 0 || active < bestActive || (active == bestActive && waiters < bestWaiters) {
			bestActive = active
			bestWaiters = waiters
			bestScore = score
			candidates = candidates[:0]
			candidates = append(candidates, i)
		} else if active == bestActive && waiters == bestWaiters {
			if score < bestScore {
				bestScore = score
				candidates = candidates[:0]
				candidates = append(candidates, i)
			} else if score == bestScore {
				candidates = append(candidates, i)
			}
		}
	}
	if !observed || len(candidates) == 0 {
		return m.tryAcquireLeastLoaded(routerID, states, "", excluded)
	}
	best := candidates[int((m.rrCursor(routerID).Add(1)-1)%uint64(len(candidates)))]
	if states[best].getHealth() == "offline" || !states[best].slot.TryAcquire() {
		return -1
	}
	return best
}

func (m *Manager) setCacheStatus(modelMapID int64, status cacheStatus) {
	status.available = true
	if status.updatedAt.IsZero() {
		status.updatedAt = time.Now()
	}
	m.cacheStatusMu.Lock()
	m.cacheStatuses[modelMapID] = status
	m.cacheStatusMu.Unlock()
}

func (m *Manager) cacheStatusFor(modelMapID int64) (cacheStatus, bool) {
	m.cacheStatusMu.RLock()
	defer m.cacheStatusMu.RUnlock()
	status, ok := m.cacheStatuses[modelMapID]
	return status, ok && status.available
}

// acquireRoundRobin picks the next member in rotation order and waits for it.
// Unlike acquireAny, it advances the cursor even when the chosen member is idle,
// so sequential requests distribute across all healthy backends.
func (m *Manager) acquireRoundRobin(ctx context.Context, routerID int64, states []*memberState, excluded map[int]bool) (int, error) {
	n := len(states)
	cursor := m.rrCursor(routerID)

	// Try each member starting from the cursor position; skip offline ones.
	for attempt := 0; attempt < n; attempt++ {
		// Modulo in uint64 before the int conversion: a raw int(cursor.Add(1)-1)
		// would go negative once the counter passes math.MaxInt64 (#14, gosec G115).
		pos := int((cursor.Add(1) - 1) % uint64(n))
		if excluded != nil && excluded[pos] {
			continue
		}
		s := states[pos]
		if s.getHealth() == "offline" {
			continue
		}
		if err := s.acquire(ctx); err != nil {
			return -1, err
		}
		return pos, nil
	}

	// All members offline — fall back to acquireAny which will block until timeout.
	return m.acquireLeastConnections(ctx, routerID, states, "", excluded)
}

// ─── memberState ─────────────────────────────────────────────────────────────

// acquire blocks until the member's host has a free concurrency slot or ctx
// is cancelled. The slot is shared by every model on the same host.
func (s *memberState) acquire(ctx context.Context) error {
	for {
		if s.getHealth() == "offline" {
			return fmt.Errorf("member offline")
		}
		ok, ch := s.slot.TryAcquireOrWait()
		if ok {
			return nil
		}
		select {
		case <-ch: // slot released or health changed — retry
			s.slot.EndWait()
		case <-ctx.Done():
			s.slot.EndWait()
			return ctx.Err()
		}
	}
}

// release frees the host slot and wakes waiters.
func (s *memberState) release() {
	s.slot.Release()
}

func (s *memberState) getHealth() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.health
}

// setHealth updates health; going offline broadcasts on the host slot to wake
// waiters so they can observe the offline state and bail out.
func (s *memberState) setHealth(h string) {
	s.mu.Lock()
	s.health = h
	s.mu.Unlock()
	if h == "offline" && s.slot != nil {
		s.slot.Broadcast()
	}
}

// ─── State map ───────────────────────────────────────────────────────────────

func (m *Manager) getOrCreateStates(members []db.RouterMember) []*memberState {
	states := make([]*memberState, len(members))
	for i, mem := range members {
		states[i] = m.getOrCreateState(mem.ModelMapID)
	}
	return states
}

func (m *Manager) getOrCreateState(id int64) *memberState {
	// Resolve outside the lock; also refreshes the host's capacity from the DB.
	slot := m.slotForModelMap(id)
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.members[id]; ok {
		return s
	}
	s := &memberState{health: "active", slot: slot}
	m.members[id] = s
	return s
}

// slotForModelMap resolves a model mapping to its host's shared concurrency
// slot. When the mapping or backend cannot be resolved, a private single-slot
// semaphore is returned so routing still degrades gracefully.
func (m *Manager) slotForModelMap(modelMapID int64) *hostlimit.Slot {
	if m.db != nil {
		if mm, err := m.db.ModelMaps.Get(modelMapID); err == nil {
			if b, err := m.db.Backends.Get(mm.BackendID); err == nil {
				return m.limiter.Slot(b.ID, b.MaxConcurrent)
			}
		}
	}
	return hostlimit.NewSlot(1)
}

// ─── Session affinity ────────────────────────────────────────────────────────

func (m *Manager) getSession(key string) int64 {
	m.sessMu.RLock()
	defer m.sessMu.RUnlock()
	if e, ok := m.sessions[key]; ok {
		return e.modelMapID
	}
	return 0
}

func (m *Manager) setSession(key string, modelMapID int64) {
	m.sessMu.Lock()
	defer m.sessMu.Unlock()
	m.sessions[key] = sessionEntry{modelMapID: modelMapID, lastSeen: time.Now()}
}

// persistentSessionKey makes a database-safe affinity key. computeSessionKey may
// include a caller header, so never persist it directly.
func persistentSessionKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func (m *Manager) getPersistentSession(routerID int64, key string) int64 {
	if m.db == nil || m.db.RouterAffinities == nil || key == "" {
		return 0
	}
	modelMapID, err := m.db.RouterAffinities.Get(routerID, persistentSessionKey(key))
	if err != nil {
		return 0 // affinity is an optimization; database trouble must never block routing
	}
	return modelMapID
}

func (m *Manager) setPersistentSession(routerID int64, key string, modelMapID int64) {
	if m.db == nil || m.db.RouterAffinities == nil || key == "" || modelMapID == 0 {
		return
	}
	_ = m.db.RouterAffinities.Upsert(routerID, persistentSessionKey(key), modelMapID)
}

// computeSessionKey returns a stable key for the request's "session".
//
// The key identifies a CONVERSATION whose KV cache is worth reusing, not a caller.
// Identity alone is not enough: `user` names a person, and agent frameworks routinely
// propagate one X-Session-ID down to every sub-agent they spawn. Keying on identity
// by itself collapses unrelated conversations onto one host, so freshly spawned
// sub-agents - which share no prefix and have no cache to preserve - stay pinned to
// the parent's machine instead of spreading to a less busy one.
//
// So identity and conversation shape are combined. Within one conversation the shape
// hash is stable (system/developer messages plus the FIRST user message, which does
// not change as turns are appended), so stickiness is preserved across turns. Across
// different conversations it differs, so they are free to spread.
//
// Caveat: a client that trims the opening user message out of a long conversation
// changes its shape hash and loses affinity. That was already true of the shape-only
// path; it now also applies to callers sending an explicit session id.
func computeSessionKey(r *http.Request, body []byte) string {
	identity := ""
	if sid := r.Header.Get("X-Session-ID"); sid != "" {
		identity = "h:" + sid
	} else if sid := r.Header.Get("X-Relai-Session-ID"); sid != "" {
		identity = "rh:" + sid
	}

	var req struct {
		User     string `json:"user"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		// Unparseable body: an explicit header is all we have to go on.
		return identity
	}
	if identity == "" && req.User != "" {
		identity = "u:" + req.User
	}

	shape := conversationShape(req.Messages)
	switch {
	case shape == "" && identity == "":
		return ""
	case shape == "":
		return identity
	case identity == "":
		return "s:" + shape
	default:
		return identity + "|" + shape
	}
}

// conversationShape hashes the stable opening of a conversation: every system or
// developer message plus the first user message. It deliberately stops there, because
// the full prompt grows with every turn and would change the key each time.
func conversationShape(messages []struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}) string {
	h := sha256.New()
	found := false
	for _, msg := range messages {
		if msg.Role == "system" || msg.Role == "developer" {
			fmt.Fprintf(h, "%s:", msg.Role)
			h.Write(stableContentBytes(msg.Content))
			found = true
			continue
		}
		if msg.Role == "user" {
			fmt.Fprintf(h, "%s:", msg.Role)
			h.Write(stableContentBytes(msg.Content))
			found = true
			break
		}
	}
	if !found {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func stableContentBytes(content json.RawMessage) []byte {
	if len(content) == 0 {
		return nil
	}
	var text string
	if err := json.Unmarshal(content, &text); err == nil {
		return []byte(text)
	}
	var v any
	if err := json.Unmarshal(content, &v); err != nil {
		return content
	}
	compact, err := json.Marshal(v)
	if err != nil {
		return content
	}
	return compact
}

// ─── Response cache ───────────────────────────────────────────────────────────

func cacheKey(routerID int64, body []byte) string {
	h := sha256.New()
	fmt.Fprintf(h, "%d:", routerID)
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

func (m *Manager) cacheGet(key string) []byte {
	m.cacheMu.RLock()
	defer m.cacheMu.RUnlock()
	e, ok := m.cache[key]
	if !ok || time.Now().After(e.expiresAt) {
		return nil
	}
	return e.response
}

func (m *Manager) cacheSet(key string, response []byte, ttl time.Duration) {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	m.cache[key] = cacheEntry{response: response, expiresAt: time.Now().Add(ttl)}
}

// captureWriter tees the response into a buffer for caching while still writing to the client.
type captureWriter struct {
	http.ResponseWriter
	body   bytes.Buffer
	status int
}

func newCaptureWriter(w http.ResponseWriter) *captureWriter {
	return &captureWriter{ResponseWriter: w, status: http.StatusOK}
}

func (cw *captureWriter) Write(b []byte) (int, error) {
	cw.body.Write(b)
	return cw.ResponseWriter.Write(b)
}

func (cw *captureWriter) WriteHeader(status int) {
	cw.status = status
	cw.ResponseWriter.WriteHeader(status)
}

// ─── Health monitoring ────────────────────────────────────────────────────────

func (m *Manager) healthMonitorLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.checkAllMembers()
		case <-ctx.Done():
			return
		}
	}
}

func (m *Manager) checkAllMembers() {
	all, err := m.db.Routers.AllMembers()
	if err != nil {
		return
	}
	seen := make(map[int64]bool)
	for _, mem := range all {
		if seen[mem.ModelMapID] {
			continue
		}
		seen[mem.ModelMapID] = true
		modelMap, err := m.db.ModelMaps.Get(mem.ModelMapID)
		if err != nil {
			continue
		}
		go m.healthCheckMember(modelMap)
	}
}

func (m *Manager) healthCheckMember(modelMap *db.ModelMap) {
	state := m.getOrCreateState(modelMap.ID)

	baseURL := modelMap.BaseURL
	if baseURL == "" {
		backend, err := m.db.Backends.Get(modelMap.BackendID)
		if err != nil {
			return
		}
		baseURL = backend.BaseURL
	}

	start := time.Now()
	resp, err := m.healthClient.Get(strings.TrimRight(baseURL, "/") + "/v1/models")
	latency := int(time.Since(start).Milliseconds())

	if err != nil || (resp != nil && resp.StatusCode >= 500) {
		if resp != nil {
			resp.Body.Close()
		}
		state.setHealth("offline")
		_ = m.db.ModelMaps.UpdateHealth(modelMap.ID, "error", latency)
		return
	}
	resp.Body.Close()

	state.mu.Lock()
	state.health = "active"
	state.failures = 0
	state.mu.Unlock()
	_ = m.db.ModelMaps.UpdateHealth(modelMap.ID, "active", latency)
	go m.refreshCacheStatus(modelMap)
}

// refreshCacheStatus collects optional Halogen KV-pool telemetry. It deliberately
// does not alter backend health: llama.cpp and remote APIs may not expose /cache,
// and cache-aware routing must degrade to ordinary sticky routing in that case.
func (m *Manager) refreshCacheStatus(modelMap *db.ModelMap) {
	if m == nil || modelMap == nil || m.healthClient == nil {
		return
	}
	baseURL := modelMap.BaseURL
	if baseURL == "" && m.db != nil {
		backend, err := m.db.Backends.Get(modelMap.BackendID)
		if err != nil {
			return
		}
		baseURL = backend.BaseURL
	}
	if baseURL == "" {
		return
	}
	resp, err := m.healthClient.Get(strings.TrimRight(baseURL, "/") + "/cache")
	if err != nil || resp == nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return
	}
	var payload struct {
		TokenHitRate      float64 `json:"token_hit_rate"`
		HitRate           float64 `json:"hit_rate"`
		Entries           int     `json:"entries"`
		MaxEntries        int     `json:"max_entries"`
		Bytes             int64   `json:"bytes"`
		Evicted           int64   `json:"evicted"`
		Dropped           int64   `json:"dropped"`
		Superseded        int64   `json:"superseded"`
		PromptTokensSaved int64   `json:"prompt_tokens_saved"`
		Pool              struct {
			UsageRatio float64 `json:"usage_ratio"`
			Positions  int64   `json:"positions"`
			Used       int64   `json:"used"`
		} `json:"pool"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return
	}
	if payload.TokenHitRate < 0 || payload.TokenHitRate > 1 || payload.Pool.UsageRatio < 0 || payload.Pool.UsageRatio > 1 {
		return
	}
	status := cacheStatus{
		tokenHitRate:      payload.TokenHitRate,
		poolUsageRatio:    payload.Pool.UsageRatio,
		poolPositions:     payload.Pool.Positions,
		poolUsed:          payload.Pool.Used,
		entries:           payload.Entries,
		maxEntries:        payload.MaxEntries,
		entryBytes:        payload.Bytes,
		evicted:           payload.Evicted,
		dropped:           payload.Dropped,
		superseded:        payload.Superseded,
		promptTokensSaved: payload.PromptTokensSaved,
		hitRate:           payload.HitRate,
		updatedAt:         time.Now(),
	}
	// Engine-side queue depth rides on /health (Halogen: queued +
	// longest_wait_s). Best-effort: a backend without these fields simply
	// keeps EngineQueueSeen=false.
	if hresp, err := m.healthClient.Get(strings.TrimRight(baseURL, "/") + "/health"); err == nil && hresp != nil {
		defer hresp.Body.Close()
		if hresp.StatusCode >= 200 && hresp.StatusCode < 300 {
			var health struct {
				Queued       *int     `json:"queued"`
				LongestWaitS *float64 `json:"longest_wait_s"`
			}
			if err := json.NewDecoder(io.LimitReader(hresp.Body, 1<<20)).Decode(&health); err == nil {
				if health.Queued != nil || health.LongestWaitS != nil {
					status.engineQueueSeen = true
					if health.Queued != nil {
						status.engineQueued = *health.Queued
					}
					if health.LongestWaitS != nil {
						status.engineWaitSecs = *health.LongestWaitS
					}
				}
			}
		}
	}
	m.setCacheStatus(modelMap.ID, status)
}

// ─── Background cleanup ───────────────────────────────────────────────────────

func (m *Manager) sessionCleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			threshold := time.Now().Add(-1 * time.Hour)
			m.sessMu.Lock()
			for k, e := range m.sessions {
				if e.lastSeen.Before(threshold) {
					delete(m.sessions, k)
				}
			}
			m.sessMu.Unlock()
			if m.db != nil && m.db.RouterAffinities != nil {
				_ = m.db.RouterAffinities.DeleteOlderThan(threshold.UTC().Format(time.RFC3339))
			}
			if m.db != nil && m.db.SessionFootprints != nil {
				_ = m.db.SessionFootprints.DeleteOlderThan(threshold.UTC().Format(time.RFC3339))
			}
		case <-ctx.Done():
			return
		}
	}
}

func (m *Manager) cacheCleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			m.cacheMu.Lock()
			for k, e := range m.cache {
				if now.After(e.expiresAt) {
					delete(m.cache, k)
				}
			}
			m.cacheMu.Unlock()
		case <-ctx.Done():
			return
		}
	}
}

// prefixKey scopes prefix tracking to one caller, so unrelated conversations are
// never compared against each other. It deliberately excludes the conversation's
// content: the tracker finds the right predecessor by common prefix, and a key that
// moved when the prompt changed would defeat that.
func prefixKey(r *http.Request, ctx *proxy.RequestContext) string {
	if sid := r.Header.Get("X-Session-ID"); sid != "" {
		return "h:" + sid
	}
	if sid := r.Header.Get("X-Relai-Session-ID"); sid != "" {
		return "rh:" + sid
	}
	if ctx != nil && ctx.APIKeyID != nil {
		return fmt.Sprintf("k:%d", *ctx.APIKeyID)
	}
	return ""
}
