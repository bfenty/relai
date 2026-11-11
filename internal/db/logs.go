package db

import (
	"database/sql"
	"strconv"
	"time"
)

// RequestLog records a proxied request.
type RequestLog struct {
	ID             int64     `json:"id"`
	APIKeyID       *int64    `json:"api_key_id,omitempty"`
	BackendID      *int64    `json:"backend_id,omitempty"`
	Model          string    `json:"model"`
	Endpoint       string    `json:"endpoint"`
	RequestTokens  int       `json:"request_tokens"`
	ResponseTokens int       `json:"response_tokens"`
	ThinkingTokens int       `json:"thinking_tokens"`
	TotalTokens    int       `json:"total_tokens"`
	Cost           float64   `json:"cost"`
	StatusCode     int       `json:"status_code"`
	LatencyMs      int       `json:"latency_ms"`
	ErrorMessage   string    `json:"error_message"`
	PrefillMs      int       `json:"prefill_ms"`
	DecodeMs       int       `json:"decode_ms"`
	DraftN         int       `json:"draft_n"`
	DraftNAccepted int       `json:"draft_n_accepted"`
	CreatedAt      time.Time `json:"created_at"`
}

// LogInput is used for creating log entries.
type LogInput struct {
	APIKeyID       *int64
	BackendID      *int64
	Model          string
	Endpoint       string
	RequestTokens  int
	ResponseTokens int
	ThinkingTokens int
	TotalTokens    int
	Cost           float64
	StatusCode     int
	LatencyMs      int
	ErrorMessage   string

	// Routing observability
	ModelMapID      *int64
	RouterName      string
	SessionKey      string
	RoutingDecision string
	WaitMs          int
	CachedTokens    int
	CompressedBytes int

	// Speculation telemetry from the backend's timings (Halogen): draft-token
	// proposals and accepts. 0 = backend did not report (non-Halogen or old).
	DraftN         int
	DraftNAccepted int

	// Prefix diagnostics: why the backend's KV cache was missed. See internal/prefix.
	PrefixCause      string
	PrefixDivergedAt int
	PrefixRetained   float64
	PrefixMessages   int

	// Throughput timing. PrefillMs is send-to-first-token (queue + prompt
	// evaluation); DecodeMs is first token to stream end. Zero when unknown
	// (non-streaming, failed requests) so analytics can exclude them rather
	// than average them toward zero.
	PrefillMs int
	DecodeMs  int

	// DecodeStartAt is the wall-clock instant of the first streamed token —
	// the start of the decode window. Nil when the phase was never observed.
	// Per-host aggregate throughput needs this to know whether requests
	// decoded concurrently (shared wall-clock) or sequentially.
	DecodeStartAt *time.Time

	// Adaptive-thinking observability (issue #10). Pointers so a row written
	// before the feature — or by a router without a classifier — stays NULL
	// instead of masquerading as a classified "off" with zero confidence.
	ThinkingLevel      *string
	ThinkingConfidence *float64
	ClassifierMs       *int
	ThinkingReason     string // classified | timeout | error | low_confidence | shadow | client_specified | ...
}

// LogRepo handles request log operations.
type LogRepo struct {
	db      *sql.DB
	dialect string
}

func newLogRepo(db *sql.DB) *LogRepo {
	return &LogRepo{db: db, dialect: "sqlite"}
}

func newPostgresLogRepo(db *sql.DB) *LogRepo {
	return &LogRepo{db: db, dialect: "postgres"}
}

func (r *LogRepo) Dialect() string {
	return r.dialect
}

func (r *LogRepo) CountAndLatest() (int64, *time.Time, error) {
	var count int64
	var latest sql.NullTime
	if err := r.db.QueryRow(`SELECT COUNT(*), MAX(created_at) FROM request_logs`).Scan(&count, &latest); err != nil {
		return 0, nil, err
	}
	if latest.Valid {
		return count, &latest.Time, nil
	}
	return count, nil, nil
}

