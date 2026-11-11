package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/bfenty/relai/internal/db"
)

// HandleEvents returns system events with optional filtering.
// Query params: level (comma-separated), type, search, from, to, limit, offset
func (h *AdminHandler) HandleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()

	f := db.EventFilters{
		EventType: q.Get("type"),
		Search:    q.Get("search"),
		From:      q.Get("from"),
		To:        q.Get("to"),
	}

	if raw := q.Get("level"); raw != "" {
		for _, l := range strings.Split(raw, ",") {
			if l = strings.TrimSpace(l); l != "" {
				f.Levels = append(f.Levels, l)
			}
		}
	}

	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
		f.Limit = n
	}
	if n, err := strconv.Atoi(q.Get("offset")); err == nil && n >= 0 {
		f.Offset = n
	}

	events, total, err := h.db.SystemEvents.List(f)
	if err != nil {
		writeJSONError(w, "failed to list events", http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []db.SystemEvent{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"events": events,
		"total":  total,
	})
}
