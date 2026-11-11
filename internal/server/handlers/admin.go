package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bfenty/relai/internal/db"
	"github.com/bfenty/relai/internal/queue"
	"github.com/bfenty/relai/internal/vrouter"
	chi "github.com/go-chi/chi/v5"
)

// AdminHandler handles admin API requests.
type AdminHandler struct {
	db      *db.DB
	vrouter *vrouter.Manager
	queue   *queue.Tracker
	speeds  *queue.SpeedLearner
}

// NewAdminHandler creates a new admin handler. Optional speed learner powers
// /api/queue/speeds.
func NewAdminHandler(d *db.DB, v *vrouter.Manager, q *queue.Tracker, speeds ...*queue.SpeedLearner) *AdminHandler {
	h := &AdminHandler{db: d, vrouter: v, queue: q}
	if len(speeds) > 0 {
		h.speeds = speeds[0]
	}
	return h
}

// HandleQueue returns a snapshot of in-flight requests (no content, metadata only).
func (h *AdminHandler) HandleQueue(w http.ResponseWriter, r *http.Request) {
	entries := h.queue.Snapshot()
	if entries == nil {
		entries = []queue.Entry{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"active": entries, "count": len(entries)})
}

// HandleQueueSpeeds returns the learned per-backend throughput (EWMA tok/s from
// completed requests). The queue view divides its estimated prompt size by this
// to draw a prefill progress bar while the backend is still silent.
func (h *AdminHandler) HandleQueueSpeeds(w http.ResponseWriter, r *http.Request) {
	out := []queue.BackendSpeed{}
	if h.speeds != nil {
		if s := h.speeds.Snapshot(); s != nil {
			out = s
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"speeds": out})
}

