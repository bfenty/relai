package server

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/bfenty/relai/internal/config"
	"github.com/bfenty/relai/internal/db"
	"github.com/bfenty/relai/internal/events"
	"github.com/bfenty/relai/internal/hostlimit"
	"github.com/bfenty/relai/internal/influx"
	"github.com/bfenty/relai/internal/mcp"
	"github.com/bfenty/relai/internal/proxy"
	"github.com/bfenty/relai/internal/queue"
	"github.com/bfenty/relai/internal/router"
	"github.com/bfenty/relai/internal/server/handlers"
	relaimw "github.com/bfenty/relai/internal/server/middleware"
	"github.com/bfenty/relai/internal/vrouter"

	"github.com/go-chi/chi/v5"
	chiware "github.com/go-chi/chi/v5/middleware"
)

//go:embed frontend/dist
var frontendFS embed.FS

//go:embed openapi.yaml
var openapiYAML []byte

// Server holds the HTTP server and dependencies.
type Server struct {
	cfg      *config.Config
	db       *db.DB
	bus      *events.Bus
	proxy    *proxy.Proxy
	registry *router.Registry
	vrouter  *vrouter.Manager
	mcp      *mcp.Gateway
	admin    *handlers.AdminHandler
	auth     *handlers.AuthHandler
	openai   *handlers.OpenAIHandler
	ollama   *handlers.OllamaHandler
	router   *chi.Mux
}

// New creates a new server.
func New(cfg *config.Config, d *db.DB) *Server {
	q := queue.NewTracker()
	speeds := queue.NewSpeedLearner()
	bus := events.NewBus()
	p := proxy.New(d, bus, q, proxy.Timeouts{
		ResponseHeader: time.Duration(cfg.StreamResponseHeaderTimeoutSeconds) * time.Second,
		StreamIdle:     time.Duration(cfg.StreamIdleTimeoutSeconds) * time.Second,
	})
	p.SetSpeedLearner(speeds)
	r := router.NewRegistry(d)
	limiter := hostlimit.New()
	vrm := vrouter.NewManager(d, p, limiter)
	authH := handlers.NewAuthHandler(d.Sessions, d.AdminCreds, d.LoginAttempts, cfg.SecureCookies)

	s := &Server{
		cfg:      cfg,
		db:       d,
		bus:      bus,
		proxy:    p,
		registry: r,
		vrouter:  vrm,
		mcp:      mcp.NewGateway(d.MCPServers),
		admin:    handlers.NewAdminHandler(d, vrm, q, speeds),
		auth:     authH,
		openai: handlers.NewOpenAIHandler(d, p, r, vrm, limiter,
			time.Duration(cfg.AdmissionTimeoutSeconds)*time.Second),
	}
	s.ollama = handlers.NewOllamaHandler(s.openai)
	return s
}

