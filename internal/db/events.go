package db

import (
	"database/sql"
	"encoding/json"
	"time"
)

type EventLevel = string
type EventType = string

const (
	LevelInfo  EventLevel = "info"
	LevelWarn  EventLevel = "warn"
	LevelError EventLevel = "error"

	EventAuth   EventType = "auth"
	EventProxy  EventType = "proxy"
	EventHealth EventType = "health"
	EventConfig EventType = "config"
	EventSystem EventType = "system"
)

type SystemEvent struct {
	ID        int64          `json:"id"`
	Level     EventLevel     `json:"level"`
	EventType EventType      `json:"event_type"`
	Message   string         `json:"message"`
	Metadata  map[string]any `json:"metadata"`
	CreatedAt time.Time      `json:"created_at"`
}

type EventFilters struct {
	Levels    []string
	EventType string
	Search    string
	From      string
	To        string
	Limit     int
	Offset    int
}

type SystemEventRepo struct{ conn *sql.DB }

func newSystemEventRepo(conn *sql.DB) *SystemEventRepo { return &SystemEventRepo{conn: conn} }

// Log records a system event. Errors are silently dropped so callers don't need to handle them.
func (r *SystemEventRepo) Log(level EventLevel, eventType EventType, message string, meta map[string]any) {
	metaJSON := "{}"
	if meta != nil {
		if b, err := json.Marshal(meta); err == nil {
			metaJSON = string(b)
		}
	}
	r.conn.Exec(`
		INSERT INTO system_events (level, event_type, message, metadata)
		VALUES (?, ?, ?, ?)
	`, level, eventType, message, metaJSON)
}

// List returns events matching the given filters.
func (r *SystemEventRepo) List(f EventFilters) ([]SystemEvent, int, error) {
	if f.Limit == 0 {
		f.Limit = 50
	}

	where := "1=1"
	args := []any{}

	if len(f.Levels) > 0 {
		placeholders := ""
		for i, l := range f.Levels {
			if i > 0 {
				placeholders += ","
			}
			placeholders += "?"
			args = append(args, l)
		}
		where += " AND level IN (" + placeholders + ")"
	}
	if f.EventType != "" {
		where += " AND event_type = ?"
		args = append(args, f.EventType)
	}
	if f.Search != "" {
		where += " AND message LIKE ?"
		args = append(args, "%"+f.Search+"%")
	}
	if f.From != "" {
		where += " AND created_at >= ?"
		args = append(args, f.From)
	}
	if f.To != "" {
		where += " AND created_at <= ?"
		args = append(args, f.To)
	}

	var total int
	if err := r.conn.QueryRow("SELECT COUNT(*) FROM system_events WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.conn.Query(`
		SELECT id, level, event_type, message, metadata, created_at
		FROM system_events WHERE `+where+`
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var events []SystemEvent
	for rows.Next() {
		var e SystemEvent
		var metaJSON string
		var createdAt string
		if err := rows.Scan(&e.ID, &e.Level, &e.EventType, &e.Message, &metaJSON, &createdAt); err != nil {
			return nil, 0, err
		}
		json.Unmarshal([]byte(metaJSON), &e.Metadata) //nolint
		e.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
		events = append(events, e)
	}
	return events, total, nil
}
