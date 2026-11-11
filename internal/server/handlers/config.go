package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/bfenty/relai/internal/db"
)

type configExport struct {
	Version       string              `json:"version"`
	ExportedAt    time.Time           `json:"exported_at"`
	Branding      db.BrandingSettings `json:"branding"`
	Backends      []db.Backend        `json:"backends"`
	ModelMaps     []db.ModelMap       `json:"model_mappings"`
	Routers       []db.Router         `json:"routers"`
	RouterMembers []db.RouterMember   `json:"router_members"`
	RoutingRules  []db.RoutingRule    `json:"routing_rules"`
	MCPServers    []db.MCPServer      `json:"mcp_servers"`
}

// HandleConfigExport returns a full config snapshot as JSON.
// Sensitive fields (backend API keys, MCP API keys) are omitted.
func (h *AdminHandler) HandleConfigExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	backends, _ := h.db.Backends.List()
	for i := range backends {
		backends[i].APIKey = "" // strip secrets
	}

	modelMaps, _ := h.db.ModelMaps.List()
	routers, _ := h.db.Routers.List()
	routingRules, _ := h.db.Routing.List()
	branding, _ := h.db.Settings.GetBranding()
	mcpServers, _ := h.db.MCPServers.List()
	for i := range mcpServers {
		mcpServers[i].APIKey = "" // strip secrets
	}

	// Collect all router members
	var allMembers []db.RouterMember
	for _, router := range routers {
		members, err := h.db.Routers.GetMembers(router.ID)
		if err == nil {
			allMembers = append(allMembers, members...)
		}
	}

	export := configExport{
		Version:       "1",
		ExportedAt:    time.Now().UTC(),
		Branding:      branding,
		Backends:      orEmpty(backends),
		ModelMaps:     orEmptyMM(modelMaps),
		Routers:       orEmptyR(routers),
		RouterMembers: orEmptyRM(allMembers),
		RoutingRules:  orEmptyRR(routingRules),
		MCPServers:    orEmptyMCP(mcpServers),
	}

	h.db.SystemEvents.Log(db.LevelInfo, db.EventConfig, "Config exported", nil)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="relai-config.json"`)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(export)
}

// HandleConfigImport ingests a config export and upserts all items by name.
// Existing items are updated; new items are created. Secrets are preserved
// for items that already exist (since the export strips them).
func (h *AdminHandler) HandleConfigImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var cfg configExport
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeJSONError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if cfg.Version == "" {
		writeJSONError(w, "missing version field — is this a valid relai config export?", http.StatusBadRequest)
		return
	}

	summary := map[string]int{}

	if cfg.Branding.AppName != "" || cfg.Branding.LogoURL != "" || cfg.Branding.PrimaryColor != "" || cfg.Branding.AccentColor != "" {
		if _, err := h.db.Settings.UpdateBranding(cfg.Branding); err == nil {
			summary["branding"] = 1
		}
	}

	// Backends — track old_id→new_id so model map foreign keys can be remapped
	backendIDMap := map[int64]int64{}
	for _, b := range cfg.Backends {
		upserted, err := h.db.Backends.UpsertByName(b)
		if err == nil {
			backendIDMap[b.ID] = upserted.ID
			summary["backends"]++
		}
	}

	// Model mappings — remap backend_id from export to local ID before upserting
	for _, mm := range cfg.ModelMaps {
		newID, ok := backendIDMap[mm.BackendID]
		if !ok {
			continue
		}
		mm.BackendID = newID
		if _, err := h.db.ModelMaps.UpsertByKey(mm); err == nil {
			summary["model_mappings"]++
		}
	}

	// Routing rules
	for _, rr := range cfg.RoutingRules {
		if _, err := h.db.Routing.UpsertByName(rr); err == nil {
			summary["routing_rules"]++
		}
	}

	// Routers
	for _, router := range cfg.Routers {
		if _, err := h.db.Routers.UpsertByName(router); err == nil {
			summary["routers"]++
		}
	}

	// MCP servers
	for _, mcp := range cfg.MCPServers {
		if _, err := h.db.MCPServers.UpsertByName(&mcp); err == nil {
			summary["mcp_servers"]++
		}
	}

	h.db.SystemEvents.Log(db.LevelInfo, db.EventConfig, "Config imported", map[string]any{"summary": summary})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"imported": summary})
}

// ── slice-or-empty helpers ────────────────────────────────────────────────────

func orEmpty(s []db.Backend) []db.Backend {
	if s == nil {
		return []db.Backend{}
	}
	return s
}
func orEmptyMM(s []db.ModelMap) []db.ModelMap {
	if s == nil {
		return []db.ModelMap{}
	}
	return s
}
func orEmptyR(s []db.Router) []db.Router {
	if s == nil {
		return []db.Router{}
	}
	return s
}
func orEmptyRM(s []db.RouterMember) []db.RouterMember {
	if s == nil {
		return []db.RouterMember{}
	}
	return s
}
func orEmptyRR(s []db.RoutingRule) []db.RoutingRule {
	if s == nil {
		return []db.RoutingRule{}
	}
	return s
}
func orEmptyMCP(s []db.MCPServer) []db.MCPServer {
	if s == nil {
		return []db.MCPServer{}
	}
	return s
}
