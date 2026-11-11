package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/bfenty/relai/internal/db"
	"github.com/bfenty/relai/internal/vrouter"
)

// HandleAnalyticsUsage returns time-bucketed usage aggregated by a dimension.
//
// Query params:
//
//	from      RFC3339 start (default: 7 days ago)
//	to        RFC3339 end   (default: now)
//	group_by  model | api_key | user | backend (default: model)
//	interval  hour | day | week (default: auto from range)
func (h *AdminHandler) HandleAnalyticsUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	now := time.Now()
	opts := db.AnalyticsOptions{
		Start:    now.AddDate(0, 0, -7),
		End:      now,
		GroupBy:  q.Get("group_by"),
		Interval: q.Get("interval"),
	}

	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeJSONError(w, "invalid 'from': expected RFC3339", http.StatusBadRequest)
			return
		}
		opts.Start = t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeJSONError(w, "invalid 'to': expected RFC3339", http.StatusBadRequest)
			return
		}
		opts.End = t
	}

	switch opts.GroupBy {
	case "", "model", "api_key", "user", "backend":
	default:
		writeJSONError(w, "invalid group_by: must be model, api_key, user, or backend", http.StatusBadRequest)
		return
	}
	switch opts.Interval {
	case "", "hour", "day", "week":
	default:
		writeJSONError(w, "invalid interval: must be hour, day, or week", http.StatusBadRequest)
		return
	}

	res, err := h.db.UsageAnalytics(opts)
	if err != nil {
		writeJSONError(w, "failed to compute analytics", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// HandleRoutingAnalytics serves GET /api/analytics/routing?hours=N.
//
// It answers whether session affinity is working: the sticky hit rate, how much of
// each prompt the upstreams served from cache rather than reprocessing, how long
// requests waited for their host, and how the load actually split across hosts.
func (h *AdminHandler) HandleRoutingAnalytics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	hours := 24
	if v := r.URL.Query().Get("hours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 24*90 {
			hours = n
		}
	}
	to := time.Now()
	from := to.Add(-time.Duration(hours) * time.Hour)

	stats, err := h.db.RoutingAnalytics(from, to)
	if err != nil {
		writeJSONError(w, "failed to compute routing analytics", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// HandleRoutingSeries serves GET /api/analytics/routing/series?hours=N&interval=X.
//
// The same signals as the routing summary, bucketed over time: cache reuse,
// reprocessed tokens, cold arrivals and compression savings. Cold arrivals are the
// costly ones - a deep prompt that turns up without its cache pays a full re-prefill
// - so the series is what shows whether a configuration change actually helped.
func (h *AdminHandler) HandleRoutingSeries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	hours := 24
	if v := r.URL.Query().Get("hours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 24*90 {
			hours = n
		}
	}
	to := time.Now()
	from := to.Add(-time.Duration(hours) * time.Hour)

	series, err := h.db.RoutingTimeSeries(from, to, r.URL.Query().Get("interval"))
	if err != nil {
		writeJSONError(w, "failed to compute routing series", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(series)
}

// HandleThroughputSeries serves GET /api/analytics/throughput/series?hours=N&interval=X.
//
// Measured prefill and decode tok/s over time, token-weighted per bucket. The
// two rates move independently — prefill scales with prompt size and cache
// reuse, decode with batch pressure — so they are charted separately. Each
// bucket also carries a by_backend split: per-host aggregates whose decode
// rate rises with concurrency (machine throughput) instead of sagging like the
// per-request rate.
func (h *AdminHandler) HandleThroughputSeries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	hours := 24
	if v := r.URL.Query().Get("hours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 24*90 {
			hours = n
		}
	}
	to := time.Now()
	from := to.Add(-time.Duration(hours) * time.Hour)

	series, err := h.db.ThroughputTimeSeries(from, to, r.URL.Query().Get("interval"))
	if err != nil {
		writeJSONError(w, "failed to compute throughput series", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(series)
}

// HandlePrefixAnalytics serves GET /api/analytics/prefix?hours=N.
//
// Cache reuse says how often prompts missed the backend's KV cache; this says why.
// Causes are ranked by the tokens they forced the backend to reprocess rather than by
// how often they occur, because one rewrite at deep context outweighs a hundred
// shallow ones, and the offender list names the client and model responsible.
func (h *AdminHandler) HandlePrefixAnalytics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	from, to := prefixWindow(r)
	stats, err := h.db.PrefixAnalytics(from, to)
	if err != nil {
		writeJSONError(w, "failed to compute prefix analytics", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// HandlePrefixSeries serves GET /api/analytics/prefix/series?hours=N&interval=X.
//
// The cause mix over time, so a regression can be tied to when it started - a client
// that began stamping a timestamp into its system prompt shows up here as a step
// change rather than as a vague rise in cold arrivals.
func (h *AdminHandler) HandlePrefixSeries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	from, to := prefixWindow(r)
	series, err := h.db.PrefixTimeSeries(from, to, r.URL.Query().Get("interval"))
	if err != nil {
		writeJSONError(w, "failed to compute prefix series", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(series)
}

func prefixWindow(r *http.Request) (time.Time, time.Time) {
	hours := 24
	if v := r.URL.Query().Get("hours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 24*90 {
			hours = n
		}
	}
	to := time.Now()
	return to.Add(-time.Duration(hours) * time.Hour), to
}

// HandleCacheTelemetry serves GET /api/analytics/cache: the latest KV-cache
// telemetry polled from each model map's backend /cache endpoint, joined with
// host/model names so the UI can render without extra lookups. Backends that do
// not expose /cache (llama.cpp, remote APIs) simply never appear as available.
func (h *AdminHandler) HandleCacheTelemetry(w http.ResponseWriter, r *http.Request) {
	type row struct {
		vrouter.CacheTelemetry
		BackendName string `json:"backend_name"`
		ModelName   string `json:"model_name"`
	}
	maps, err := h.db.ModelMaps.List()
	if err != nil {
		writeJSONError(w, "failed to list model maps", http.StatusInternalServerError)
		return
	}
	backends, err := h.db.Backends.List()
	if err != nil {
		writeJSONError(w, "failed to list backends", http.StatusInternalServerError)
		return
	}
	names := make(map[int64]string, len(backends))
	for _, b := range backends {
		names[b.ID] = b.Name
	}
	tele := make(map[int64]vrouter.CacheTelemetry)
	if h.vrouter != nil {
		for _, t := range h.vrouter.CacheTelemetrySnapshot() {
			tele[t.ModelMapID] = t
		}
	}
	rows := make([]row, 0, len(maps))
	for _, mm := range maps {
		t, ok := tele[mm.ID]
		if !ok {
			t = vrouter.CacheTelemetry{ModelMapID: mm.ID}
		}
		rows = append(rows, row{
			CacheTelemetry: t,
			BackendName:    names[mm.BackendID],
			ModelName:      mm.LocalModel,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rows)
}