func (r *LogRepo) migrate() error {
	if r.dialect != "postgres" {
		return nil
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS request_logs (
			id BIGSERIAL PRIMARY KEY,
			api_key_id BIGINT,
			backend_id BIGINT,
			model TEXT NOT NULL,
			endpoint TEXT NOT NULL,
			request_tokens INTEGER DEFAULT 0,
			response_tokens INTEGER DEFAULT 0,
			thinking_tokens INTEGER DEFAULT 0,
			total_tokens INTEGER DEFAULT 0,
			cost DOUBLE PRECISION DEFAULT 0,
			status_code INTEGER DEFAULT 0,
			latency_ms INTEGER DEFAULT 0,
			error_message TEXT DEFAULT '',
			created_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP
		)`,
		`ALTER TABLE request_logs ADD COLUMN IF NOT EXISTS prefill_ms INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE request_logs ADD COLUMN IF NOT EXISTS decode_ms INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE request_logs ADD COLUMN IF NOT EXISTS decode_start_at TIMESTAMPTZ`,
		`CREATE INDEX IF NOT EXISTS idx_request_logs_created_at ON request_logs(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_request_logs_api_key ON request_logs(api_key_id)`,
		`CREATE INDEX IF NOT EXISTS idx_request_logs_backend ON request_logs(backend_id)`,
		`CREATE INDEX IF NOT EXISTS idx_request_logs_model ON request_logs(model)`,
	}
	for _, stmt := range stmts {
		if _, err := r.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// Create inserts a new log entry.
func (r *LogRepo) Create(input LogInput) (int64, error) {
	query := `
		INSERT INTO request_logs (api_key_id, backend_id, model, endpoint,
			request_tokens, response_tokens, thinking_tokens, total_tokens, cost, status_code,
			latency_ms, error_message,
			model_map_id, router_name, session_key, routing_decision, wait_ms, cached_tokens,
			compressed_bytes,
			prefix_cause, prefix_diverged_at, prefix_retained, prefix_messages,
			prefill_ms, decode_ms, decode_start_at,
			draft_n, draft_n_accepted,
			thinking_level, thinking_confidence, classifier_ms, thinking_reason)
			VALUES (` + r.placeholders(32) + `)`
	args := []any{
		input.APIKeyID, input.BackendID, input.Model, input.Endpoint,
		input.RequestTokens, input.ResponseTokens, input.ThinkingTokens, input.TotalTokens,
		input.Cost, input.StatusCode, input.LatencyMs, input.ErrorMessage,
		input.ModelMapID, input.RouterName, input.SessionKey, input.RoutingDecision,
		input.WaitMs, input.CachedTokens, input.CompressedBytes,
		input.PrefixCause, input.PrefixDivergedAt, input.PrefixRetained, input.PrefixMessages,
		input.PrefillMs, input.DecodeMs, input.DecodeStartAt,
		input.DraftN, input.DraftNAccepted,
		input.ThinkingLevel, input.ThinkingConfidence, input.ClassifierMs, input.ThinkingReason,
	}
	if r.dialect == "postgres" {
		var id int64
		err := r.db.QueryRow(query+" RETURNING id", args...).Scan(&id)
		return id, err
	}
	result, err := r.db.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// List returns logs with optional filtering.
type LogFilter struct {
	StartDate  *time.Time
	EndDate    *time.Time
	BackendID  *int64
	APIKeyID   *int64
	Model      string
	ErrorsOnly bool
	Limit      int
	Offset     int
}

func (r *LogRepo) List(f LogFilter) ([]RequestLog, int, error) {
	query := `SELECT id, api_key_id, backend_id, model, endpoint,
		request_tokens, response_tokens, thinking_tokens, total_tokens, cost, status_code,
		latency_ms, error_message, COALESCE(prefill_ms,0), COALESCE(decode_ms,0),
		COALESCE(draft_n,0), COALESCE(draft_n_accepted,0), created_at
		FROM request_logs WHERE 1=1`
	countQuery := `SELECT COUNT(*) FROM request_logs WHERE 1=1`
	args := []interface{}{}
	countArgs := []interface{}{}
	addArg := func(v any) string {
		args = append(args, v)
		return r.placeholder(len(args))
	}
	addCountArg := func(v any) string {
		countArgs = append(countArgs, v)
		return r.placeholder(len(countArgs))
	}

	if f.StartDate != nil {
		query += ` AND created_at >= ` + addArg(*f.StartDate)
		countQuery += ` AND created_at >= ` + addCountArg(*f.StartDate)
	}
	if f.EndDate != nil {
		query += ` AND created_at <= ` + addArg(*f.EndDate)
		countQuery += ` AND created_at <= ` + addCountArg(*f.EndDate)
	}
	if f.BackendID != nil {
		query += ` AND backend_id = ` + addArg(*f.BackendID)
		countQuery += ` AND backend_id = ` + addCountArg(*f.BackendID)
	}
	if f.APIKeyID != nil {
		query += ` AND api_key_id = ` + addArg(*f.APIKeyID)
		countQuery += ` AND api_key_id = ` + addCountArg(*f.APIKeyID)
	}
	if f.Model != "" {
		query += ` AND model = ` + addArg(f.Model)
		countQuery += ` AND model = ` + addCountArg(f.Model)
	}
	if f.ErrorsOnly {
		query += ` AND (status_code >= 400 OR error_message != '')`
		countQuery += ` AND (status_code >= 400 OR error_message != '')`
	}

	// Get total count
	var total int
	r.db.QueryRow(countQuery, countArgs...).Scan(&total)

	// Add ordering and pagination
	query += ` ORDER BY created_at DESC`
	if f.Limit > 0 {
		query += ` LIMIT ` + addArg(f.Limit)
	}
	if f.Offset > 0 {
		query += ` OFFSET ` + addArg(f.Offset)
	}

	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	logs := make([]RequestLog, 0)
	for rows.Next() {
		var l RequestLog
		err := rows.Scan(&l.ID, &l.APIKeyID, &l.BackendID, &l.Model, &l.Endpoint,
			&l.RequestTokens, &l.ResponseTokens, &l.ThinkingTokens, &l.TotalTokens, &l.Cost,
			&l.StatusCode, &l.LatencyMs, &l.ErrorMessage, &l.PrefillMs, &l.DecodeMs,
			&l.DraftN, &l.DraftNAccepted, &l.CreatedAt)
		if err != nil {
			return nil, 0, err
		}
		logs = append(logs, l)
	}
	return logs, total, rows.Err()
}

// DashboardStats returns aggregated stats for the dashboard.
type DashboardStats struct {
	TotalRequestsToday      int           `json:"total_requests_today"`
	TotalRequestsWeek       int           `json:"total_requests_week"`
	TotalRequestsMonth      int           `json:"total_requests_month"`
	TotalCostToday          float64       `json:"total_cost_today"`
	TotalCostWeek           float64       `json:"total_cost_week"`
	TotalCostMonth          float64       `json:"total_cost_month"`
	TotalTokensToday        int           `json:"total_tokens_today"`
	AvgLatencyMs            float64       `json:"avg_latency_ms"`
	AvgTokensPerSecondToday float64       `json:"avg_tokens_per_second_today"`
	ErrorCountToday         int           `json:"error_count_today"`
	BackendStats            []BackendStat `json:"backend_stats"`
	TopAPIKeys              []APIKeyStat  `json:"top_api_keys"`
	TopUsers                []UserStat    `json:"top_users"`
	TopModels               []ModelStat   `json:"top_models"`
	UsageSeries             []UsagePoint  `json:"usage_series"`
	SeriesInterval          string        `json:"series_interval"` // "hour" or "day"
}

type BackendStat struct {
	BackendID          int64   `json:"backend_id"`
	BackendName        string  `json:"backend_name"`
	Requests           int     `json:"requests"`
	Cost               float64 `json:"cost"`
	AvgLatency         float64 `json:"avg_latency"`
	AvgTokensPerSecond float64 `json:"avg_tokens_per_second"`
}

type APIKeyStat struct {
	APIKeyID int64   `json:"api_key_id"`
	KeyName  string  `json:"key_name"`
	Requests int     `json:"requests"`
	Cost     float64 `json:"cost"`
	Tokens   int     `json:"tokens"`
}

type UserStat struct {
	UserID   int64   `json:"user_id"`
	UserName string  `json:"user_name"`
	Requests int     `json:"requests"`
	Tokens   int     `json:"tokens"`
	Cost     float64 `json:"cost"`
}

type ModelStat struct {
	Model    string  `json:"model"`
	Requests int     `json:"requests"`
	Tokens   int     `json:"tokens"`
	Cost     float64 `json:"cost"`
}

type UsagePoint struct {
	Time     time.Time `json:"time"`
	Requests int       `json:"requests"`
	Tokens   int       `json:"tokens"`
	Cost     float64   `json:"cost"`
	Errors   int       `json:"errors"`
}

// DashboardStats aggregates dashboard data. rangeStr selects the window for
// the breakdowns and usage series: "today" (24 hourly buckets), "week"
// (7 daily buckets), or "month" (30 daily buckets).
func (r *LogRepo) DashboardStats(rangeStr string) (*DashboardStats, error) {
	stats := &DashboardStats{}

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	weekAgo := today.AddDate(0, 0, -7)
	monthAgo := today.AddDate(0, -1, 0)

	// Window for breakdowns (per-backend/key/user/model) and the usage series.
	var since time.Time
	var buckets int
	var bucketSize time.Duration
	var seriesStart time.Time
	switch rangeStr {
	case "week":
		stats.SeriesInterval = "day"
		since = today.AddDate(0, 0, -6)
		seriesStart = since
		buckets = 7
		bucketSize = 24 * time.Hour
	case "month":
		stats.SeriesInterval = "day"
		since = today.AddDate(0, 0, -29)
		seriesStart = since
		buckets = 30
		bucketSize = 24 * time.Hour
	default: // "today"
		stats.SeriesInterval = "hour"
		since = today
		// Last bucket covers the current partial hour: start 23h back so a
		// request logged right now lands in bucket 23, not out of range.
		seriesStart = now.Truncate(time.Hour).Add(-23 * time.Hour)
		buckets = 24
		bucketSize = time.Hour
	}

	// Today stats
	r.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(cost), 0), COALESCE(SUM(total_tokens), 0),
		       COALESCE(AVG(latency_ms), 0),
		       SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END),
		       COALESCE(SUM(CAST(response_tokens AS REAL)) * 1000.0 / NULLIF(SUM(latency_ms), 0), 0)
		FROM request_logs WHERE created_at >= `+r.placeholder(1), r.timeParam(today)).Scan(
		&stats.TotalRequestsToday, &stats.TotalCostToday, &stats.TotalTokensToday,
		&stats.AvgLatencyMs, &stats.ErrorCountToday, &stats.AvgTokensPerSecondToday)

	// Week stats
	r.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(cost), 0)
		FROM request_logs WHERE created_at >= `+r.placeholder(1), r.timeParam(weekAgo)).Scan(
		&stats.TotalRequestsWeek, &stats.TotalCostWeek)

	// Month stats
	r.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(cost), 0)
		FROM request_logs WHERE created_at >= `+r.placeholder(1), r.timeParam(monthAgo)).Scan(
		&stats.TotalRequestsMonth, &stats.TotalCostMonth)

	if r.dialect != "postgres" {
		if err := r.dashboardWithLocalMetadata(stats, since); err != nil {
			return nil, err
		}
	}
	if err := r.dashboardRequestLogOnly(stats, since, seriesStart, buckets, bucketSize); err != nil {
		return nil, err
	}

	return stats, nil
}

