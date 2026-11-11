package db

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// DB wraps the SQLite connection and provides access to all repositories.
type DB struct {
	conn              *sql.DB
	logConn           *sql.DB
	path              string
	Backends          *BackendRepo
	APIKeys           *APIKeyRepo
	Routing           *RoutingRepo
	ModelMaps         *ModelMapRepo
	Logs              *LogRepo
	Routers           *RouterRepo
	Users             *UserRepo
	Sessions          *SessionRepo
	AdminCreds        *AdminCredRepo
	LoginAttempts     *LoginAttemptRepo
	SystemEvents      *SystemEventRepo
	MCPServers        *MCPServerRepo
	Settings          *SettingsRepo
	RouterAffinities  *RouterAffinityRepo
	SessionFootprints *SessionFootprintRepo
}

// sqliteBusyTimeout is how long a writer waits for the write lock before giving up.
// SQLite allows one writer at a time even in WAL mode; without a busy timeout a
// contending writer fails immediately with SQLITE_BUSY. Every authenticated request
// takes a write transaction (CheckRateLimit), so concurrent traffic on one API key
// otherwise produces spurious 500s.
const sqliteBusyTimeout = 5000 // milliseconds

// sqliteDSN attaches the pragmas to the DSN so every pooled connection gets them.
// Running them via conn.Exec only configures whichever connection the pool happens
// to hand out, leaving the others without a busy timeout.
func sqliteDSN(path string) string {
	// _txlock=immediate makes Begin() take the write lock up front. With the default
	// deferred transactions, two callers that read before writing (CheckRateLimit, the
	// config import/export transactions) both hold read locks and then deadlock on the
	// upgrade - SQLite returns SQLITE_BUSY straight away and busy_timeout cannot help,
	// because retrying an upgrade can never succeed without restarting the transaction.
	pragmas := "_pragma=busy_timeout(" + strconv.Itoa(sqliteBusyTimeout) + ")" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(ON)" +
		"&_txlock=immediate"
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + pragmas
}

// isMemoryDSN reports whether path refers to a private in-memory database. Each
// connection to one of those gets its own empty database, so the pool must be
// pinned to a single connection.
func isMemoryDSN(path string) bool {
	return strings.HasPrefix(path, ":memory:") || strings.Contains(path, "mode=memory")
}

// New opens the SQLite database and runs migrations.
func New(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if isMemoryDSN(path) {
		// A private in-memory DB exists per connection; more than one would silently
		// hand out empty databases.
		conn.SetMaxOpenConns(1)
	}

	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	db := &DB{conn: conn, path: path}
	if err := db.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	db.Backends = newBackendRepo(conn)
	db.APIKeys = newAPIKeyRepo(conn)
	db.Routing = newRoutingRepo(conn)
	db.ModelMaps = newModelMapRepo(conn)
	db.Logs = newLogRepo(conn)
	db.Routers = newRouterRepo(conn)
	db.Users = newUserRepo(conn)
	db.Sessions = newSessionRepo(conn)
	db.AdminCreds = newAdminCredRepo(conn)
	db.LoginAttempts = newLoginAttemptRepo(conn)
	db.SystemEvents = newSystemEventRepo(conn)
	db.MCPServers = newMCPServerRepo(conn)
	db.Settings = newSettingsRepo(conn)
	db.RouterAffinities = newRouterAffinityRepo(conn)
	db.SessionFootprints = newSessionFootprintRepo(conn)

	return db, nil
}

// Close closes the database connection.
func (d *DB) Close() error {
	if d.logConn != nil {
		_ = d.logConn.Close()
	}
	return d.conn.Close()
}

func (d *DB) Ping() error {
	return d.conn.Ping()
}

func (d *DB) DBPath() string {
	return d.path
}

func (d *DB) PingLogs() error {
	if d.logConn == nil {
		return nil
	}
	return d.logConn.Ping()
}

func (d *DB) ActiveModelTargetCount() (int, error) {
	var count int
	err := d.conn.QueryRow(`
		SELECT COUNT(*)
		FROM model_mappings mm
		JOIN backends b ON b.id = mm.backend_id
		WHERE b.status = 'active'
		  AND mm.status NOT IN ('error', 'offline', 'inactive')`).Scan(&count)
	return count, err
}

