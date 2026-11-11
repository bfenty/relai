package db

import (
	"database/sql"
	"fmt"
)

// SessionFootprintRepo stores the per-conversation KV footprint estimate (#19):
// the last observed prompt size (retained_bytes) and the warm host (model_map_id)
// for each virtual-router session. Only the SHA-256 digest of the session key is
// stored — never prompt content or raw caller headers.
//
// The value is an estimate of what the conversation needs resident in the warm
// host's KV pool, derived from request sizes, not measured engine state. It is
// the input to cost-aware spill decisions (#18): bumping a small footprint is
// cheap; a whale's re-prefill is not.
type SessionFootprintRepo struct{ conn *sql.DB }

func newSessionFootprintRepo(conn *sql.DB) *SessionFootprintRepo {
	return &SessionFootprintRepo{conn: conn}
}

// SessionFootprint is one conversation's last-known KV footprint.
type SessionFootprint struct {
	RouterID       int64
	SessionKeyHash string
	ModelMapID     int64
	RetainedBytes  int64
}

// Upsert records (or refreshes) a session's footprint estimate.
func (r *SessionFootprintRepo) Upsert(routerID int64, sessionKeyHash string, modelMapID int64, retainedBytes int64) error {
	_, err := r.conn.Exec(`
		INSERT INTO session_kv_footprints (router_id, session_key_hash, model_map_id, retained_bytes, last_seen_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(router_id, session_key_hash) DO UPDATE SET
			model_map_id = excluded.model_map_id,
			retained_bytes = excluded.retained_bytes,
			last_seen_at = CURRENT_TIMESTAMP
	`, routerID, sessionKeyHash, modelMapID, retainedBytes)
	return err
}

// ListByRouter returns all footprints for a router, largest first — the order
// the spill policy wants to evaluate (small conversations are spill candidates).
func (r *SessionFootprintRepo) ListByRouter(routerID int64) ([]SessionFootprint, error) {
	rows, err := r.conn.Query(`
		SELECT router_id, session_key_hash, model_map_id, retained_bytes
		FROM session_kv_footprints
		WHERE router_id = ?
		ORDER BY retained_bytes ASC`, routerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionFootprint
	for rows.Next() {
		var f SessionFootprint
		if err := rows.Scan(&f.RouterID, &f.SessionKeyHash, &f.ModelMapID, &f.RetainedBytes); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// DeleteOlderThan removes footprints for expired sessions; cutoff is RFC3339 UTC.
func (r *SessionFootprintRepo) DeleteOlderThan(cutoff string) error {
	_, err := r.conn.Exec(`DELETE FROM session_kv_footprints WHERE last_seen_at < datetime(?)`, cutoff)
	if err != nil {
		return fmt.Errorf("delete expired session footprints: %w", err)
	}
	return nil
}

// Get returns the retained_bytes for one session, or 0 when unknown/expired.
func (r *SessionFootprintRepo) Get(routerID int64, sessionKeyHash string) (int64, error) {
	var bytes int64
	err := r.conn.QueryRow(`
		SELECT retained_bytes FROM session_kv_footprints
		WHERE router_id = ? AND session_key_hash = ?`, routerID, sessionKeyHash).Scan(&bytes)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return bytes, err
}