// timeParam converts a time bound for use in a created_at comparison.
// SQLite stores CURRENT_TIMESTAMP as a UTC 'YYYY-MM-DD HH:MM:SS' string, so
// bound parameters must be formatted the same way or comparisons silently
// shift by the server's UTC offset. Postgres handles time.Time natively.
func (r *LogRepo) timeParam(t time.Time) any {
	if r.dialect == "postgres" {
		return t
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}

func (r *LogRepo) dashboardWithLocalMetadata(stats *DashboardStats, since time.Time) error {
	rows, err := r.db.Query(`
		SELECT b.id, b.name, COUNT(l.id), COALESCE(SUM(l.cost), 0), COALESCE(AVG(l.latency_ms), 0),
		       COALESCE(SUM(CAST(l.response_tokens AS REAL)) * 1000.0 / NULLIF(SUM(l.latency_ms), 0), 0)
		FROM backends b
		LEFT JOIN request_logs l ON l.backend_id = b.id AND l.created_at >= `+r.placeholder(1)+`
		GROUP BY b.id ORDER BY COUNT(l.id) DESC`, r.timeParam(since))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var bs BackendStat
		if err := rows.Scan(&bs.BackendID, &bs.BackendName, &bs.Requests, &bs.Cost, &bs.AvgLatency, &bs.AvgTokensPerSecond); err != nil {
			return err
		}
		stats.BackendStats = append(stats.BackendStats, bs)
	}

	rows2, err := r.db.Query(`
		SELECT ak.id, ak.name, COUNT(l.id), COALESCE(SUM(l.cost), 0), COALESCE(SUM(l.total_tokens), 0)
		FROM api_keys ak
		LEFT JOIN request_logs l ON l.api_key_id = ak.id AND l.created_at >= `+r.placeholder(1)+`
		GROUP BY ak.id ORDER BY COALESCE(SUM(l.total_tokens), 0) DESC, COUNT(l.id) DESC LIMIT 10`, r.timeParam(since))
	if err != nil {
		return err
	}
	defer rows2.Close()
	for rows2.Next() {
		var ks APIKeyStat
		if err := rows2.Scan(&ks.APIKeyID, &ks.KeyName, &ks.Requests, &ks.Cost, &ks.Tokens); err != nil {
			return err
		}
		stats.TopAPIKeys = append(stats.TopAPIKeys, ks)
	}

	rows3, err := r.db.Query(`
		SELECT u.id, u.name, COUNT(l.id), COALESCE(SUM(l.total_tokens), 0), COALESCE(SUM(l.cost), 0)
		FROM users u
		LEFT JOIN api_keys ak ON ak.user_id = u.id
		LEFT JOIN request_logs l ON l.api_key_id = ak.id AND l.created_at >= `+r.placeholder(1)+`
		GROUP BY u.id ORDER BY COALESCE(SUM(l.total_tokens), 0) DESC, COUNT(l.id) DESC LIMIT 10`, r.timeParam(since))
	if err != nil {
		return err
	}
	defer rows3.Close()
	for rows3.Next() {
		var us UserStat
		if err := rows3.Scan(&us.UserID, &us.UserName, &us.Requests, &us.Tokens, &us.Cost); err != nil {
			return err
		}
		stats.TopUsers = append(stats.TopUsers, us)
	}
	return nil
}