// HandleBackends handles /api/backends.
func (h *AdminHandler) HandleBackends(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listBackends(w, r)
	case http.MethodPost:
		h.createBackend(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// HandleBackend handles /api/backends/:id.
func (h *AdminHandler) HandleBackend(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(strings.TrimPrefix(r.URL.Path, "/api/backends/"))
	if err != nil {
		writeJSONError(w, "invalid ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getBackend(w, r, id)
	case http.MethodPut:
		h.updateBackend(w, r, id)
	case http.MethodDelete:
		h.deleteBackend(w, r, id)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// backendHealthTimeout bounds the host probe. Without it the check inherits Go's
// zero-value client and can hang indefinitely on an unresponsive host.
const backendHealthTimeout = 10 * time.Second

// backendProbeURLs returns every distinct base URL that can serve this backend, in
// the order the proxy would prefer them: any model mapping's own base_url first,
// then the backend's own. Mapping overrides come first because when they are set
// they are the address traffic actually goes to.
func (h *AdminHandler) backendProbeURLs(backend *db.Backend) []string {
	seen := map[string]bool{}
	urls := make([]string, 0, 2)
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u == "" || seen[u] {
			return
		}
		seen[u] = true
		urls = append(urls, u)
	}
	if maps, err := h.db.ModelMaps.List(); err == nil {
		for _, mm := range maps {
			if mm.BackendID == backend.ID {
				add(mm.BaseURL)
			}
		}
	}
	add(backend.BaseURL)
	return urls
}

// HandleBackendHealth handles /api/backends/:id/health.
func (h *AdminHandler) HandleBackendHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Strip the /health suffix before parsing the ID, as HandleModelMapping does.
	path := strings.TrimPrefix(r.URL.Path, "/api/backends/")
	path = strings.TrimSuffix(path, "/health")

	id, err := parseID(path)
	if err != nil {
		writeJSONError(w, "invalid ID", http.StatusBadRequest)
		return
	}

	backend, err := h.db.Backends.Get(id)
	if err != nil {
		writeJSONError(w, "backend not found", http.StatusNotFound)
		return
	}

	// Probe the URLs this host is actually served on. A model mapping may carry its
	// own base_url, which proxy.ProxyRequest and healthCheckModelMapping both prefer
	// over the backend's. Probing only backend.BaseURL therefore checks an address no
	// traffic uses: a host whose mappings each carry the real host:port, but whose
	// backend row holds a bare hostname, was probed on port 80, refused, marked
	// "error", and dropped out of Backends.ListActive() - so every model on it
	// returned 503 while its mappings all showed healthy.
	//
	// The host counts as up if any URL that serves it answers.
	client := &http.Client{Timeout: backendHealthTimeout}
	var (
		lastErr   error
		lastCode  int
		probedURL string
		latencyMs int
		ok        bool
	)
	for _, raw := range h.backendProbeURLs(backend) {
		started := time.Now()
		req, err := http.NewRequest(http.MethodGet, strings.TrimRight(raw, "/")+"/v1/models", nil)
		if err != nil {
			lastErr, probedURL = err, raw
			continue
		}
		// Paid OpenAI-compatible hosts require the same backend credential that
		// proxying uses. Existing local hosts generally have no key and therefore
		// retain the exact anonymous probe they used before this migration.
		if backend.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+backend.APIKey)
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr, probedURL = err, raw
			continue
		}
		code := resp.StatusCode
		resp.Body.Close()
		if code < 400 {
			latencyMs, probedURL, ok = int(time.Since(started).Milliseconds()), raw, true
			break
		}
		lastCode, probedURL = code, raw
	}

	if !ok {
		h.db.Backends.UpdateHealth(id, "error", 0)
		msg := "health check failed"
		if lastErr != nil {
			msg += " for " + probedURL + ": " + lastErr.Error()
		} else if lastCode != 0 {
			msg += " for " + probedURL + ": upstream returned " + strconv.Itoa(lastCode)
		}
		writeJSONError(w, msg, http.StatusBadGateway)
		return
	}
	h.db.Backends.UpdateHealth(id, "active", latencyMs)

	backend, _ = h.db.Backends.Get(id)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(backend)
}

// HandleAPIKeys handles /api/keys.
func (h *AdminHandler) HandleAPIKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listAPIKeys(w, r)
	case http.MethodPost:
		h.createAPIKey(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// HandleCreateTestKey handles POST /api/keys/test.
func (h *AdminHandler) HandleCreateTestKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Get owner from request context (set by auth middleware)
	owner := "admin"
	if u := r.Context().Value("user"); u != nil {
		if user, ok := u.(*db.User); ok && user != nil {
			owner = user.Name
		}
	}

	key, plainKey, err := h.db.APIKeys.CreateTestKey(owner)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Log the test key creation
	h.db.SystemEvents.Log(db.LevelWarn, "test_key_created",
		fmt.Sprintf("Test key created by %s (expires in 2h, prefix: %s)", owner, key.KeyPrefix),
		map[string]any{"key_id": key.ID, "prefix": key.KeyPrefix})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"key":   key,
		"value": plainKey,
	})
}

// HandleCleanupTestKeys handles POST /api/keys/cleanup.
func (h *AdminHandler) HandleCleanupTestKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	deleted, err := h.db.APIKeys.CleanupExpiredTestKeys()
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Log cleanup
	h.db.SystemEvents.Log(db.LevelInfo, "test_key_cleanup",
		fmt.Sprintf("Cleaned up %d expired test keys", deleted),
		map[string]any{"deleted": deleted})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"deleted": deleted,
	})
}

