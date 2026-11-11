package db

import (
	"database/sql"
	"fmt"
	"sort"
	"time"
)

// BackendThroughput is one backend's share of a time bucket.
//
// DecodeTokS is the summed-duration rate (tokens / Σ decode_ms). It is a
// concurrency-weighted average of per-request rates and therefore sags under
// batching just like the per-request chart — kept for continuity.
//
// DecodeWallTokS is the true per-host aggregate: each request's tokens are
// attributed to the wall-clock seconds its decode window actually occupied
// (overlapping requests share the clock), so the rate RISES with batching —
// total tokens the machine emitted per second of real time spent decoding.
// Requests predating decode_start_at instrumentation are excluded, so this
// field is 0 until the column has data.
type BackendThroughput struct {
	BackendID   int64  `json:"backend_id"`
	BackendName string `json:"backend_name"`
	Requests    int64  `json:"requests"`

	PrefillRequests int64   `json:"prefill_requests"`
	PrefillTokens   int64   `json:"prefill_tokens"`
	PrefillMs       int64   `json:"prefill_ms"`
	PrefillTokS     float64 `json:"prefill_tok_s"`

	DecodeRequests int64   `json:"decode_requests"`
	DecodeTokens   int64   `json:"decode_tokens"`
	DecodeMs       int64   `json:"decode_ms"`
	DecodeTokS     float64 `json:"decode_tok_s"`

	DecodeWallTokS float64 `json:"decode_wall_tok_s"`

	// Speculation (Halogen MTP): draft proposals and accepts observed in the
	// bucket. AcceptRate is accepted/proposals; 0 when no request reported
	// (SpecRequests=0), which distinguishes "no data" from "0% accepted".
	SpecRequests int64   `json:"spec_requests"`
	DraftN       int64   `json:"draft_n"`
	DraftNAccept int64   `json:"draft_n_accepted"`
	AcceptRate   float64 `json:"accept_rate"`
}

// ThroughputPoint is one time bucket of measured prefill/decode throughput.
//
// Rates are token-weighted (sum tokens / sum time), not an average of per-request
// rates: a 40k-token prefill and a 200-token prefill should not count the same.
// Requests without recorded phase timing (non-streaming, failures, rows predating
// instrumentation) are counted in Requests but excluded from the rates.
type ThroughputPoint struct {
	Time     time.Time `json:"time"`
	Requests int64     `json:"requests"`

	PrefillRequests int64   `json:"prefill_requests"`
	PrefillTokens   int64   `json:"prefill_tokens"`
	PrefillMs       int64   `json:"prefill_ms"`
	PrefillTokS     float64 `json:"prefill_tok_s"`

	DecodeRequests int64   `json:"decode_requests"`
	DecodeTokens   int64   `json:"decode_tokens"`
	DecodeMs       int64   `json:"decode_ms"`
	DecodeTokS     float64 `json:"decode_tok_s"`

	// Speculation (Halogen MTP) rollup for the bucket; see BackendThroughput.
	SpecRequests int64   `json:"spec_requests"`
	DraftN       int64   `json:"draft_n"`
	DraftNAccept int64   `json:"draft_n_accepted"`
	AcceptRate   float64 `json:"accept_rate"`

	// ByBackend splits the bucket per host (omitted when no request in the
	// bucket carried a backend). Sorted by backend ID for stable rendering.
	ByBackend []BackendThroughput `json:"by_backend,omitempty"`
}

// ThroughputSeries is a time-bucketed view of generation throughput.
type ThroughputSeries struct {
	From     time.Time         `json:"from"`
	To       time.Time         `json:"to"`
	Interval string            `json:"interval"`
	Points   []ThroughputPoint `json:"points"`
}

// timeInterval is a wall-clock span [start, end) used for decode-window
// attribution.
type timeInterval struct{ start, end time.Time }

