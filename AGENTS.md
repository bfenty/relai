# AGENTS.md

Relai is a single Go binary that embeds a React frontend and uses SQLite for persistence. No external services required.

## Build

Frontend **must** build before the Go binary — the Go binary embeds `internal/server/frontend/dist/`.

```bash
# Full build
cd frontend && npm install && npm run build && cd ..
go build -o relai ./cmd/relai

# Run
RELAI_ADMIN_KEY=your-key ./relai
```

The `npm run build` script runs `tsc && vite build && cp -r dist/* ../internal/server/frontend/dist/`. Running `vite build` alone skips the copy step and the Go binary will have no frontend.

## Frontend Dev

```bash
cd frontend && npm run dev   # Vite dev server, proxied to Go backend at :8080
npm run lint
```

## Test

```bash
go test ./...                # All tests, in-memory SQLite, no prerequisites
go test ./internal/db/...    # Specific package
go test ./internal/router/... -run TestRoundRobin  # Single test
```

## Architecture

- **Entry**: `cmd/relai/main.go` → `server.New()` → `SetupRoutes()` → `Run()`
- **Embed**: `//go:embed frontend/dist` in `internal/server/server.go`
- **Router**: chi (`go-chi/chi/v5`), routes in `internal/server/server.go`
- **DB**: pure-Go SQLite (`modernc.org/sqlite`), repos in `internal/db`
- **Config**: env vars — `RELAI_PORT`, `RELAI_HOST`, `RELAI_DB_PATH`, `RELAI_ADMIN_KEY`, `RELAI_SECURE_COOKIES`

### Key Packages

| Package | Purpose |
|---|---|
| `internal/config` | Env var configuration |
| `internal/db` | SQLite repos (Backends, APIKeys, Routing, ModelMaps, Logs, Sessions, Users, AdminCreds) |
| `internal/router` | Routing strategies: round_robin, weighted, model_based, cost_based, latency_based |
| `internal/vrouter` | Virtual router manager (groups of backends) |
| `internal/queue` | Request queue tracker |
| `internal/proxy` | HTTP forwarding, SSE streaming, token counting |
| `internal/server` | chi router wiring, embedded frontend, SPA fallback |
| `internal/server/handlers` | AdminHandler, AuthHandler, OpenAIHandler |
| `internal/server/middleware` | Auth (API key), AdminAuth (admin key + session), CORS |
| `pkg/openai/types` | Shared OpenAI request/response structs |

### Request Flow

1. `/v1/chat/completions` → `Auth` middleware validates API key (prefixed `rlai_`) or session
2. `OpenAIHandler.HandleCompletions` extracts model name
3. `router.Registry.SelectBackend` evaluates routing rules by priority, falls back to round-robin
4. `proxy.Proxy.ProxyRequest` forwards request, rewrites model name via mappings, streams SSE back
5. Token counts and latency logged to `request_logs`

### Gotchas

- **AdminAuth is enabled** on all `/api/*` routes (not commented out)
- **API keys** are stored hashed in SQLite, client-visible prefix is `rlai_`
- **Model mappings** translate local → remote model names, scoped per backend
- **Frontend build target** is `internal/server/frontend/dist/`, not `frontend/dist/` (though both exist)
