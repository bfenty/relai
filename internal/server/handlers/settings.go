package handlers

import (
	"encoding/json"
	"net/http"
	"path/filepath"

	"github.com/bfenty/relai/internal/db"
)

func (h *AdminHandler) HandleBrandingSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		branding, err := h.db.Settings.GetBranding()
		if err != nil {
			branding = db.DefaultBrandingSettings()
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(branding) //nolint
	case http.MethodPut:
		var input db.BrandingSettings
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeJSONError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		branding, err := h.db.Settings.UpdateBranding(input)
		if err != nil {
			writeJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.db.SystemEvents.Log(db.LevelInfo, db.EventConfig, "branding settings updated", map[string]any{
			"app_name":      branding.AppName,
			"has_logo":      branding.LogoURL != "",
			"primary_color": branding.PrimaryColor,
			"accent_color":  branding.AccentColor,
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(branding) //nolint
	default:
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type StorageDiagnostics struct {
	SQLitePath          string   `json:"sqlite_path"`
	SQLitePathAbsolute  bool     `json:"sqlite_path_absolute"`
	LogBackend          string   `json:"log_backend"`
	RequestLogRows      int64    `json:"request_log_rows"`
	LatestRequestLogAt  string   `json:"latest_request_log_at,omitempty"`
	PostgresLogsEnabled bool     `json:"postgres_logs_enabled"`
	Warnings            []string `json:"warnings"`
}

func (h *AdminHandler) HandleStorageDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	diag := StorageDiagnostics{
		SQLitePath:         h.db.DBPath(),
		SQLitePathAbsolute: filepath.IsAbs(h.db.DBPath()),
		LogBackend:         h.db.Logs.Dialect(),
	}
	diag.PostgresLogsEnabled = diag.LogBackend == "postgres"
	if !diag.SQLitePathAbsolute {
		diag.Warnings = append(diag.Warnings, "SQLite app-state path is relative; use an absolute persistent path in deployment.")
	}
	if !diag.PostgresLogsEnabled {
		diag.Warnings = append(diag.Warnings, "Request logs are using SQLite; set RELAI_LOG_DB_URL for production PostgreSQL usage logging.")
	}

	count, latest, err := h.db.Logs.CountAndLatest()
	if err != nil {
		diag.Warnings = append(diag.Warnings, "Failed to inspect request logs: "+err.Error())
	} else {
		diag.RequestLogRows = count
		if latest != nil {
			diag.LatestRequestLogAt = latest.Format("2006-01-02T15:04:05Z07:00")
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(diag) //nolint
}
