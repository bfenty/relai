package db

import (
	"fmt"
	"time"
)

// RoutingStats answers the question the request log could not previously answer:
// is session affinity actually working, and what is it costing when it is not?
//
// The headline numbers are the sticky hit rate (how often a conversation was routed
// back to the host holding its context) and the cache reuse rate (how much of each
// prompt the upstream served from its KV cache rather than reprocessing). A silent
// affinity failure shows up as a fall in both, and as a rise in ReprocessedTokens.
// DecisionStat is the per-decision rollup used to evaluate spill economics.
type DecisionStat struct {
	Requests          int64 `json:"requests"`
	PromptTokens      int64 `json:"prompt_tokens"`
	CachedTokens      int64 `json:"cached_tokens"`
	ReprocessedTokens int64 `json:"reprocessed_tokens"` // prompt - cached: what the host had to prefill
	MeanWaitMs        int64 `json:"mean_wait_ms"`
	MeanLatencyMs     int64 `json:"mean_latency_ms"`
}

type RoutingStats struct {
	From      time.Time        `json:"from"`
	To        time.Time        `json:"to"`
	Total     int64            `json:"total_requests"`
	Decisions map[string]int64 `json:"decisions"`

	// DecisionStats breaks the same aggregates down per routing decision so the
	// spill policy (#18) can be judged on evidence: spill rows that show low wait
	// AND modest reprocessed tokens while sticky_wait waits run long mean the
	// spills are saving time; the reverse means they are paying cache misses for
	// nothing.
	DecisionStats map[string]DecisionStat `json:"decision_stats"`

	StickyHitRate  float64 `json:"sticky_hit_rate"`  // sticky_hit+sticky_wait over all routed requests
	SpillRate      float64 `json:"spill_rate"`       // gave up on the warm host
	CacheReuseRate float64 `json:"cache_reuse_rate"` // cached / prompt, measured rows only

	// Rows written before routing was recorded carry no cache data, so counting their
	// prompts would drag every rate toward zero. They are reported but excluded from
	// the rates above and from the per-host cache reuse.
	MeasuredRequests   int64 `json:"measured_requests"`
	UnmeasuredRequests int64 `json:"unmeasured_requests"`

	PromptTokens      int64 `json:"prompt_tokens"`
	CachedTokens      int64 `json:"cached_tokens"`
	ReprocessedTokens int64 `json:"reprocessed_tokens"` // prompt tokens the upstream had to prefill

	MeanWaitMs int64 `json:"mean_wait_ms"`
	P95WaitMs  int64 `json:"p95_wait_ms"`

	// Compression, where a target has it enabled. BytesSaved is content that never
	// had to be prefilled; CompressedRequests is how many requests it actually
	// touched, which keeps the saving honest when only some targets compress.
	CompressedRequests int64 `json:"compressed_requests"`
	BytesSaved         int64 `json:"compression_bytes_saved"`

	Hosts []HostRoutingStats `json:"hosts"`
}

// HostRoutingStats breaks the same picture down per host, which is how an unbalanced
// cluster shows itself: one host carrying the sessions while another idles.
type HostRoutingStats struct {
	BackendID      *int64  `json:"backend_id"`
	BackendName    string  `json:"backend_name"`
	Requests       int64   `json:"requests"`
	Share          float64 `json:"share"`
	CacheReuseRate float64 `json:"cache_reuse_rate"`
	MeanLatencyMs  int64   `json:"mean_latency_ms"`
	BytesSaved     int64   `json:"compression_bytes_saved"`
}

