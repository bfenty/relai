package db

import (
	"fmt"
	"sort"
	"strconv"
	"time"
)

// AnalyticsCell is the aggregated usage for a single (bucket, group) pair.
type AnalyticsCell struct {
	Requests int     `json:"requests"`
	Tokens   int     `json:"tokens"`
	Cost     float64 `json:"cost"`
	Errors   int     `json:"errors"`
}

// AnalyticsBucket is one time bucket holding per-group aggregates.
type AnalyticsBucket struct {
	Time   time.Time                `json:"time"`
	Groups map[string]AnalyticsCell `json:"groups"`
}

// AnalyticsGroup is the total for a single group across the whole range,
// used to drive the legend and the drill-down table.
type AnalyticsGroup struct {
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	Requests int     `json:"requests"`
	Tokens   int     `json:"tokens"`
	Cost     float64 `json:"cost"`
	Errors   int     `json:"errors"`
}

// AnalyticsResult is the full response for a usage-analytics query.
type AnalyticsResult struct {
	GroupBy  string            `json:"group_by"`
	Interval string            `json:"interval"`
	Start    time.Time         `json:"start"`
	End      time.Time         `json:"end"`
	Buckets  []AnalyticsBucket `json:"buckets"`
	Groups   []AnalyticsGroup  `json:"groups"`
	Summary  AnalyticsCell     `json:"summary"`
}

// AnalyticsOptions parameterize a usage-analytics query.
type AnalyticsOptions struct {
	Start    time.Time
	End      time.Time
	GroupBy  string // model, api_key, user, backend
	Interval string // hour, day, week, or "" for auto
}

// otherGroupKey collects groups beyond the top maxAnalyticsGroups so stacked
// charts stay readable while totals remain accurate.
const (
	otherGroupKey       = "__other__"
	maxAnalyticsGroups  = 15
	maxAnalyticsBuckets = 2000
)