func (r *LogRepo) dashboardRequestLogOnly(stats *DashboardStats, since, seriesStart time.Time, buckets int, bucketSize time.Duration) error {
	rows, err := r.db.Query(`
		SELECT model, COUNT(*), COALESCE(SUM(total_tokens), 0), COALESCE(SUM(cost), 0)
		FROM request_logs
		WHERE created_at >= `+r.placeholder(1)+`
		GROUP BY model ORDER BY COALESCE(SUM(total_tokens), 0) DESC, COUNT(*) DESC LIMIT 10`, r.timeParam(since))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ms ModelStat
		if err := rows.Scan(&ms.Model, &ms.Requests, &ms.Tokens, &ms.Cost); err != nil {
			return err
		}
		stats.TopModels = append(stats.TopModels, ms)
	}

	series := make([]UsagePoint, buckets)
	for i := range series {
		series[i].Time = seriesStart.Add(time.Duration(i) * bucketSize)
	}

	rows2, err := r.db.Query(`
		SELECT created_at, total_tokens, cost, status_code
		FROM request_logs
		WHERE created_at >= `+r.placeholder(1)+`
		ORDER BY created_at ASC`, r.timeParam(seriesStart))
	if err != nil {
		return err
	}
	defer rows2.Close()
	for rows2.Next() {
		var createdAt time.Time
		var tokens, status int
		var cost float64
		if err := rows2.Scan(&createdAt, &tokens, &cost, &status); err != nil {
			return err
		}
		idx := int(createdAt.Sub(seriesStart) / bucketSize)
		if idx < 0 || idx >= len(series) {
			continue
		}
		series[idx].Requests++
		series[idx].Tokens += tokens
		series[idx].Cost += cost
		if status >= 400 {
			series[idx].Errors++
		}
	}
	stats.UsageSeries = series
	return nil
}

