package db

import (
	"fmt"
	"sort"
	"time"
)

// Prefix diagnostics answer the question cached_tokens leaves open: a request missed
// the backend's KV cache, but why? The causes are recorded per request by
// internal/prefix; this aggregates them so the expensive ones can be found and,
// unlike a bare cold-arrival rate, actually acted on.

// PrefixCauseStat is one cause and what it cost over the window.
type PrefixCauseStat struct {
	Cause    string  `json:"cause"`
	Requests int64   `json:"requests"`
	Share    float64 `json:"share"`

	// ReprocessedTokens is prompt tokens the backend had to evaluate again because
	// the prefix was invalid. This, not the request count, is the real cost: one
	// rewrite at 180k tokens outweighs a hundred at 500.
	ReprocessedTokens int64 `json:"reprocessed_tokens"`

	// EstimatedSeconds converts those tokens to wall time at the measured prefill
	// rate, because seconds are what anyone actually cares about.
	EstimatedSeconds float64 `json:"estimated_seconds"`

	MeanRetained float64 `json:"mean_retained"`
}

// PrefixStats summarises cache-miss causes over a window.
type PrefixStats struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`

	// Diagnosed counts requests carrying a diagnosis. Rows written before prefix
	// instrumentation have none, and counting them would understate every share.
	Diagnosed int64 `json:"diagnosed_requests"`
	// Faults are the diagnosed requests attributed to a specific, fixable cause.
	Faults int64 `json:"fault_requests"`

	ReprocessedTokens int64   `json:"reprocessed_tokens"`
	EstimatedSeconds  float64 `json:"estimated_seconds"`

	Causes []PrefixCauseStat `json:"causes"`

	// Offenders are the worst client/model combinations, so a fault points at
	// something specific rather than at the cluster in general.
	Offenders []PrefixOffender `json:"offenders"`
}

// PrefixOffender attributes rewrites to whoever is sending them.
type PrefixOffender struct {
	Model             string  `json:"model"`
	APIKeyName        string  `json:"api_key_name"`
	Cause             string  `json:"cause"`
	Requests          int64   `json:"requests"`
	ReprocessedTokens int64   `json:"reprocessed_tokens"`
	EstimatedSeconds  float64 `json:"estimated_seconds"`
}

// prefillTokensPerSecond converts reprocessed tokens into wall time. Measured on the
// gfx1151 host at deep context; it is a local constant rather than a per-backend
// figure because it only needs to make costs comparable, not exact.
const prefillTokensPerSecond = 259.0

// PrefixAnalytics aggregates cache-miss causes over a window.
func (d *DB) PrefixAnalytics(from, to time.Time) (*PrefixStats, error) {
	if to.IsZero() {
		to = time.Now()
	}
	if from.IsZero() {
		from = to.Add(-24 * time.Hour)
	}

	rows, err := d.Logs.db.Query(`
		SELECT COALESCE(l.prefix_cause,''), COALESCE(l.request_tokens,0),
		       COALESCE(l.cached_tokens,0), COALESCE(l.prefix_retained,0),
		       COALESCE(l.model,''), COALESCE(k.name,'')
		FROM request_logs l
		LEFT JOIN api_keys k ON k.id = l.api_key_id
		WHERE l.created_at >= `+d.Logs.placeholder(1)+`
		  AND l.created_at <= `+d.Logs.placeholder(2)+`
		  AND COALESCE(l.prefix_cause,'') != ''`,
		d.Logs.timeParam(from), d.Logs.timeParam(to))
	if err != nil {
		return nil, fmt.Errorf("prefix analytics: %w", err)
	}
	defer rows.Close()

	stats := &PrefixStats{From: from, To: to}
	type agg struct {
		requests   int64
		reprocess  int64
		retainSum  float64
		retainRows int64
	}
	byCause := map[string]*agg{}
	type offKey struct{ model, key, cause string }
	byOffender := map[offKey]*agg{}

	for rows.Next() {
		var cause, model, keyName string
		var promptTokens, cachedTokens int64
		var retained float64
		if err := rows.Scan(&cause, &promptTokens, &cachedTokens, &retained, &model, &keyName); err != nil {
			return nil, err
		}
		stats.Diagnosed++

		// Tokens the backend had to evaluate again. Taken from what it actually
		// reported rather than from the retention estimate, so the cost reflects the
		// backend's behaviour and not our model of it.
		reprocessed := promptTokens - cachedTokens
		if reprocessed < 0 {
			reprocessed = 0
		}

		a := byCause[cause]
		if a == nil {
			a = &agg{}
			byCause[cause] = a
		}
		a.requests++
		a.reprocess += reprocessed
		a.retainSum += retained
		a.retainRows++

		if prefixCauseIsFault(cause) {
			stats.Faults++
			stats.ReprocessedTokens += reprocessed
			k := offKey{model: model, key: keyName, cause: cause}
			o := byOffender[k]
			if o == nil {
				o = &agg{}
				byOffender[k] = o
			}
			o.requests++
			o.reprocess += reprocessed
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	stats.EstimatedSeconds = float64(stats.ReprocessedTokens) / prefillTokensPerSecond

	for cause, a := range byCause {
		s := PrefixCauseStat{
			Cause:             cause,
			Requests:          a.requests,
			ReprocessedTokens: a.reprocess,
			EstimatedSeconds:  float64(a.reprocess) / prefillTokensPerSecond,
		}
		if stats.Diagnosed > 0 {
			s.Share = float64(a.requests) / float64(stats.Diagnosed)
		}
		if a.retainRows > 0 {
			s.MeanRetained = a.retainSum / float64(a.retainRows)
		}
		stats.Causes = append(stats.Causes, s)
	}
	// Rank by cost, not by count: the point of the view is to surface what is
	// actually expensive.
	sort.Slice(stats.Causes, func(i, j int) bool {
		if stats.Causes[i].ReprocessedTokens != stats.Causes[j].ReprocessedTokens {
			return stats.Causes[i].ReprocessedTokens > stats.Causes[j].ReprocessedTokens
		}
		return stats.Causes[i].Requests > stats.Causes[j].Requests
	})

	for k, a := range byOffender {
		stats.Offenders = append(stats.Offenders, PrefixOffender{
			Model:             k.model,
			APIKeyName:        k.key,
			Cause:             k.cause,
			Requests:          a.requests,
			ReprocessedTokens: a.reprocess,
			EstimatedSeconds:  float64(a.reprocess) / prefillTokensPerSecond,
		})
	}
	sort.Slice(stats.Offenders, func(i, j int) bool {
		return stats.Offenders[i].ReprocessedTokens > stats.Offenders[j].ReprocessedTokens
	})
	if len(stats.Offenders) > 10 {
		stats.Offenders = stats.Offenders[:10]
	}
	if stats.Causes == nil {
		stats.Causes = []PrefixCauseStat{}
	}
	if stats.Offenders == nil {
		stats.Offenders = []PrefixOffender{}
	}
	return stats, nil
}

// prefixCauseIsFault mirrors prefix.Blames. It is duplicated rather than imported
// because internal/prefix depends on nothing, and keeping it that way avoids a cycle
// through the db package.
func prefixCauseIsFault(cause string) bool {
	switch cause {
	case "system", "head", "tail", "truncated", "host_change", "evicted":
		return true
	default:
		return false
	}
}

// ─── Time series ──────────────────────────────────────────────────────────────

// PrefixPoint is one bucket of cache-miss causes.
type PrefixPoint struct {
	Time      time.Time `json:"time"`
	Diagnosed int64     `json:"diagnosed_requests"`
	Faults    int64     `json:"fault_requests"`

	// One field per cause so a stacked chart can show the mix directly.
	Append          int64 `json:"append"`
	System          int64 `json:"system"`
	Head            int64 `json:"head"`
	Tail            int64 `json:"tail"`
	Truncated       int64 `json:"truncated"`
	HostChange      int64 `json:"host_change"`
	Evicted         int64 `json:"evicted"`
	NewConversation int64 `json:"new_conversation"`

	ReprocessedTokens int64   `json:"reprocessed_tokens"`
	EstimatedSeconds  float64 `json:"estimated_seconds"`
	FaultRate         float64 `json:"fault_rate"`
}

// PrefixSeries is a time-bucketed view of cache-miss causes.
type PrefixSeries struct {
	From     time.Time     `json:"from"`
	To       time.Time     `json:"to"`
	Interval string        `json:"interval"`
	Points   []PrefixPoint `json:"points"`
}

// PrefixTimeSeries buckets cache-miss causes over a window, so a regression can be
// tied to when it started rather than only to how much it costs.
func (d *DB) PrefixTimeSeries(from, to time.Time, interval string) (*PrefixSeries, error) {
	if to.IsZero() {
		to = time.Now()
	}
	if from.IsZero() {
		from = to.Add(-24 * time.Hour)
	}
	interval = normalizeRoutingInterval(interval, to.Sub(from))

	rows, err := d.Logs.db.Query(`
		SELECT created_at, COALESCE(prefix_cause,''), COALESCE(request_tokens,0),
		       COALESCE(cached_tokens,0)
		FROM request_logs
		WHERE created_at >= `+d.Logs.placeholder(1)+`
		  AND created_at <= `+d.Logs.placeholder(2)+`
		  AND COALESCE(prefix_cause,'') != ''
		ORDER BY created_at ASC`,
		d.Logs.timeParam(from), d.Logs.timeParam(to))
	if err != nil {
		return nil, fmt.Errorf("prefix series: %w", err)
	}
	defer rows.Close()

	buckets := map[time.Time]*PrefixPoint{}
	for rows.Next() {
		var created time.Time
		var cause string
		var promptTokens, cachedTokens int64
		if err := rows.Scan(&created, &cause, &promptTokens, &cachedTokens); err != nil {
			return nil, err
		}
		key := truncateTo(created, interval)
		p := buckets[key]
		if p == nil {
			p = &PrefixPoint{Time: key}
			buckets[key] = p
		}
		p.Diagnosed++
		switch cause {
		case "append":
			p.Append++
		case "system":
			p.System++
		case "head":
			p.Head++
		case "tail":
			p.Tail++
		case "truncated":
			p.Truncated++
		case "host_change":
			p.HostChange++
		case "evicted":
			p.Evicted++
		case "new_conversation":
			p.NewConversation++
		}
		if prefixCauseIsFault(cause) {
			p.Faults++
			if r := promptTokens - cachedTokens; r > 0 {
				p.ReprocessedTokens += r
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	series := &PrefixSeries{From: from, To: to, Interval: interval, Points: []PrefixPoint{}}
	// Emit every bucket in the range, including empty ones, so a gap in traffic reads
	// as a gap rather than as the chart silently closing over it.
	for t := truncateTo(from, interval); !t.After(to); t = advance(t, interval) {
		p := buckets[t]
		if p == nil {
			p = &PrefixPoint{Time: t}
		}
		p.EstimatedSeconds = float64(p.ReprocessedTokens) / prefillTokensPerSecond
		if p.Diagnosed > 0 {
			p.FaultRate = float64(p.Faults) / float64(p.Diagnosed)
		}
		series.Points = append(series.Points, *p)
	}
	return series, nil
}