// UsageAnalytics aggregates request_logs into time buckets grouped by the
// requested dimension. It is dialect-agnostic: rows are pulled from the log
// store (SQLite or Postgres) and bucketed in Go, while human-readable labels
// for keys/users/backends are resolved from the app SQLite DB. This is the one
// code path that works whether or not Postgres log storage is enabled.
func (d *DB) UsageAnalytics(opts AnalyticsOptions) (*AnalyticsResult, error) {
	switch opts.GroupBy {
	case "model", "api_key", "user", "backend":
	default:
		opts.GroupBy = "model"
	}
	if opts.End.Before(opts.Start) {
		opts.Start, opts.End = opts.End, opts.Start
	}
	if !opts.End.After(opts.Start) {
		opts.End = opts.Start.Add(time.Hour)
	}

	interval := opts.Interval
	if interval != "hour" && interval != "day" && interval != "week" {
		interval = autoInterval(opts.End.Sub(opts.Start))
	}

	loc := time.Local

	// Resolve label maps from the app DB based on the grouping dimension.
	var keyNames, backendNames, userNames map[int64]string
	var keyToUser map[int64]*int64
	var err error
	switch opts.GroupBy {
	case "api_key":
		keyNames, _, err = d.apiKeyLabels()
	case "user":
		keyNames, keyToUser, err = d.apiKeyLabels()
		if err == nil {
			userNames, err = d.idNameMap(`SELECT id, name FROM users`)
		}
	case "backend":
		backendNames, err = d.idNameMap(`SELECT id, name FROM backends`)
	}
	if err != nil {
		return nil, err
	}

	// Pull the raw rows in range from the log store.
	logDB := d.Logs.db
	rows, err := logDB.Query(`
		SELECT created_at, api_key_id, backend_id, model, total_tokens, cost, status_code
		FROM request_logs
		WHERE created_at >= `+d.Logs.placeholder(1)+` AND created_at < `+d.Logs.placeholder(2)+`
		ORDER BY created_at ASC`,
		d.Logs.timeParam(opts.Start), d.Logs.timeParam(opts.End))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type cellKey struct {
		bucket time.Time
		group  string
	}
	cells := map[cellKey]*AnalyticsCell{}
	groupTotals := map[string]*AnalyticsGroup{}
	summary := AnalyticsCell{}

	for rows.Next() {
		var (
			createdAt time.Time
			apiKeyID  *int64
			backendID *int64
			model     string
			tokens    int
			cost      float64
			status    int
		)
		if err := rows.Scan(&createdAt, &apiKeyID, &backendID, &model, &tokens, &cost, &status); err != nil {
			return nil, err
		}

		key, label := analyticsGroupKey(opts.GroupBy, model, apiKeyID, backendID, keyNames, backendNames, userNames, keyToUser)
		bucket := alignBucket(createdAt, interval, loc)

		ck := cellKey{bucket: bucket, group: key}
		c := cells[ck]
		if c == nil {
			c = &AnalyticsCell{}
			cells[ck] = c
		}
		gt := groupTotals[key]
		if gt == nil {
			gt = &AnalyticsGroup{Key: key, Label: label}
			groupTotals[key] = gt
		}

		isErr := status >= 400
		c.Requests++
		c.Tokens += tokens
		c.Cost += cost
		gt.Requests++
		gt.Tokens += tokens
		gt.Cost += cost
		summary.Requests++
		summary.Tokens += tokens
		summary.Cost += cost
		if isErr {
			c.Errors++
			gt.Errors++
			summary.Errors++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Rank groups; collapse the long tail into "Other" for the chart series.
	groups := make([]*AnalyticsGroup, 0, len(groupTotals))
	for _, g := range groupTotals {
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Tokens != groups[j].Tokens {
			return groups[i].Tokens > groups[j].Tokens
		}
		return groups[i].Requests > groups[j].Requests
	})

	keptKeys := map[string]bool{}
	for i, g := range groups {
		if i < maxAnalyticsGroups {
			keptKeys[g.Key] = true
		}
	}
	remap := func(key string) string {
		if keptKeys[key] {
			return key
		}
		return otherGroupKey
	}

	// Build the ordered, gap-filled bucket timeline.
	startBucket := alignBucket(opts.Start, interval, loc)
	order := []time.Time{}
	index := map[time.Time]int{}
	for t := startBucket; t.Before(opts.End); t = stepBucket(t, interval) {
		index[t] = len(order)
		order = append(order, t)
		if len(order) >= maxAnalyticsBuckets {
			break
		}
	}
	buckets := make([]AnalyticsBucket, len(order))
	for i, t := range order {
		buckets[i] = AnalyticsBucket{Time: t, Groups: map[string]AnalyticsCell{}}
	}
	for ck, c := range cells {
		i, ok := index[ck.bucket]
		if !ok {
			continue
		}
		gk := remap(ck.group)
		existing := buckets[i].Groups[gk]
		existing.Requests += c.Requests
		existing.Tokens += c.Tokens
		existing.Cost += c.Cost
		existing.Errors += c.Errors
		buckets[i].Groups[gk] = existing
	}

	// Final group list: top groups plus a synthetic "Other" if anything was collapsed.
	outGroups := make([]AnalyticsGroup, 0, len(groups)+1)
	var other AnalyticsGroup
	other.Key = otherGroupKey
	other.Label = "Other"
	hasOther := false
	for i, g := range groups {
		if i < maxAnalyticsGroups {
			outGroups = append(outGroups, *g)
			continue
		}
		hasOther = true
		other.Requests += g.Requests
		other.Tokens += g.Tokens
		other.Cost += g.Cost
		other.Errors += g.Errors
	}
	if hasOther {
		outGroups = append(outGroups, other)
	}

	return &AnalyticsResult{
		GroupBy:  opts.GroupBy,
		Interval: interval,
		Start:    opts.Start,
		End:      opts.End,
		Buckets:  buckets,
		Groups:   outGroups,
		Summary:  summary,
	}, nil
}

// analyticsGroupKey returns the stable key and display label for a log row
// under the chosen grouping dimension.
func analyticsGroupKey(groupBy, model string, apiKeyID, backendID *int64,
	keyNames, backendNames, userNames map[int64]string, keyToUser map[int64]*int64) (string, string) {
	switch groupBy {
	case "api_key":
		if apiKeyID == nil {
			return "none", "(anonymous)"
		}
		name := keyNames[*apiKeyID]
		if name == "" {
			name = "key #" + strconv.FormatInt(*apiKeyID, 10)
		}
		return strconv.FormatInt(*apiKeyID, 10), name
	case "user":
		if apiKeyID == nil {
			return "none", "(unassigned)"
		}
		uid := keyToUser[*apiKeyID]
		if uid == nil {
			return "none", "(unassigned)"
		}
		name := userNames[*uid]
		if name == "" {
			name = "user #" + strconv.FormatInt(*uid, 10)
		}
		return "u" + strconv.FormatInt(*uid, 10), name
	case "backend":
		if backendID == nil {
			return "none", "(direct)"
		}
		name := backendNames[*backendID]
		if name == "" {
			name = "host #" + strconv.FormatInt(*backendID, 10)
		}
		return strconv.FormatInt(*backendID, 10), name
	default: // model
		if model == "" {
			return "unknown", "(unknown)"
		}
		return model, model
	}
}

func autoInterval(span time.Duration) string {
	switch {
	case span <= 48*time.Hour:
		return "hour"
	case span <= 92*24*time.Hour:
		return "day"
	default:
		return "week"
	}
}

// alignBucket snaps t (in loc) to the start of its interval bucket.
func alignBucket(t time.Time, interval string, loc *time.Location) time.Time {
	t = t.In(loc)
	switch interval {
	case "hour":
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc)
	case "week":
		wd := (int(t.Weekday()) + 6) % 7 // Monday = 0
		d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		return d.AddDate(0, 0, -wd)
	default: // day
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	}
}

func stepBucket(t time.Time, interval string) time.Time {
	switch interval {
	case "hour":
		return t.Add(time.Hour)
	case "week":
		return t.AddDate(0, 0, 7)
	default:
		return t.AddDate(0, 0, 1)
	}
}

// apiKeyLabels returns id->name and id->user_id maps for all API keys.
func (d *DB) apiKeyLabels() (map[int64]string, map[int64]*int64, error) {
	rows, err := d.conn.Query(`SELECT id, name, user_id FROM api_keys`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	names := map[int64]string{}
	users := map[int64]*int64{}
	for rows.Next() {
		var id int64
		var name string
		var userID *int64
		if err := rows.Scan(&id, &name, &userID); err != nil {
			return nil, nil, err
		}
		names[id] = name
		users[id] = userID
	}
	return names, users, rows.Err()
}

// idNameMap runs a `SELECT id, name FROM ...` query into a map.
func (d *DB) idNameMap(query string) (map[int64]string, error) {
	rows, err := d.conn.Query(query)
	if err != nil {
		return nil, fmt.Errorf("load labels: %w", err)
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}