// ThroughputTimeSeries buckets measured prefill/decode throughput over a window.
// interval accepts "minute", "hour" or "day"; anything else is chosen from the span.
func (d *DB) ThroughputTimeSeries(from, to time.Time, interval string) (*ThroughputSeries, error) {
	if to.IsZero() {
		to = time.Now()
	}
	if from.IsZero() {
		from = to.Add(-24 * time.Hour)
	}
	interval = normalizeRoutingInterval(interval, to.Sub(from))

	rows, err := d.Logs.db.Query(`
		SELECT l.created_at, COALESCE(l.request_tokens,0), COALESCE(l.response_tokens,0),
		       COALESCE(l.cached_tokens,0), COALESCE(l.prefill_ms,0), COALESCE(l.decode_ms,0),
		       l.decode_start_at, l.backend_id, COALESCE(b.name, ''),
		       COALESCE(l.draft_n,0), COALESCE(l.draft_n_accepted,0)
		FROM request_logs l
		LEFT JOIN backends b ON b.id = l.backend_id
		WHERE l.created_at >= `+d.Logs.placeholder(1)+` AND l.created_at <= `+d.Logs.placeholder(2)+`
		ORDER BY l.created_at ASC`, d.Logs.timeParam(from), d.Logs.timeParam(to))
	if err != nil {
		return nil, fmt.Errorf("throughput series: %w", err)
	}
	defer rows.Close()

	buckets := map[time.Time]*ThroughputPoint{}
	type hostKey struct {
		time time.Time
		host int64
	}
	hostBuckets := map[hostKey]*BackendThroughput{}
	hostOrder := map[int64]string{}
	// decodeWindow is one request's decode phase in wall-clock time. Tokens
	// are attributed to each bucket the window overlaps, proportional to the
	// overlap; the per-host denominator is the UNION of windows (concurrent
	// requests share the clock), which is what makes the rate rise with
	// batching instead of averaging per-request sag.
	type decodeWindow struct {
		host  int64
		start time.Time
		end   time.Time
		toks  int64
	}
	var windows []decodeWindow
	for rows.Next() {
		var (
			created             time.Time
			prompt, completion  int64
			cached              int64
			prefillMs, decodeMs int64
			decodeStart         sql.NullTime
			backendID           sql.NullInt64
			backendName         string
			draftN, draftAccept int64
		)
		if err := rows.Scan(&created, &prompt, &completion, &cached, &prefillMs, &decodeMs,
			&decodeStart, &backendID, &backendName, &draftN, &draftAccept); err != nil {
			return nil, err
		}
		key := truncateTo(created, interval)
		p := buckets[key]
		if p == nil {
			p = &ThroughputPoint{Time: key}
			buckets[key] = p
		}
		p.Requests++
		// Prefill rate counts only freshly evaluated tokens. request_tokens
		// includes KV-cache hits, which the host read from cache instead of
		// evaluating; dividing them by the short prefill time reports cache
		// reads as GPU throughput (order-of-magnitude inflation under sticky
		// routing).
		fresh := prompt - cached
		hasPrefill := prefillMs > 0 && fresh > 0
		hasDecode := decodeMs > 0 && completion > 0
		if hasPrefill {
			p.PrefillRequests++
			p.PrefillTokens += fresh
			p.PrefillMs += prefillMs
		}
		if hasDecode {
			p.DecodeRequests++
			p.DecodeTokens += completion
			p.DecodeMs += decodeMs
		}
		if draftN > 0 {
			p.SpecRequests++
			p.DraftN += draftN
			p.DraftNAccept += draftAccept
		}
		if backendID.Valid {
			hostOrder[backendID.Int64] = backendName
			hk := hostKey{time: key, host: backendID.Int64}
			bp := hostBuckets[hk]
			if bp == nil {
				bp = &BackendThroughput{BackendID: backendID.Int64, BackendName: backendName}
				hostBuckets[hk] = bp
			}
			bp.Requests++
			if draftN > 0 {
				bp.SpecRequests++
				bp.DraftN += draftN
				bp.DraftNAccept += draftAccept
			}
			if hasPrefill {
				bp.PrefillRequests++
				bp.PrefillTokens += fresh
				bp.PrefillMs += prefillMs
			}
			if hasDecode {
				bp.DecodeRequests++
				bp.DecodeTokens += completion
				bp.DecodeMs += decodeMs
				if decodeStart.Valid && decodeMs > 0 {
					windows = append(windows, decodeWindow{
						host:  backendID.Int64,
						start: decodeStart.Time,
						end:   decodeStart.Time.Add(time.Duration(decodeMs) * time.Millisecond),
						toks:  completion,
					})
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Attribute decode windows to buckets by wall-clock overlap.
	// wallToks[hostKey] = tokens generated during the bucket (proportional
	// to overlap); wallSecs[hostKey] = seconds the host spent decoding in
	// the bucket, counting overlapping requests once (union of intervals).
	wallToks := map[hostKey]float64{}
	wallIntervals := map[hostKey][]timeInterval{}
	for _, w := range windows {
		dur := w.end.Sub(w.start)
		if dur <= 0 {
			continue
		}
		for cur := truncateTo(w.start, interval); cur.Before(w.end); cur = advance(cur, interval) {
			next := advance(cur, interval)
			lo, hi := w.start, w.end
			if lo.Before(cur) {
				lo = cur
			}
			if hi.After(next) {
				hi = next
			}
			overlap := hi.Sub(lo)
			if overlap > 0 {
				hk := hostKey{time: cur, host: w.host}
				wallToks[hk] += float64(w.toks) * float64(overlap) / float64(dur)
				wallIntervals[hk] = append(wallIntervals[hk], timeInterval{start: lo, end: hi})
			}
		}
	}
	wallSecs := map[hostKey]float64{}
	for hk, ivs := range wallIntervals {
		wallSecs[hk] = unionSeconds(ivs)
	}
	// A window straddling a bucket boundary attributes tokens to a bucket the
	// request itself was not bucketed in (bucketing keys off created_at, i.e.
	// stream end). Seed host rows so wall attribution always has a home.
	for hk := range wallSecs {
		if hostBuckets[hk] == nil {
			hostBuckets[hk] = &BackendThroughput{BackendID: hk.host, BackendName: hostOrder[hk.host]}
		}
	}
	series := &ThroughputSeries{From: from, To: to, Interval: interval}
	for t := truncateTo(from, interval); !t.After(to); t = advance(t, interval) {
		p := buckets[t]
		if p == nil {
			p = &ThroughputPoint{Time: t}
		}
		if p.PrefillMs > 0 {
			p.PrefillTokS = 1000 * float64(p.PrefillTokens) / float64(p.PrefillMs)
		}
		if p.DecodeMs > 0 {
			p.DecodeTokS = 1000 * float64(p.DecodeTokens) / float64(p.DecodeMs)
		}
		if p.DraftN > 0 {
			p.AcceptRate = float64(p.DraftNAccept) / float64(p.DraftN)
		}
		var hosts []BackendThroughput
		for id := range hostOrder {
			if bp := hostBuckets[hostKey{time: t, host: id}]; bp != nil {
				if bp.PrefillMs > 0 {
					bp.PrefillTokS = 1000 * float64(bp.PrefillTokens) / float64(bp.PrefillMs)
				}
				if bp.DecodeMs > 0 {
					bp.DecodeTokS = 1000 * float64(bp.DecodeTokens) / float64(bp.DecodeMs)
				}
				if secs := wallSecs[hostKey{time: t, host: id}]; secs > 0 {
					bp.DecodeWallTokS = wallToks[hostKey{time: t, host: id}] / secs
				}
				if bp.DraftN > 0 {
					bp.AcceptRate = float64(bp.DraftNAccept) / float64(bp.DraftN)
				}
				hosts = append(hosts, *bp)
			}
		}
		if len(hosts) > 0 {
			sort.Slice(hosts, func(i, j int) bool { return hosts[i].BackendID < hosts[j].BackendID })
			p.ByBackend = hosts
		}
		series.Points = append(series.Points, *p)
	}
	return series, nil
}

// unionSeconds merges overlapping intervals and returns their total length in
// seconds. Concurrent decode windows on one host overlap in wall-clock time;
// merging them means the machine's clock is counted once no matter how many
// requests shared it — the property that makes the aggregate rate rise with
// batching.
func unionSeconds(ivs []timeInterval) float64 {
	if len(ivs) == 0 {
		return 0
	}
	sort.Slice(ivs, func(i, j int) bool { return ivs[i].start.Before(ivs[j].start) })
	var total time.Duration
	curStart, curEnd := ivs[0].start, ivs[0].end
	for _, iv := range ivs[1:] {
		if iv.start.After(curEnd) {
			total += curEnd.Sub(curStart)
			curStart, curEnd = iv.start, iv.end
			continue
		}
		if iv.end.After(curEnd) {
			curEnd = iv.end
		}
	}
	total += curEnd.Sub(curStart)
	return total.Seconds()
}