// SetupRoutes configures all HTTP routes.
func (s *Server) SetupRoutes() {
	r := chi.NewRouter()
	r.Use(chiware.Logger)
	r.Use(chiware.Recoverer)
	r.Use(relaimw.TraceMiddleware) // X-Trace-Id + error-body enrichment
	r.Use(relaimw.SecurityHeaders)

	// OpenAI-compatible API (requires API key auth or session)
	r.Group(func(r chi.Router) {
		r.Use(relaimw.CORS)
		r.Use(relaimw.MaxBody(32 << 20)) // 32 MiB — request bodies are buffered in full
		r.Use(relaimw.Auth(s.db.APIKeys, s.cfg.AdminKey, s.db.Sessions))
		r.Post("/v1/chat/completions", s.openai.HandleCompletions)
		r.Post("/v1/completions", s.openai.HandleCompletions)
		r.Post("/v1/embeddings", s.openai.HandleEmbeddings)
		r.Post("/v1/rerank", s.openai.HandleRerank)
		r.Post("/v2/embed", s.openai.HandleEmbeddings)
		r.Post("/v2/rerank", s.openai.HandleRerank)
		r.Get("/v1/models", s.openai.HandleModels)
		r.Get("/v1/models/*", s.openai.HandleModelGet)
	})

	// MCP gateway (requires API key auth or session)
	r.Group(func(r chi.Router) {
		r.Use(relaimw.CORS)
		r.Use(relaimw.Auth(s.db.APIKeys, s.cfg.AdminKey, s.db.Sessions))
		r.Handle("/mcp", s.mcp)
		r.Get("/mcp/artifacts/{server}/*", s.mcp.ServeArtifact)
		r.Head("/mcp/artifacts/{server}/*", s.mcp.ServeArtifact)
	})

	// Ollama native API (issue #12): /api/chat, /api/generate, /api/tags,
	// /api/version. These are exact static paths, so chi dispatches them here
	// and never falls through to the AdminAuth /api/* routes below. Same
	// inference auth as /v1 — Ollama clients send rlai_ keys, not admin keys.
	r.Group(func(r chi.Router) {
		r.Use(relaimw.CORS)
		r.Use(relaimw.MaxBody(32 << 20))
		r.Use(relaimw.Auth(s.db.APIKeys, s.cfg.AdminKey, s.db.Sessions))
		r.Post("/api/chat", s.ollama.HandleChat)
		r.Post("/api/generate", s.ollama.HandleGenerate)
		r.Get("/api/tags", s.ollama.HandleTags)
		r.Get("/api/version", s.ollama.HandleVersion)
	})

	// Auth endpoints (no auth required)
	r.With(relaimw.MaxBody(64<<10)).Post("/api/auth/login", s.auth.HandleLogin)
	r.Get("/api/settings/branding", s.admin.HandleBrandingSettings)

	// Auth endpoints (require auth)
	r.Group(func(r chi.Router) {
		r.Use(relaimw.AdminAuth(s.cfg.AdminKey, s.db.Sessions))
		r.Post("/api/auth/logout", s.auth.HandleLogout)
		r.Get("/api/auth/me", s.auth.HandleMe)
	})

	// Admin API (requires admin auth)
	r.Group(func(r chi.Router) {
		r.Use(relaimw.MaxBody(8 << 20)) // 8 MiB — covers config import payloads
		r.Use(relaimw.AdminAuth(s.cfg.AdminKey, s.db.Sessions))
		r.Get("/api/dashboard/stats", s.admin.HandleDashboardStats)
		r.Get("/api/dashboard/stream", s.handleDashboardStream)
		r.Get("/api/analytics/usage", s.admin.HandleAnalyticsUsage)
		r.Get("/api/analytics/routing", s.admin.HandleRoutingAnalytics)
		r.Get("/api/analytics/routing/series", s.admin.HandleRoutingSeries)
		r.Get("/api/analytics/prefix", s.admin.HandlePrefixAnalytics)
		r.Get("/api/analytics/prefix/series", s.admin.HandlePrefixSeries)
		r.Get("/api/queue", s.admin.HandleQueue)
		r.Get("/api/queue/speeds", s.admin.HandleQueueSpeeds)
		r.Get("/api/analytics/throughput/series", s.admin.HandleThroughputSeries)
		r.Get("/api/analytics/cache", s.admin.HandleCacheTelemetry)

		// Backends
		r.Get("/api/backends", s.admin.HandleBackends)
		r.Post("/api/backends", s.admin.HandleBackends)
		r.Get("/api/backends/{id}", s.admin.HandleBackend)
		r.Put("/api/backends/{id}", s.admin.HandleBackend)
		r.Delete("/api/backends/{id}", s.admin.HandleBackend)
		r.Post("/api/backends/{id}/health", s.admin.HandleBackendHealth)
		r.Post("/api/backends/{id}/unarchive", s.admin.HandleBackendUnarchive)

		// API Keys
		r.Get("/api/keys", s.admin.HandleAPIKeys)
		r.Post("/api/keys", s.admin.HandleAPIKeys)
		r.Post("/api/keys/test", s.admin.HandleCreateTestKey)
		r.Post("/api/keys/cleanup", s.admin.HandleCleanupTestKeys)
		r.Get("/api/keys/{id}", s.admin.HandleAPIKey)
		r.Put("/api/keys/{id}", s.admin.HandleAPIKey)
		r.Delete("/api/keys/{id}", s.admin.HandleAPIKey)

		// Routing Rules
		r.Get("/api/routing-rules", s.admin.HandleRoutingRules)
		r.Post("/api/routing-rules", s.admin.HandleRoutingRules)
		r.Get("/api/routing-rules/{id}", s.admin.HandleRoutingRule)
		r.Put("/api/routing-rules/{id}", s.admin.HandleRoutingRule)
		r.Delete("/api/routing-rules/{id}", s.admin.HandleRoutingRule)

		// Model Mappings
		r.Get("/api/model-mappings", s.admin.HandleModelMappings)
		r.Post("/api/model-mappings", s.admin.HandleModelMappings)
		r.Get("/api/model-mappings/{id}", s.admin.HandleModelMapping)
		r.Put("/api/model-mappings/{id}", s.admin.HandleModelMapping)
		r.Delete("/api/model-mappings/{id}", s.admin.HandleModelMapping)
		r.Post("/api/model-mappings/{id}/health", s.admin.HandleModelMapping)

		// Logs
		r.Get("/api/logs", s.admin.HandleLogs)

		// Users
		r.Get("/api/users", s.admin.HandleUsers)
		r.Post("/api/users", s.admin.HandleUsers)
		r.Get("/api/users/{id}", s.admin.HandleUser)
		r.Put("/api/users/{id}", s.admin.HandleUser)
		r.Delete("/api/users/{id}", s.admin.HandleUser)
		r.Get("/api/users/{id}/keys", s.admin.HandleUserKeys)
		r.Post("/api/users/{id}/keys", s.admin.HandleUserKeys)

		// Virtual Routers
		r.Get("/api/routers", s.admin.HandleRouters)
		r.Post("/api/routers", s.admin.HandleRouters)
		r.Get("/api/routers/{id}", s.admin.HandleRouter)
		r.Put("/api/routers/{id}", s.admin.HandleRouter)
		r.Delete("/api/routers/{id}", s.admin.HandleRouter)
		r.Get("/api/routers/{id}/members", s.admin.HandleRouterMembers)
		r.Post("/api/routers/{id}/members", s.admin.HandleRouterMembers)
		r.Delete("/api/routers/{id}/members/{memberID}", s.admin.HandleRouterMember)
		r.Get("/api/routers/{id}/status", s.admin.HandleRouterStatus)

		// MCP Servers
		r.Get("/api/mcp-servers", s.admin.HandleMCPServers)
		r.Post("/api/mcp-servers", s.admin.HandleMCPServers)
		r.Get("/api/mcp-servers/{id}", s.admin.HandleMCPServer)
		r.Put("/api/mcp-servers/{id}", s.admin.HandleMCPServer)
		r.Delete("/api/mcp-servers/{id}", s.admin.HandleMCPServer)

		// System Events
		r.Get("/api/events", s.admin.HandleEvents)

		// Config export/import
		r.Get("/api/config/export", s.admin.HandleConfigExport)
		r.Post("/api/config/import", s.admin.HandleConfigImport)
		r.Put("/api/settings/branding", s.admin.HandleBrandingSettings)
		r.Get("/api/settings/storage", s.admin.HandleStorageDiagnostics)
		r.Get("/api/settings/influx", s.admin.HandleInfluxSettings)
		r.Put("/api/settings/influx", s.admin.HandleInfluxSettings)
		r.Post("/api/settings/influx/test", s.admin.HandleInfluxTest)

		// Metrics (Prometheus format)
		r.Get("/metrics", s.admin.HandleMetrics)
	})

	// Health check and spec (no auth)
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	r.Get("/ready", s.handleReady)
	r.Get("/api/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		_, _ = w.Write(openapiYAML)
	})

	// Serve frontend
	s.serveFrontend(r)

	s.router = r
}