// HandleAPIKey handles /api/keys/:id.
func (h *AdminHandler) HandleAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(strings.TrimPrefix(r.URL.Path, "/api/keys/"))
	if err != nil {
		writeJSONError(w, "invalid ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getAPIKey(w, r, id)
	case http.MethodPut:
		h.updateAPIKey(w, r, id)
	case http.MethodDelete:
		h.deleteAPIKey(w, r, id)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// HandleRoutingRules handles /api/routing-rules.
func (h *AdminHandler) HandleRoutingRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listRoutingRules(w, r)
	case http.MethodPost:
		h.createRoutingRule(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// HandleRoutingRule handles /api/routing-rules/:id.
func (h *AdminHandler) HandleRoutingRule(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(strings.TrimPrefix(r.URL.Path, "/api/routing-rules/"))
	if err != nil {
		writeJSONError(w, "invalid ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getRoutingRule(w, r, id)
	case http.MethodPut:
		h.updateRoutingRule(w, r, id)
	case http.MethodDelete:
		h.deleteRoutingRule(w, r, id)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// HandleLogs handles /api/logs.
func (h *AdminHandler) HandleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()

	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	offset, _ := strconv.Atoi(q.Get("offset"))

	filter := db.LogFilter{
		Limit:  limit,
		Offset: offset,
	}

	if v := q.Get("model"); v != "" {
		filter.Model = v
	}
	if v := q.Get("backend_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			filter.BackendID = &id
		}
	}
	if v := q.Get("api_key_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			filter.APIKeyID = &id
		}
	}
	if q.Get("errors_only") == "true" {
		filter.ErrorsOnly = true
	}
	if v := q.Get("from"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.StartDate = &t
		}
	}
	if v := q.Get("to"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.EndDate = &t
		}
	}

	logs, total, err := h.db.Logs.List(filter)
	if err != nil {
		writeJSONError(w, "failed to list logs", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"data":  logs,
		"total": total,
	})
}

// HandleDashboardStats handles /api/dashboard/stats.
func (h *AdminHandler) HandleDashboardStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	rangeStr := r.URL.Query().Get("range")
	switch rangeStr {
	case "", "today", "week", "month":
	default:
		writeJSONError(w, "invalid range: must be today, week, or month", http.StatusBadRequest)
		return
	}

	stats, err := h.db.Logs.DashboardStats(rangeStr)
	if err != nil {
		writeJSONError(w, "failed to get stats", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// Backend handlers
func (h *AdminHandler) listBackends(w http.ResponseWriter, r *http.Request) {
	var (
		backends []db.Backend
		err      error
	)
	if r.URL.Query().Get("include_archived") == "true" {
		backends, err = h.db.Backends.ListIncludingArchived()
	} else {
		backends, err = h.db.Backends.List()
	}
	if err != nil {
		writeJSONError(w, "failed to list backends", http.StatusInternalServerError)
		return
	}
	for i := range backends {
		redactBackendKey(&backends[i])
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(backends)
}

// redactBackendKey hides the stored provider key in API responses; clients
// see only whether one is set. Updates with an empty api_key preserve the
// stored key, so redacted values can be round-tripped safely.
func redactBackendKey(b *db.Backend) {
	b.HasAPIKey = b.APIKey != ""
	b.APIKey = ""
}

func (h *AdminHandler) createBackend(w http.ResponseWriter, r *http.Request) {
	var input db.BackendInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSONError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	backend, err := h.db.Backends.Create(input)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	redactBackendKey(backend)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(backend)
}

func (h *AdminHandler) getBackend(w http.ResponseWriter, r *http.Request, id int64) {
	backend, err := h.db.Backends.Get(id)
	if err != nil {
		writeJSONError(w, "backend not found", http.StatusNotFound)
		return
	}
	redactBackendKey(backend)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(backend)
}

func (h *AdminHandler) updateBackend(w http.ResponseWriter, r *http.Request, id int64) {
	var input db.BackendInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSONError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	backend, err := h.db.Backends.Update(id, input)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	redactBackendKey(backend)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(backend)
}

// deleteBackend retires a host. By default it is archived: it leaves the hosts list
// and the routing pool, but its row survives so historical request_logs keep naming
// the host that served them. ?purge=true removes the row outright, which clears the
// host attribution on those logs - use it only when the history is genuinely unwanted.
func (h *AdminHandler) deleteBackend(w http.ResponseWriter, r *http.Request, id int64) {
	if r.URL.Query().Get("purge") == "true" {
		if err := h.db.Backends.Delete(id); err != nil {
			writeJSONError(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := h.db.Backends.Archive(id); err != nil {
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleBackendUnarchive handles POST /api/backends/:id/unarchive.
func (h *AdminHandler) HandleBackendUnarchive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/backends/")
	path = strings.TrimSuffix(path, "/unarchive")
	id, err := parseID(path)
	if err != nil {
		writeJSONError(w, "invalid ID", http.StatusBadRequest)
		return
	}
	if err := h.db.Backends.Unarchive(id); err != nil {
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	backend, err := h.db.Backends.Get(id)
	if err != nil {
		writeJSONError(w, "backend not found", http.StatusNotFound)
		return
	}
	redactBackendKey(backend)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(backend)
}

// API Key handlers
func (h *AdminHandler) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.db.APIKeys.List()
	if err != nil {
		writeJSONError(w, "failed to list keys", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(keys)
}

func (h *AdminHandler) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var input db.APIKeyInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSONError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	key, plainKey, err := h.db.APIKeys.Create(input)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"key":   key,
		"value": plainKey,
	})
}

func (h *AdminHandler) getAPIKey(w http.ResponseWriter, r *http.Request, id int64) {
	key, err := h.db.APIKeys.Get(id)
	if err != nil {
		writeJSONError(w, "key not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(key)
}

func (h *AdminHandler) updateAPIKey(w http.ResponseWriter, r *http.Request, id int64) {
	var input db.APIKeyInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSONError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	key, err := h.db.APIKeys.Update(id, input)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(key)
}

func (h *AdminHandler) deleteAPIKey(w http.ResponseWriter, r *http.Request, id int64) {
	if err := h.db.APIKeys.Delete(id); err != nil {
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Routing rule handlers
func (h *AdminHandler) listRoutingRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.db.Routing.List()
	if err != nil {
		writeJSONError(w, "failed to list rules", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rules)
}

func (h *AdminHandler) createRoutingRule(w http.ResponseWriter, r *http.Request) {
	var input db.RoutingRuleInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSONError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	rule, err := h.db.Routing.Create(input)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(rule)
}

func (h *AdminHandler) getRoutingRule(w http.ResponseWriter, r *http.Request, id int64) {
	rule, err := h.db.Routing.Get(id)
	if err != nil {
		writeJSONError(w, "rule not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rule)
}

func (h *AdminHandler) updateRoutingRule(w http.ResponseWriter, r *http.Request, id int64) {
	var input db.RoutingRuleInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSONError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	rule, err := h.db.Routing.Update(id, input)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rule)
}

func (h *AdminHandler) deleteRoutingRule(w http.ResponseWriter, r *http.Request, id int64) {
	if err := h.db.Routing.Delete(id); err != nil {
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Model mapping handlers
func (h *AdminHandler) HandleModelMappings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listModelMappings(w, r)
	case http.MethodPost:
		h.createModelMapping(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (h *AdminHandler) HandleModelMapping(w http.ResponseWriter, r *http.Request) {
	// Strip /health suffix before parsing the ID
	path := strings.TrimPrefix(r.URL.Path, "/api/model-mappings/")
	path = strings.TrimSuffix(path, "/health")

	id, err := parseID(path)
	if err != nil {
		writeJSONError(w, "invalid ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.getModelMapping(w, r, id)
	case http.MethodPut:
		h.updateModelMapping(w, r, id)
	case http.MethodPost:
		h.healthCheckModelMapping(w, r, id)
	case http.MethodDelete:
		h.deleteModelMapping(w, r, id)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (h *AdminHandler) listModelMappings(w http.ResponseWriter, r *http.Request) {
	maps, err := h.db.ModelMaps.List()
	if err != nil {
		writeJSONError(w, "failed to list model mappings", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(maps)
}

func (h *AdminHandler) createModelMapping(w http.ResponseWriter, r *http.Request) {
	var input db.ModelMapInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSONError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	mapping, err := h.db.ModelMaps.Create(input)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(mapping)
}

func (h *AdminHandler) getModelMapping(w http.ResponseWriter, r *http.Request, id int64) {
	mapping, err := h.db.ModelMaps.Get(id)
	if err != nil {
		writeJSONError(w, "model mapping not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mapping)
}

func (h *AdminHandler) updateModelMapping(w http.ResponseWriter, r *http.Request, id int64) {
	var input db.ModelMapInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSONError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	mapping, err := h.db.ModelMaps.Update(id, input)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(mapping)
}

func (h *AdminHandler) healthCheckModelMapping(w http.ResponseWriter, r *http.Request, id int64) {
	mapping, err := h.db.ModelMaps.Get(id)
	if err != nil {
		writeJSONError(w, "model mapping not found", http.StatusNotFound)
		return
	}

	// Resolve the effective base URL
	baseURL := mapping.BaseURL
	if baseURL == "" {
		backend, err := h.db.Backends.Get(mapping.BackendID)
		if err != nil {
			writeJSONError(w, "backend not found", http.StatusNotFound)
			return
		}
		baseURL = backend.BaseURL
	}

	start := time.Now()
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(strings.TrimRight(baseURL, "/") + "/v1/models")
	latencyMs := int(time.Since(start).Milliseconds())

	if err != nil {
		h.db.ModelMaps.UpdateHealth(id, "error", latencyMs)
		writeJSONError(w, "health check failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	resp.Body.Close()
	status := "active"
	if resp.StatusCode >= 400 {
		status = "error"
	}
	h.db.ModelMaps.UpdateHealth(id, status, latencyMs)

	updated, _ := h.db.ModelMaps.Get(id)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updated)
}

func (h *AdminHandler) deleteModelMapping(w http.ResponseWriter, r *http.Request, id int64) {
	if err := h.db.ModelMaps.Delete(id); err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseID(s string) (int64, error) {
	s = strings.TrimSuffix(s, "/")
	return strconv.ParseInt(s, 10, 64)
}

// ── Router handlers ───────────────────────────────────────────────────────────

func (h *AdminHandler) HandleRouters(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		routers, err := h.db.Routers.List()
		if err != nil {
			writeJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(routers)
	case http.MethodPost:
		var input db.RouterInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeJSONError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		rt, err := h.db.Routers.Create(input)
		if err != nil {
			writeJSONError(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(rt)
	default:
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *AdminHandler) HandleRouter(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, "invalid ID", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		rt, err := h.db.Routers.Get(id)
		if err != nil {
			writeJSONError(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rt)
	case http.MethodPut:
		var input db.RouterInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeJSONError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		rt, err := h.db.Routers.Update(id, input)
		if err != nil {
			writeJSONError(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(rt)
	case http.MethodDelete:
		if err := h.db.Routers.Delete(id); err != nil {
			writeJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *AdminHandler) HandleRouterMembers(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, "invalid router ID", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		members, err := h.db.Routers.GetMembers(id)
		if err != nil {
			writeJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(members)
	case http.MethodPost:
		var input struct {
			ModelMapID int64 `json:"model_map_id"`
			Priority   int   `json:"priority"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeJSONError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		mem, err := h.db.Routers.AddMember(id, input.ModelMapID, input.Priority)
		if err != nil {
			writeJSONError(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(mem)
	default:
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *AdminHandler) HandleRouterMember(w http.ResponseWriter, r *http.Request) {
	memberID, err := parseID(chi.URLParam(r, "memberID"))
	if err != nil {
		writeJSONError(w, "invalid member ID", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodDelete {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := h.db.Routers.RemoveMember(memberID); err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) HandleRouterStatus(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, "invalid ID", http.StatusBadRequest)
		return
	}
	status, err := h.vrouter.GetStatus(id)
	if err != nil {
		writeJSONError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// ── Users ─────────────────────────────────────────────────────────────────────

func (h *AdminHandler) HandleUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		users, err := h.db.Users.List()
		if err != nil {
			writeJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(users)

	case http.MethodPost:
		var input db.UserInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeJSONError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		user, err := h.db.Users.Create(input)
		if err != nil {
			writeJSONError(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(user)

	default:
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *AdminHandler) HandleUser(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, "invalid user ID", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		user, err := h.db.Users.Get(id)
		if err != nil {
			writeJSONError(w, "user not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(user)

	case http.MethodPut:
		var input db.UserInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeJSONError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		user, err := h.db.Users.Update(id, input)
		if err != nil {
			writeJSONError(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(user)

	case http.MethodDelete:
		if err := h.db.Users.Delete(id); err != nil {
			writeJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *AdminHandler) HandleUserKeys(w http.ResponseWriter, r *http.Request) {
	userID, err := parseID(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, "invalid user ID", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		keys, err := h.db.APIKeys.ListByUser(userID)
		if err != nil {
			writeJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(keys)

	case http.MethodPost:
		var input db.APIKeyInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeJSONError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		input.UserID = &userID
		key, plainKey, err := h.db.APIKeys.Create(input)
		if err != nil {
			writeJSONError(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"key": key, "value": plainKey})

	default:
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