func (d *DB) UnreadyRouterNames() ([]string, error) {
	rows, err := d.conn.Query(`
		SELECT r.name
		FROM routers r
		WHERE NOT EXISTS (
			SELECT 1
			FROM router_members rm
			JOIN model_mappings mm ON mm.id = rm.model_map_id
			JOIN backends b ON b.id = mm.backend_id
			WHERE rm.router_id = r.id
			  AND b.status = 'active'
			  AND mm.status NOT IN ('error', 'offline', 'inactive')
		)
		ORDER BY r.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	names := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// UsePostgresLogs writes request_logs and log-derived dashboards to PostgreSQL.
func (d *DB) UsePostgresLogs(dsn string) error {
	conn, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open postgres logs: %w", err)
	}
	if err := conn.Ping(); err != nil {
		conn.Close()
		return fmt.Errorf("ping postgres logs: %w", err)
	}

	logs := newPostgresLogRepo(conn)
	if err := logs.migrate(); err != nil {
		conn.Close()
		return fmt.Errorf("migrate postgres logs: %w", err)
	}

	d.logConn = conn
	d.Logs = logs
	return nil
}

func (d *DB) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS backends (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			base_url TEXT NOT NULL,
			api_key TEXT DEFAULT '',
			weight INTEGER DEFAULT 1,
			status TEXT DEFAULT 'active',
			last_latency_ms INTEGER DEFAULT 0,
			last_health_check TIMESTAMP,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key_hash TEXT NOT NULL UNIQUE,
			key_prefix TEXT NOT NULL,
			name TEXT NOT NULL,
			rate_limit_rpm INTEGER DEFAULT 60,
			rate_limit_remaining INTEGER DEFAULT 60,
			rate_limit_reset_at TIMESTAMP,
			allowed_backends TEXT DEFAULT '[]',
			allowed_models TEXT DEFAULT '[]',
			is_active BOOLEAN DEFAULT 1,
			last_used_at TIMESTAMP,
			total_cost REAL DEFAULT 0,
			total_requests INTEGER DEFAULT 0,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS routing_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			strategy TEXT NOT NULL,
			priority INTEGER NOT NULL,
			conditions TEXT NOT NULL DEFAULT '{}',
			config TEXT DEFAULT '{}',
			is_active BOOLEAN DEFAULT 1,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS model_mappings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			backend_id INTEGER NOT NULL REFERENCES backends(id) ON DELETE CASCADE,
			remote_model TEXT NOT NULL,
			local_model TEXT NOT NULL,
			UNIQUE(local_model),
			UNIQUE(backend_id, remote_model, local_model)
		)`,
		`CREATE TABLE IF NOT EXISTS request_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			api_key_id INTEGER REFERENCES api_keys(id),
			backend_id INTEGER REFERENCES backends(id),
			model TEXT NOT NULL,
			endpoint TEXT NOT NULL,
			request_tokens INTEGER DEFAULT 0,
			response_tokens INTEGER DEFAULT 0,
			total_tokens INTEGER DEFAULT 0,
			cost REAL DEFAULT 0,
			status_code INTEGER DEFAULT 0,
			latency_ms INTEGER DEFAULT 0,
			error_message TEXT DEFAULT '',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_request_logs_created_at ON request_logs(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_request_logs_api_key ON request_logs(api_key_id)`,
		`CREATE INDEX IF NOT EXISTS idx_request_logs_backend ON request_logs(backend_id)`,
		`CREATE INDEX IF NOT EXISTS idx_routing_rules_priority ON routing_rules(priority)`,
		// Additive migrations for model_mappings — safe to re-run (duplicate column errors are ignored)
		`ALTER TABLE model_mappings ADD COLUMN base_url TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE model_mappings ADD COLUMN status TEXT NOT NULL DEFAULT 'unknown'`,
		`ALTER TABLE model_mappings ADD COLUMN last_latency_ms INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE model_mappings ADD COLUMN last_health_check TIMESTAMP`,
		`ALTER TABLE model_mappings ADD COLUMN params TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE model_mappings ADD COLUMN input_cost_per_million REAL NOT NULL DEFAULT 0`,
		`ALTER TABLE model_mappings ADD COLUMN output_cost_per_million REAL NOT NULL DEFAULT 0`,
		`ALTER TABLE model_mappings ADD COLUMN model_type TEXT NOT NULL DEFAULT 'chat'`,
		`ALTER TABLE model_mappings ADD COLUMN modes TEXT NOT NULL DEFAULT '[]'`,
		`CREATE TABLE IF NOT EXISTS routers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			strategy TEXT DEFAULT 'sticky',
			cache_enabled BOOLEAN DEFAULT 0,
			cache_ttl_seconds INTEGER DEFAULT 3600,
			queue_timeout_seconds INTEGER DEFAULT 60,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS router_members (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			router_id INTEGER NOT NULL REFERENCES routers(id) ON DELETE CASCADE,
			model_map_id INTEGER NOT NULL REFERENCES model_mappings(id) ON DELETE CASCADE,
			priority INTEGER DEFAULT 0,
			UNIQUE(router_id, model_map_id)
		)`,
		`ALTER TABLE routers ADD COLUMN model_type TEXT NOT NULL DEFAULT 'chat'`,
		`ALTER TABLE backends ADD COLUMN color TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			email TEXT NOT NULL UNIQUE,
			is_active BOOLEAN DEFAULT 1,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`ALTER TABLE api_keys ADD COLUMN user_id INTEGER REFERENCES users(id) ON DELETE SET NULL`,
		`ALTER TABLE api_keys ADD COLUMN owner TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE api_keys ADD COLUMN scopes TEXT NOT NULL DEFAULT '["inference","models:read","embeddings","rerank"]'`,
		`ALTER TABLE api_keys ADD COLUMN expires_at TIMESTAMP`,
		// Interactive priority: keys with priority>0 get X-Halogen-Priority on
		// upstream requests, letting Halogen's admission reserve place these
		// ahead of the bulk queue. Additive column; default 0 = ordinary.
		`ALTER TABLE api_keys ADD COLUMN priority INTEGER NOT NULL DEFAULT 0`,
		// Speculation telemetry (Halogen timings.draft_n / draft_n_accepted):
		// drafter proposals and accepts per request. The accept rate is the
		// MTP health signal — drift means a model/config regression.
		`ALTER TABLE request_logs ADD COLUMN draft_n INTEGER`,
		`ALTER TABLE request_logs ADD COLUMN draft_n_accepted INTEGER`,
		`ALTER TABLE backends ADD COLUMN archived_at TIMESTAMP`,
		`ALTER TABLE request_logs ADD COLUMN model_map_id INTEGER`,
		`ALTER TABLE request_logs ADD COLUMN router_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE request_logs ADD COLUMN session_key TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE request_logs ADD COLUMN routing_decision TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE request_logs ADD COLUMN wait_ms INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE request_logs ADD COLUMN cached_tokens INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE model_mappings ADD COLUMN compression TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE routers ADD COLUMN compression TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE request_logs ADD COLUMN compressed_bytes INTEGER NOT NULL DEFAULT 0`,
		`CREATE INDEX IF NOT EXISTS idx_request_logs_routing ON request_logs(routing_decision, created_at)`,
		// Prefix diagnostics: why a request missed the backend's KV cache. cached_tokens
		// records that a miss happened; these record what invalidated the prefix.
		`ALTER TABLE request_logs ADD COLUMN prefix_cause TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE request_logs ADD COLUMN prefix_diverged_at INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE request_logs ADD COLUMN prefix_retained REAL NOT NULL DEFAULT 0`,
		`ALTER TABLE request_logs ADD COLUMN prefix_messages INTEGER NOT NULL DEFAULT 0`,
		`CREATE INDEX IF NOT EXISTS idx_request_logs_prefix ON request_logs(prefix_cause, created_at)`,
		`ALTER TABLE request_logs ADD COLUMN thinking_tokens INTEGER NOT NULL DEFAULT 0`,
		// Throughput timing: prefill_ms is time from send to first streamed
		// token (queue + prompt evaluation); decode_ms is first token to stream
		// end. Dividing the reported token counts by these gives honest prefill
		// and decode tok/s per request without storing any content.
		`ALTER TABLE request_logs ADD COLUMN prefill_ms INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE request_logs ADD COLUMN decode_ms INTEGER NOT NULL DEFAULT 0`,
		`CREATE TABLE IF NOT EXISTS admin_credentials (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			username      TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS admin_sessions (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			token_hash   TEXT NOT NULL UNIQUE,
			ip_address   TEXT NOT NULL DEFAULT '',
			created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			expires_at   TIMESTAMP NOT NULL,
			last_seen_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS login_attempts (
			ip           TEXT PRIMARY KEY,
			attempts     INTEGER DEFAULT 0,
			last_attempt TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			locked_until TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS system_events (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			level      TEXT NOT NULL DEFAULT 'info',
			event_type TEXT NOT NULL DEFAULT 'system',
			message    TEXT NOT NULL,
			metadata   TEXT NOT NULL DEFAULT '{}',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_system_events_created_at ON system_events(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_system_events_level ON system_events(level)`,
		`CREATE TABLE IF NOT EXISTS mcp_servers (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			name        TEXT NOT NULL UNIQUE,
			url         TEXT NOT NULL DEFAULT '',
			transport   TEXT NOT NULL DEFAULT 'http',
			api_key     TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			is_enabled  BOOLEAN DEFAULT 1,
			created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS app_settings (
			key        TEXT PRIMARY KEY,
			value      TEXT NOT NULL DEFAULT '',
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`ALTER TABLE api_keys ADD COLUMN is_test BOOLEAN NOT NULL DEFAULT 0`,
		`ALTER TABLE backends ADD COLUMN max_concurrent INTEGER NOT NULL DEFAULT 1`,
		// Existing hosts retain Relai's historical OpenAI-compatible/local semantics.
		// These additive defaults keep backend IDs, model mappings, and router state intact.
		`ALTER TABLE backends ADD COLUMN provider TEXT NOT NULL DEFAULT 'openai-compatible'`,
		`ALTER TABLE backends ADD COLUMN origin TEXT NOT NULL DEFAULT 'local'`,
		// Persistent virtual-router affinity survives a relai restart. session_key_hash
		// is a SHA-256 digest; prompt content and raw session IDs are never stored.
		`CREATE TABLE IF NOT EXISTS router_affinities (
			router_id INTEGER NOT NULL REFERENCES routers(id) ON DELETE CASCADE,
			session_key_hash TEXT NOT NULL,
			model_map_id INTEGER NOT NULL REFERENCES model_mappings(id) ON DELETE CASCADE,
			last_seen_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (router_id, session_key_hash)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_router_affinities_last_seen ON router_affinities(last_seen_at)`,
		// Per-conversation KV footprint (#19): an estimate of what each session
		// needs resident in its warm host's KV pool. retained_bytes is the last
		// observed prompt size scaled by prefix retention; it is an estimate,
		// not measured engine state. Only the session-key digest is stored.
		`CREATE TABLE IF NOT EXISTS session_kv_footprints (
			router_id INTEGER NOT NULL REFERENCES routers(id) ON DELETE CASCADE,
			session_key_hash TEXT NOT NULL,
			model_map_id INTEGER NOT NULL DEFAULT 0,
			retained_bytes INTEGER NOT NULL DEFAULT 0,
			last_seen_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (router_id, session_key_hash)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_session_kv_footprints_last_seen ON session_kv_footprints(last_seen_at)`,
		// Adaptive thinking (issue #10): per-router classifier sidecar config for
		// the adaptive_thinking strategy. All nullable/additive — existing routers
		// keep byte-for-byte behavior because an empty sidecar URL disables the
		// classifier entirely.
		`ALTER TABLE routers ADD COLUMN thinking_sidecar_url TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE routers ADD COLUMN thinking_timeout_ms INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE routers ADD COLUMN thinking_default_level TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE routers ADD COLUMN thinking_confidence_floor REAL NOT NULL DEFAULT 0`,
		`ALTER TABLE routers ADD COLUMN thinking_mode TEXT NOT NULL DEFAULT 'shadow'`,
		`ALTER TABLE routers ADD COLUMN thinking_param_template TEXT NOT NULL DEFAULT ''`,
		// Per-request classifier observability. Nullable so rows predating the
		// feature stay distinguishable from rows where the classifier ran and
		// chose "off".
		`ALTER TABLE request_logs ADD COLUMN thinking_level TEXT`,
		`ALTER TABLE request_logs ADD COLUMN thinking_confidence REAL`,
		`ALTER TABLE request_logs ADD COLUMN classifier_ms INTEGER`,
		`ALTER TABLE request_logs ADD COLUMN thinking_reason TEXT NOT NULL DEFAULT ''`,
		// Wall-clock start of the decode phase (time of first streamed token).
		// decode_ms alone can't locate the decode window in time, so per-host
		// aggregate throughput can't tell whether two requests decoded
		// concurrently or back-to-back. NULL for rows written before this
		// column and for requests whose first token was never observed.
		`ALTER TABLE request_logs ADD COLUMN decode_start_at TIMESTAMP`,
	}

	for _, stmt := range migrations {
		if _, err := d.conn.Exec(stmt); err != nil {
			// ALTER TABLE ADD COLUMN fails with "duplicate column name" on re-runs — that's fine
			if strings.Contains(err.Error(), "duplicate column name") {
				continue
			}
			return fmt.Errorf("migration failed: %s: %w", stmt[:50], err)
		}
	}
	if err := d.relaxModelMappingUniqueConstraint(); err != nil {
		return err
	}
	if err := d.ensureUniqueLocalModelNames(); err != nil {
		return err
	}
	return nil
}

func (d *DB) relaxModelMappingUniqueConstraint() error {
	var sqlText string
	err := d.conn.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'model_mappings'`).Scan(&sqlText)
	if err != nil {
		return fmt.Errorf("inspect model_mappings schema: %w", err)
	}
	if strings.Contains(sqlText, "UNIQUE(backend_id, remote_model, local_model)") ||
		strings.Contains(sqlText, "UNIQUE (backend_id, remote_model, local_model)") {
		return nil
	}
	if !strings.Contains(sqlText, "UNIQUE(backend_id, remote_model)") &&
		!strings.Contains(sqlText, "UNIQUE (backend_id, remote_model)") {
		return nil
	}

	if _, err := d.conn.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer d.conn.Exec(`PRAGMA foreign_keys=ON`)

	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmts := []string{
		`DROP TABLE IF EXISTS model_mappings_new`,
		`CREATE TABLE model_mappings_new (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			backend_id INTEGER NOT NULL REFERENCES backends(id) ON DELETE CASCADE,
			remote_model TEXT NOT NULL,
			local_model TEXT NOT NULL,
			base_url TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'unknown',
			last_latency_ms INTEGER NOT NULL DEFAULT 0,
			last_health_check TIMESTAMP,
			params TEXT NOT NULL DEFAULT '{}',
			input_cost_per_million REAL NOT NULL DEFAULT 0,
			output_cost_per_million REAL NOT NULL DEFAULT 0,
			model_type TEXT NOT NULL DEFAULT 'chat',
			modes TEXT NOT NULL DEFAULT '[]',
			compression TEXT NOT NULL DEFAULT '',
			UNIQUE(backend_id, remote_model, local_model)
		)`,
		`INSERT INTO model_mappings_new (
			id, backend_id, remote_model, local_model, base_url, status, last_latency_ms,
			last_health_check, params, input_cost_per_million, output_cost_per_million, model_type, modes,
			compression
		)
		SELECT id, backend_id, remote_model, local_model, base_url, status, last_latency_ms,
			last_health_check, params, input_cost_per_million, output_cost_per_million, model_type, modes,
			compression
		FROM model_mappings`,
		`DROP TABLE model_mappings`,
		`ALTER TABLE model_mappings_new RENAME TO model_mappings`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("relax model mapping uniqueness: %w", err)
		}
	}
	return tx.Commit()
}

func (d *DB) ensureUniqueLocalModelNames() error {
	if err := d.renameDuplicateLocalModels(); err != nil {
		return err
	}
	if _, err := d.conn.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_model_mappings_local_model_unique ON model_mappings(local_model)`); err != nil {
		return fmt.Errorf("create local model uniqueness index: %w", err)
	}
	return nil
}

func (d *DB) renameDuplicateLocalModels() error {
	rows, err := d.conn.Query(`
		SELECT local_model
		FROM model_mappings
		GROUP BY local_model
		HAVING COUNT(*) > 1`)
	if err != nil {
		return err
	}
	defer rows.Close()

	duplicates := []string{}
	for rows.Next() {
		var localModel string
		if err := rows.Scan(&localModel); err != nil {
			return err
		}
		duplicates = append(duplicates, localModel)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(duplicates) == 0 {
		return nil
	}

	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, localModel := range duplicates {
		idRows, err := tx.Query(`SELECT id FROM model_mappings WHERE local_model = ? ORDER BY id`, localModel)
		if err != nil {
			return err
		}
		ids := []int64{}
		for idRows.Next() {
			var id int64
			if err := idRows.Scan(&id); err != nil {
				idRows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := idRows.Err(); err != nil {
			idRows.Close()
			return err
		}
		idRows.Close()

		for _, id := range ids[1:] {
			name, err := d.uniqueLocalModelName(tx, localModel, id)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE model_mappings SET local_model = ? WHERE id = ?`, name, id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (d *DB) uniqueLocalModelName(tx *sql.Tx, base string, id int64) (string, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "model"
	}
	for suffix := 0; suffix < 1000; suffix++ {
		candidate := fmt.Sprintf("%s-%d", base, id)
		if suffix > 0 {
			candidate = fmt.Sprintf("%s-%d-%d", base, id, suffix)
		}
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM model_mappings WHERE local_model = ?`, candidate).Scan(&exists); err != nil {
			return "", err
		}
		if exists == 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not create unique local model name for %q", base)
}