// serveFrontend serves the React frontend with SPA fallback.
func (s *Server) serveFrontend(r chi.Router) {
	files, err := fs.Sub(frontendFS, "frontend/dist")
	if err != nil {
		log.Printf("Warning: no frontend embedded: %v", err)
		r.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/v1/") && !strings.HasPrefix(r.URL.Path, "/v2/") && !strings.HasPrefix(r.URL.Path, "/mcp") {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprintf(w, `<html><head><title>Relai</title></head><body><h1>Relai</h1><p>Frontend not built yet.</p></body></html>`)
			}
		})
		return
	}

	fileServer := http.FileServer(http.FS(files))

	r.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/v1/") || strings.HasPrefix(path, "/v2/") || strings.HasPrefix(path, "/mcp") || strings.HasPrefix(path, "/health") || strings.HasPrefix(path, "/ready") {
			http.NotFound(w, r)
			return
		}

		assetPath := strings.TrimPrefix(filepath.Clean(path), "/")
		if assetPath == "." {
			assetPath = "index.html"
		}

		f, err := files.Open(assetPath)
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		http.ServeFileFS(w, r, files, "index.html")
	})
}

type readinessResponse struct {
	Ready  bool                      `json:"ready"`
	Checks map[string]readinessCheck `json:"checks"`
}

