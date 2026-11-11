package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/bfenty/relai/internal/db"
	"github.com/bfenty/relai/internal/influx"
)

// HandleInfluxSettings serves GET and PUT /api/settings/influx.
//
// The stored token is never returned; has_token reports whether one is set, and an
// empty token on PUT preserves the stored value so the redacted form round-trips.
func (h *AdminHandler) HandleInfluxSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := h.db.Settings.GetInflux()
		if err != nil {
			writeJSONError(w, "failed to read metrics export settings", http.StatusInternalServerError)
			return
		}
		writeInflux(w, cfg)
	case http.MethodPut:
		var in db.InfluxSettings
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSONError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		cfg, err := h.db.Settings.UpdateInflux(in)
		if err != nil {
			writeJSONError(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeInflux(w, cfg)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// HandleInfluxTest writes a single point using the supplied settings, so a
// configuration can be verified before it is enabled. Falls back to the stored
// token when the request carries none.
func (h *AdminHandler) HandleInfluxTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var in db.InfluxSettings
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	stored, err := h.db.Settings.GetInflux()
	if err != nil {
		writeJSONError(w, "failed to read metrics export settings", http.StatusInternalServerError)
		return
	}
	if in.URL == "" {
		in.URL = stored.URL
	}
	if in.Bucket == "" {
		in.Bucket = stored.Bucket
	}
	if in.Org == "" {
		in.Org = stored.Org
	}
	if in.Token == "" {
		in.Token = stored.Token
	}
	if in.URL == "" || in.Bucket == "" || in.Token == "" {
		writeJSONError(w, "url, bucket and token are required to test", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	line := "relai_routing_test value=1i " + itoaNano(time.Now())
	if err := influx.New(h.db).Write(ctx, in, line); err != nil {
		writeJSONError(w, "write failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"message": "wrote a test point to bucket " + in.Bucket,
	})
}

func writeInflux(w http.ResponseWriter, cfg db.InfluxSettings) {
	cfg.HasToken = cfg.Token != ""
	cfg.Token = ""
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cfg)
}

func itoaNano(t time.Time) string {
	return strconv.FormatInt(t.UnixNano(), 10)
}
