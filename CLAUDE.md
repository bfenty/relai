# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build Commands

```bash
# Build frontend (must run before building Go binary)
cd frontend && npm install && npm run build && cd ..

# Build Go binary (embeds the built frontend)
go build -o relai ./cmd/relai

# Run
RELAI_ADMIN_KEY=your-key ./relai

# Docker
docker build -t relai .
docker compose up
```

## Testing

```bash
# Run all tests
go test ./...

# Run a specific package's tests
go test ./internal/db/...
go test ./internal/router/...

# Run a single test
go test ./internal/router/... -run TestRoundRobin
```

Tests use in-memory SQLite (`:memory:`) — no external dependencies needed.

## Frontend Development

```bash
cd frontend
npm install
npm run dev    # Vite dev server (proxied to Go backend at :8080)
npm run lint
```

The frontend build output goes to `frontend/dist/` and is **also copied** to `internal/server/frontend/dist/` by `npm run build`. The Go binary embeds the `internal/server/frontend/dist` directory via `//go:embed`.

## Architecture

Relai is a single Go binary that embeds a React frontend and uses SQLite for persistence. There are no external service dependencies.

### Request Flow

1. Client sends OpenAI-compatible request to `/v1/chat/completions`
2. `middleware.Auth` validates the API key (hashed in SQLite, prefixed `rlai_`) and checks rate limits
3. `handlers.OpenAIHandler.HandleCompletions` extracts the model name from the request body
4. `router.Registry.SelectBackend` evaluates active routing rules by priority, falling back to round-robin
5. `proxy.Proxy.ProxyRequest` forwards the request to the selected backend, optionally rewriting the model name via model mappings, and streams the response back
6. Token counts and latency are logged to `request_logs`

### Key Packages

- **`internal/config`** — env var config (`RELAI_PORT`, `RELAI_HOST`, `RELAI_DB_PATH`, `RELAI_ADMIN_KEY`)
- **`internal/db`** — SQLite repositories (`BackendRepo`, `APIKeyRepo`, `RoutingRepo`, `ModelMapRepo`, `LogRepo`); schema migrations run on startup
- **`internal/router`** — `Registry` holds five `Router` implementations (`round_robin`, `weighted`, `model_based`, `cost_based`, `latency_based`); routing rules stored in DB are evaluated by priority with glob model matching
- **`internal/proxy`** — forwards HTTP requests to backends, handles SSE streaming, logs token counts; `StreamingReader` wraps the response body to parse SSE events
- **`internal/server`** — `chi` router wiring; embeds the frontend via `//go:embed frontend/dist`; SPA fallback serves `index.html` for non-API routes
- **`internal/server/handlers`** — `AdminHandler` (CRUD for backends/keys/rules/mappings/logs), `OpenAIHandler` (completions/embeddings/models)
- **`internal/server/middleware`** — `Auth` (API key), `AdminAuth` (admin key, currently commented out in routes), `CORS`
- **`pkg/openai/types`** — shared OpenAI request/response structs

### Model Mappings

A model mapping translates a "local" model name (what the client sends, e.g. `gpt-4`) to a "remote" model name (what the backend expects, e.g. `gpt-4-0613`), scoped to a specific backend. The proxy rewrites the request body when a mapping exists.

### Admin Auth Note

`AdminAuth` middleware exists but is **commented out** in `server.go` (line 75). Admin endpoints are currently unprotected unless re-enabled.

### Frontend Stack

React 18 + TypeScript + Vite + Tailwind CSS + TanStack Query + Recharts + dnd-kit. Pages: Dashboard, Backends, API Keys, Routing, Model Mappings, Logs. API calls go through `frontend/src/api/client.ts`.
