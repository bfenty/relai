package db

import (
	"database/sql"
	"fmt"
)

// RouterAffinityRepo stores only a SHA-256 digest of a virtual-router session key.
// It restores cache affinity after a relai restart without persisting prompt content
// or a caller-provided raw session ID.
type RouterAffinityRepo struct{ conn *sql.DB }

func newRouterAffinityRepo(conn *sql.DB) *RouterAffinityRepo {
	return &RouterAffinityRepo{conn: conn}
}

// Get returns the selected model mapping for a router/session digest, or zero when
// no unexpired affinity is recorded.
func (r *RouterAffinityRepo) Get(routerID int64, sessionKeyHash string) (int64, error) {
	var modelMapID int64
	err := r.conn.QueryRow(`
		SELECT model_map_id FROM router_affinities
		WHERE router_id = ? AND session_key_hash = ?
	`, routerID, sessionKeyHash).Scan(&modelMapID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return modelMapID, err
}

// Upsert records the successful backend selection and refreshes its last-seen time.
func (r *RouterAffinityRepo) Upsert(routerID int64, sessionKeyHash string, modelMapID int64) error {
	_, err := r.conn.Exec(`
		INSERT INTO router_affinities (router_id, session_key_hash, model_map_id, last_seen_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(router_id, session_key_hash) DO UPDATE SET
			model_map_id = excluded.model_map_id,
			last_seen_at = CURRENT_TIMESTAMP
	`, routerID, sessionKeyHash, modelMapID)
	return err
}

// DeleteOlderThan removes expired cache-placement hints. cutoff must be an RFC3339
// UTC timestamp; it is passed as data rather than interpolated into SQL.
func (r *RouterAffinityRepo) DeleteOlderThan(cutoff string) error {
	_, err := r.conn.Exec(`DELETE FROM router_affinities WHERE last_seen_at < datetime(?)`, cutoff)
	if err != nil {
		return fmt.Errorf("delete expired router affinities: %w", err)
	}
	return nil
}