type readinessCheck struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	resp := readinessResponse{
		Ready:  true,
		Checks: make(map[string]readinessCheck),
	}
	addCheck := func(name string, ok bool, message string) {
		resp.Checks[name] = readinessCheck{OK: ok, Message: message}
		if !ok {
			resp.Ready = false
		}
	}

	if err := s.db.Ping(); err != nil {
		addCheck("database", false, err.Error())
	} else {
		addCheck("database", true, "ok")
	}

	if err := s.db.PingLogs(); err != nil {
		addCheck("request_logs", false, err.Error())
	} else if s.cfg.LogDBURL != "" {
		addCheck("request_logs", true, "postgres")
	} else {
		addCheck("request_logs", true, "sqlite")
	}

	if s.cfg.ReadyRequireModels {
		count, err := s.db.ActiveModelTargetCount()
		if err != nil {
			addCheck("model_targets", false, err.Error())
		} else if count == 0 {
			addCheck("model_targets", false, "no active model targets")
		} else {
			addCheck("model_targets", true, fmt.Sprintf("%d active", count))
		}

		unreadyRouters, err := s.db.UnreadyRouterNames()
		if err != nil {
			addCheck("routers", false, err.Error())
		} else if len(unreadyRouters) > 0 {
			addCheck("routers", false, "unready: "+strings.Join(unreadyRouters, ", "))
		} else {
			addCheck("routers", true, "ok")
		}
	}

	status := http.StatusOK
	if !resp.Ready {
		status = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

// Router returns the chi router.
func (s *Server) Router() http.Handler {
	return s.router
}

// handleDashboardStream sends stat delta events via SSE.
func (s *Server) handleDashboardStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	errorsOnly := r.URL.Query().Get("errors") == "true"

	ch, unsubscribe := s.bus.Subscribe()
	defer unsubscribe()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if errorsOnly && ev.Type != "error" {
				continue
			}
			_, err := w.Write([]byte(events.FormatSSE(ev)))
			if err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// Run starts the HTTP server.
func (s *Server) Run() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	log.Printf("Starting Relai on %s", addr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.vrouter.Start(ctx)

	// Optional metrics export. Reads its own configuration each tick, so it is
	// inert until enabled and needs no restart to retarget.
	go influx.New(s.db).Run(ctx)

	// Cleanup expired test keys on startup
	go func() {
		deleted, err := s.db.APIKeys.CleanupExpiredTestKeys()
		if err != nil {
			log.Printf("Warning: test key cleanup failed: %v", err)
		} else if deleted > 0 {
			log.Printf("Cleaned up %d expired test keys", deleted)
		}
	}()

	// Run cleanup every 2 hours
	go func() {
		ticker := time.NewTicker(2 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				deleted, err := s.db.APIKeys.CleanupExpiredTestKeys()
				if err != nil {
					log.Printf("Warning: test key cleanup failed: %v", err)
				} else if deleted > 0 {
					log.Printf("Cleaned up %d expired test keys", deleted)
				}
			}
		}
	}()

	srv := &http.Server{
		Addr:    addr,
		Handler: s.Router(),
		// No global write/read timeout: completions stream for minutes. The
		// header timeout still protects against slowloris-style connections.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return srv.ListenAndServe()
}
