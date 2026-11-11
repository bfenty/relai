package db

import (
	"fmt"
	"time"
)

// RoutingPoint is one time bucket of routing health.
//
// The two numbers that matter for cost are ReprocessedTokens and ColdRequests: a
// request that arrives without its KV cache pays a full prompt re-prefill, which at
// deep context is tens of seconds. Everything else here explains why that happened.
type RoutingPoint struct {
	Time     time.Time `json:"time"`
	Requests int64     `json:"requests"`

	// Measured requests only - rows predating routing instrumentation carry no
	// cache data and would drag every rate toward zero.
	Measured int64 `json:"measured_requests"`

	PromptTokens      int64   `json:"prompt_tokens"`
	CachedTokens      int64   `json:"cached_tokens"`
	ReprocessedTokens int64   `json:"reprocessed_tokens"`
	CacheReuseRate    float64 `json:"cache_reuse_rate"`

	// ColdRequests are deep requests that arrived with almost no cache - the ones
	// that cost real time. Shallow requests are excluded because a cold 200-token
	// prompt is free and would swamp the signal.
	ColdRequests int64   `json:"cold_requests"`
	DeepRequests int64   `json:"deep_requests"`
	ColdRate     float64 `json:"cold_rate"`

	CompressedRequests int64 `json:"compressed_requests"`
	BytesSaved         int64 `json:"compression_bytes_saved"`

	MeanLatencyMs int64 `json:"mean_latency_ms"`
	StickyHits    int64 `json:"sticky_hits"`
	Spills        int64 `json:"spills"`
}

// RoutingSeries is a time-bucketed view of routing health.
type RoutingSeries struct {
	From     time.Time      `json:"from"`
	To       time.Time      `json:"to"`
	Interval string         `json:"interval"`
	Points   []RoutingPoint `json:"points"`
}

// coldReuseThreshold is the cache-reuse fraction below which a request counts as
// having arrived cold. Well below the ~0.95 a warm conversation reports, so normal
// per-turn growth is never mistaken for a cache miss.
const coldReuseThreshold = 0.5

// deepTokenThreshold is the prompt size above which a cold arrival is expensive
// enough to care about. Below this a full prefill costs under a second.
const deepTokenThreshold = 2000

// RoutingTimeSeries buckets routing health over a window. interval accepts
// "minute", "hour" or "day"; anything else is chosen from the span.
func (d *DB) RoutingTimeSeries(from, to time.Time, interval string) (*RoutingSeries, error) {
	if to.IsZero() {
		to = time.Now()
	}
	if from.IsZero() {
		from = to.Add(-24 * time.Hour)
	}
	interval = normalizeRoutingInterval(interval, to.Sub(from))

	rows, err := d.Logs.db.Query(`
		SELECT created_at, COALESCE(routing_decision,''), COALESCE(request_tokens,0),
		       COALESCE(cached_tokens,0), COALESCE(compressed_bytes,0), COALESCE(latency_ms,0)
		FROM request_logs
		WHERE created_at >= `+d.Logs.placeholder(1)+` AND created_at <= `+d.Logs.placeholder(2)+`
		ORDER BY created_at ASC`, d.Logs.timeParam(from), d.Logs.timeParam(to))
	if err != nil {
		return nil, fmt.Errorf("routing series: %w", err)
	}
	defer rows.Close()

	buckets := map[time.Time]*RoutingPoint{}
	latencySum := map[time.Time]int64{}
	latencyN := map[time.Time]int64{}

	for rows.Next() {
		var (
			created                        time.Time
			decision                       string
			prompt, cached, saved, latency int64
		)
		if err := rows.Scan(&created, &decision, &prompt, &cached, &saved, &latency); err != nil {
			return nil, err
		}
		key := truncateTo(created, interval)
		p := buckets[key]
		if p == nil {
			p = &RoutingPoint{Time: key}
			buckets[key] = p
		}
		p.Requests++
		latencySum[key] += latency
		latencyN[key]++

		if decision == "" {
			continue // predates instrumentation: counted, but contributes no rates
		}
		p.Measured++
		p.PromptTokens += prompt
		if cached > prompt {
			cached = prompt // defensive: upstreams disagree on accounting
		}
		p.CachedTokens += cached
		p.BytesSaved += saved
		if saved > 0 {
			p.CompressedRequests++
		}
		switch decision {
		case "sticky_hit", "sticky_wait":
			p.StickyHits++
		case "sticky_spill":
			p.Spills++
		}
		if prompt >= deepTokenThreshold {
			p.DeepRequests++
			if float64(cached)/float64(prompt) < coldReuseThreshold {
				p.ColdRequests++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	series := &RoutingSeries{From: from, To: to, Interval: interval}
	for t := truncateTo(from, interval); !t.After(to); t = advance(t, interval) {
		p := buckets[t]
		if p == nil {
			p = &RoutingPoint{Time: t}
		}
		if p.PromptTokens > 0 {
			p.ReprocessedTokens = p.PromptTokens - p.CachedTokens
			p.CacheReuseRate = float64(p.CachedTokens) / float64(p.PromptTokens)
		}
		if p.DeepRequests > 0 {
			p.ColdRate = float64(p.ColdRequests) / float64(p.DeepRequests)
		}
		if n := latencyN[t]; n > 0 {
			p.MeanLatencyMs = latencySum[t] / n
		}
		series.Points = append(series.Points, *p)
	}
	return series, nil
}

func normalizeRoutingInterval(interval string, span time.Duration) string {
	switch interval {
	case "minute", "hour", "day":
		return interval
	}
	switch {
	case span <= 3*time.Hour:
		return "minute"
	case span <= 7*24*time.Hour:
		return "hour"
	default:
		return "day"
	}
}

func truncateTo(t time.Time, interval string) time.Time {
	t = t.UTC()
	switch interval {
	case "minute":
		return t.Truncate(time.Minute)
	case "day":
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	default:
		return t.Truncate(time.Hour)
	}
}

func advance(t time.Time, interval string) time.Time {
	switch interval {
	case "minute":
		return t.Add(time.Minute)
	case "day":
		return t.AddDate(0, 0, 1)
	default:
		return t.Add(time.Hour)
	}
}