// RoutingAnalytics aggregates routing behaviour over a window.
func (d *DB) RoutingAnalytics(from, to time.Time) (*RoutingStats, error) {
	if to.IsZero() {
		to = time.Now()
	}
	if from.IsZero() {
		from = to.Add(-24 * time.Hour)
	}
	out := &RoutingStats{From: from, To: to, Decisions: map[string]int64{}, DecisionStats: map[string]DecisionStat{}}

	rows, err := d.Logs.db.Query(`
		SELECT COALESCE(routing_decision,''), COUNT(*),
		       COALESCE(SUM(request_tokens),0), COALESCE(SUM(cached_tokens),0),
		       COALESCE(SUM(wait_ms),0),
		       COALESCE(SUM(compressed_bytes),0),
		       COALESCE(SUM(CASE WHEN compressed_bytes > 0 THEN 1 ELSE 0 END),0),
		       COALESCE(SUM(latency_ms),0)
		FROM request_logs
		WHERE created_at >= `+d.Logs.placeholder(1)+` AND created_at <= `+d.Logs.placeholder(2)+`
		GROUP BY routing_decision`, d.Logs.timeParam(from), d.Logs.timeParam(to))
	if err != nil {
		return nil, fmt.Errorf("routing decisions: %w", err)
	}
	defer rows.Close()

	var routed, sticky, spilled int64
	for rows.Next() {
		var decision string
		var n, prompt, cached, wait, saved, compressed, latency int64
		if err := rows.Scan(&decision, &n, &prompt, &cached, &wait, &saved, &compressed, &latency); err != nil {
			return nil, err
		}
		if decision == "" {
			decision = "unrecorded"
		}
		out.Decisions[decision] = n
		out.Total += n
		if decision == "unrecorded" {
			out.UnmeasuredRequests += n
			continue // no cache data on these rows; counting them would skew every rate
		}
		st := DecisionStat{
			Requests:      n,
			PromptTokens:  prompt,
			CachedTokens:  cached,
			MeanWaitMs:    wait / n,
			MeanLatencyMs: latency / n,
		}
		if cached > prompt {
			cached = prompt
		}
		st.ReprocessedTokens = prompt - cached
		out.DecisionStats[decision] = st
		out.MeasuredRequests += n
		out.BytesSaved += saved
		out.CompressedRequests += compressed
		out.PromptTokens += prompt
		out.CachedTokens += cached
		switch decision {
		case "sticky_hit", "sticky_wait":
			sticky += n
			routed += n
		case "sticky_spill":
			spilled += n
			routed += n
		case "fresh":
			routed += n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if routed > 0 {
		out.StickyHitRate = float64(sticky) / float64(routed)
		out.SpillRate = float64(spilled) / float64(routed)
	}
	if out.PromptTokens > 0 {
		if out.CachedTokens > out.PromptTokens {
			out.CachedTokens = out.PromptTokens // defensive: upstreams disagree on accounting
		}
		out.ReprocessedTokens = out.PromptTokens - out.CachedTokens
		out.CacheReuseRate = float64(out.CachedTokens) / float64(out.PromptTokens)
	}

	if err := d.routingWaitPercentiles(from, to, out); err != nil {
		return nil, err
	}
	if err := d.routingHostBreakdown(from, to, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (d *DB) routingWaitPercentiles(from, to time.Time, out *RoutingStats) error {
	rows, err := d.Logs.db.Query(`
		SELECT COALESCE(wait_ms,0) FROM request_logs
		WHERE created_at >= `+d.Logs.placeholder(1)+` AND created_at <= `+d.Logs.placeholder(2)+`
		ORDER BY wait_ms`, d.Logs.timeParam(from), d.Logs.timeParam(to))
	if err != nil {
		return fmt.Errorf("wait percentiles: %w", err)
	}
	defer rows.Close()
	waits := make([]int64, 0, 256)
	var sum int64
	for rows.Next() {
		var w int64
		if err := rows.Scan(&w); err != nil {
			return err
		}
		waits = append(waits, w)
		sum += w
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(waits) == 0 {
		return nil
	}
	out.MeanWaitMs = sum / int64(len(waits))
	out.P95WaitMs = waits[int(float64(len(waits))*0.95)%len(waits)]
	return nil
}

func (d *DB) routingHostBreakdown(from, to time.Time, out *RoutingStats) error {
	// request_logs may live in a separate store (Postgres) from backends, so the host
	// names are resolved here rather than joined in SQL.
	names, err := d.idNameMap(`SELECT id, name FROM backends`)
	if err != nil {
		return fmt.Errorf("backend names: %w", err)
	}
	rows, err := d.Logs.db.Query(`
		SELECT backend_id, COUNT(*),
		       COALESCE(SUM(CASE WHEN COALESCE(routing_decision,'') <> '' THEN request_tokens ELSE 0 END),0),
		       COALESCE(SUM(CASE WHEN COALESCE(routing_decision,'') <> '' THEN cached_tokens ELSE 0 END),0),
		       COALESCE(AVG(latency_ms),0),
		       COALESCE(SUM(CASE WHEN COALESCE(routing_decision,'') <> '' THEN compressed_bytes ELSE 0 END),0)
		FROM request_logs
		WHERE created_at >= `+d.Logs.placeholder(1)+` AND created_at <= `+d.Logs.placeholder(2)+`
		GROUP BY backend_id
		ORDER BY COUNT(*) DESC`, d.Logs.timeParam(from), d.Logs.timeParam(to))
	if err != nil {
		return fmt.Errorf("host breakdown: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var h HostRoutingStats
		var prompt, cached int64
		var meanLatency float64
		if err := rows.Scan(&h.BackendID, &h.Requests, &prompt, &cached, &meanLatency, &h.BytesSaved); err != nil {
			return err
		}
		h.BackendName = "(removed)"
		if h.BackendID != nil {
			if n, ok := names[*h.BackendID]; ok {
				h.BackendName = n
			}
		}
		h.MeanLatencyMs = int64(meanLatency)
		if prompt > 0 {
			if cached > prompt {
				cached = prompt
			}
			h.CacheReuseRate = float64(cached) / float64(prompt)
		}
		if out.Total > 0 {
			h.Share = float64(h.Requests) / float64(out.Total)
		}
		out.Hosts = append(out.Hosts, h)
	}
	return rows.Err()
}