// MetricsData holds data for the Prometheus /metrics endpoint.
type MetricsData struct {
	TotalRequests  int64
	TotalErrors    int64
	InputTokens    int64
	OutputTokens   int64
	ThinkingTokens int64
	CostDollars    float64
	BackendMetrics []BackendMetric
}

type BackendMetric struct {
	Name     string
	Status   string
	Requests int64
	Errors   int64
}

func (r *LogRepo) MetricsData() (*MetricsData, error) {
	m := &MetricsData{}
	r.db.QueryRow(`
		SELECT COUNT(*),
		       SUM(CASE WHEN status_code >= 400 THEN 1 ELSE 0 END),
		       COALESCE(SUM(request_tokens), 0),
		       COALESCE(SUM(response_tokens), 0),
		       COALESCE(SUM(thinking_tokens), 0),
		       COALESCE(SUM(cost), 0)
		FROM request_logs`).Scan(
		&m.TotalRequests, &m.TotalErrors,
		&m.InputTokens, &m.OutputTokens, &m.ThinkingTokens, &m.CostDollars)

	rows, err := r.db.Query(`
		SELECT b.name, b.status,
		       COUNT(l.id),
		       COALESCE(SUM(CASE WHEN l.status_code >= 400 THEN 1 ELSE 0 END), 0)
		FROM backends b
		LEFT JOIN request_logs l ON l.backend_id = b.id
		GROUP BY b.id`)
	if err != nil {
		return m, nil
	}
	defer rows.Close()
	for rows.Next() {
		var bm BackendMetric
		rows.Scan(&bm.Name, &bm.Status, &bm.Requests, &bm.Errors)
		m.BackendMetrics = append(m.BackendMetrics, bm)
	}
	return m, nil
}

func (r *LogRepo) placeholder(n int) string {
	if r.dialect == "postgres" {
		return "$" + strconv.Itoa(n)
	}
	return "?"
}

func (r *LogRepo) placeholders(n int) string {
	out := ""
	for i := 1; i <= n; i++ {
		if i > 1 {
			out += ", "
		}
		out += r.placeholder(i)
	}
	return out
}
