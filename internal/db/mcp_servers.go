package db

import (
	"database/sql"
	"time"
)

type MCPServer struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	Transport   string    `json:"transport"` // "http" or "stdio"
	APIKey      string    `json:"api_key,omitempty"`
	HasAPIKey   bool      `json:"has_api_key"`
	Description string    `json:"description"`
	IsEnabled   bool      `json:"is_enabled"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type MCPServerRepo struct{ conn *sql.DB }

func newMCPServerRepo(conn *sql.DB) *MCPServerRepo { return &MCPServerRepo{conn: conn} }

func (r *MCPServerRepo) List() ([]MCPServer, error) {
	rows, err := r.conn.Query(`
		SELECT id, name, url, transport, api_key, description, is_enabled, created_at, updated_at
		FROM mcp_servers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MCPServer
	for rows.Next() {
		s, err := scanMCPServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, nil
}

func (r *MCPServerRepo) Get(id int64) (*MCPServer, error) {
	row := r.conn.QueryRow(`
		SELECT id, name, url, transport, api_key, description, is_enabled, created_at, updated_at
		FROM mcp_servers WHERE id = ?`, id)
	return scanMCPServer(row)
}

func (r *MCPServerRepo) Create(s *MCPServer) (*MCPServer, error) {
	res, err := r.conn.Exec(`
		INSERT INTO mcp_servers (name, url, transport, api_key, description, is_enabled)
		VALUES (?, ?, ?, ?, ?, ?)`,
		s.Name, s.URL, s.Transport, s.APIKey, s.Description, s.IsEnabled)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return r.Get(id)
}

func (r *MCPServerRepo) Update(id int64, s *MCPServer) (*MCPServer, error) {
	// An empty incoming key means "keep the stored one" (same contract as
	// UpsertByName and backends): the GUI round-trips redacted objects, so a
	// metadata-only edit must not wipe the secret.
	if s.APIKey == "" {
		if cur, err := r.Get(id); err == nil && cur != nil {
			s.APIKey = cur.APIKey
		}
	}
	_, err := r.conn.Exec(`
		UPDATE mcp_servers
		SET name=?, url=?, transport=?, api_key=?, description=?, is_enabled=?,
		    updated_at=CURRENT_TIMESTAMP
		WHERE id=?`,
		s.Name, s.URL, s.Transport, s.APIKey, s.Description, s.IsEnabled, id)
	if err != nil {
		return nil, err
	}
	return r.Get(id)
}

// UpsertByName creates or updates an MCP server matched by name.
// If the incoming APIKey is empty, the existing key is preserved.
func (r *MCPServerRepo) UpsertByName(s *MCPServer) (*MCPServer, error) {
	transport := s.Transport
	if transport == "" {
		transport = "http"
	}
	_, err := r.conn.Exec(`
		INSERT INTO mcp_servers (name, url, transport, api_key, description, is_enabled)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			url        = excluded.url,
			transport  = excluded.transport,
			api_key    = CASE WHEN excluded.api_key = '' THEN mcp_servers.api_key ELSE excluded.api_key END,
			description = excluded.description,
			is_enabled = excluded.is_enabled,
			updated_at = CURRENT_TIMESTAMP`,
		s.Name, s.URL, transport, s.APIKey, s.Description, s.IsEnabled)
	if err != nil {
		return nil, err
	}
	row := r.conn.QueryRow(`
		SELECT id, name, url, transport, api_key, description, is_enabled, created_at, updated_at
		FROM mcp_servers WHERE name = ?`, s.Name)
	return scanMCPServer(row)
}

func (r *MCPServerRepo) Delete(id int64) error {
	_, err := r.conn.Exec(`DELETE FROM mcp_servers WHERE id = ?`, id)
	return err
}

type mcpScanner interface {
	Scan(dest ...any) error
}

func scanMCPServer(s mcpScanner) (*MCPServer, error) {
	var m MCPServer
	var ca, ua string
	err := s.Scan(&m.ID, &m.Name, &m.URL, &m.Transport, &m.APIKey,
		&m.Description, &m.IsEnabled, &ca, &ua)
	if err != nil {
		return nil, err
	}
	m.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", ca)
	m.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", ua)
	m.HasAPIKey = m.APIKey != ""
	return &m, nil
}
