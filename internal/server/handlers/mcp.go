package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/bfenty/relai/internal/db"
	chi "github.com/go-chi/chi/v5"
)

// HandleMCPServers handles GET/POST /api/mcp-servers.

// redactMCPServerKey hides the stored provider key in API responses; clients
// see only whether one is set (has_api_key). Updates with an empty api_key
// preserve the stored key, so redacted values round-trip safely.
func redactMCPServerKey(s *db.MCPServer) {
	s.HasAPIKey = s.APIKey != ""
	s.APIKey = ""
}

func redactMCPServerKeys(servers []db.MCPServer) {
	for i := range servers {
		redactMCPServerKey(&servers[i])
	}
}

func (h *AdminHandler) HandleMCPServers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		servers, err := h.db.MCPServers.List()
		if err != nil {
			writeJSONError(w, "failed to list MCP servers", http.StatusInternalServerError)
			return
		}
		if servers == nil {
			servers = []db.MCPServer{}
		}
		redactMCPServerKeys(servers)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(servers)

	case http.MethodPost:
		var s db.MCPServer
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			writeJSONError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		if s.Transport == "" {
			s.Transport = "http"
		}
		created, err := h.db.MCPServers.Create(&s)
		if err != nil {
			writeJSONError(w, err.Error(), http.StatusBadRequest)
			return
		}
		h.db.SystemEvents.Log(db.LevelInfo, db.EventConfig, "MCP server created: "+s.Name, nil)
		redactMCPServerKey(created)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(created)

	default:
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// HandleMCPServer handles GET/PUT/DELETE /api/mcp-servers/:id.
func (h *AdminHandler) HandleMCPServer(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(chi.URLParam(r, "id"))
	if err != nil {
		writeJSONError(w, "invalid ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s, err := h.db.MCPServers.Get(id)
		if err != nil {
			writeJSONError(w, "MCP server not found", http.StatusNotFound)
			return
		}
		redactMCPServerKey(s)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s)

	case http.MethodPut:
		var input db.MCPServer
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeJSONError(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		updated, err := h.db.MCPServers.Update(id, &input)
		if err != nil {
			writeJSONError(w, err.Error(), http.StatusBadRequest)
			return
		}
		h.db.SystemEvents.Log(db.LevelInfo, db.EventConfig, "MCP server updated: "+input.Name, nil)
		redactMCPServerKey(updated)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(updated)

	case http.MethodDelete:
		s, _ := h.db.MCPServers.Get(id)
		if err := h.db.MCPServers.Delete(id); err != nil {
			writeJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if s != nil {
			h.db.SystemEvents.Log(db.LevelWarn, db.EventConfig, "MCP server deleted: "+s.Name, nil)
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
