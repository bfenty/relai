# Relai — LLM Relay Proxy

A self-hosted relay proxy that puts an OpenAI-compatible API in front of your own
fleet of LLM engines (llama.cpp, Halogen flash-server, Ollama, vLLM, or anything
that speaks the OpenAI wire format) — with smart routing, KV-cache-aware
placement, per-key analytics, and a full admin dashboard. Single Go binary,
embedded React frontend, SQLite (or Postgres for request logs), no external
dependencies.

## Features

- **OpenAI-Compatible API** — drop-in replacement for OpenAI client libraries
  (`/v1/chat/completions`, `/v1/models`, streaming, tool calls)
- **Virtual routers** — group backends behind friendly model names and pick a
  placement strategy per router:
  - round-robin, weighted, model-based, cost-based, latency-based
  - **cache-aware sticky** — session affinity plus first placement on a backend
    with observed KV-pool headroom (polls Halogen-compatible `/cache`)
  - **cost-aware spill** — a warm-but-busy session spills to an idle sibling when
    the estimated re-prefill is cheaper than queueing
- **Interactive request priority** — mark latency-critical API keys (voice
  assistants, chat UIs) so their requests carry `X-Halogen-Priority` and claim
  reserved engine slots instead of queueing behind bulk agent work. Client
  headers are stripped; priority is the router's decision, never the caller's.
- **Speculation telemetry** — captures Halogen MTP `draft_n`/`draft_n_accepted`
  counters per request and charts the accept rate, so a degrading drafter is
  visible instead of silently costing decode speed.
- **Engine + router queue visibility** — relai's admission queue and the
  engine-side queue (`queued`, `longest_wait_s`) side by side in the admin UI.
- **Prefix-cache analytics** — why tokens get reprocessed (mutated system
  prompts, evictions, new conversations), attributed per API key.
- **Throughput analytics** — prefill/decode tok/s per host and per bucket,
  token-weighted, with wall-clock aggregates.
- **API key management** — scopes, rate limits, per-key cost attribution,
  leaderboard.
- **Request logging** — every request with token counts, cached tokens, cost,
  latency, wait, prefill/decode split, routing decision, and a per-request
  trace ID in error bodies.
- **Web dashboard** — backends, routers, keys, live queue view, logs, analytics,
  throughput charts, cache telemetry cards.
- **MCP endpoints** — expose configured backends over the Model Context
  Protocol.

## Quick Start

### Docker

```bash
docker run -d \
  -p 8080:8080 \
  -v relai-data:/data \
  -e RELAI_ADMIN_KEY=your-secret-admin-key \
  ghcr.io/YOU/relai:latest
```

### Binary

```bash
export RELAI_ADMIN_KEY=your-secret-admin-key
./relai
```

Then open `http://localhost:8080` and log in with the admin key.

### From source

```bash
cd frontend && npm ci && npm run build   # builds + embeds into internal/server/frontend/dist
cd .. && go build ./... && go test ./...
```

## Connecting a backend

Add a backend (base URL of any OpenAI-compatible engine), then attach it to a
router with a friendly model name. Halogen-compatible engines additionally
expose `/cache` and `/health` telemetry that relai consumes automatically for
cache-aware routing, queue visibility, and speculation charts. Plain
llama.cpp/Ollama backends work too — telemetry-dependent features simply stay
quiet.

## Configuration

See `.env.example` for the full list. Highlights:

| Variable | Purpose |
| --- | --- |
| `RELAI_ADMIN_KEY` | admin bearer key (required) |
| `RELAI_PORT` | listen port (default 8080) |
| `RELAI_LOG_DB_URL` | optional Postgres DSN for request logs |

## Deploying to an LXC

`deploy.sh` builds the binary locally, ships it to a Proxmox host over SSH, and
installs it as a systemd service inside a container (see the script header for
the environment variables it accepts). It is an example deployment path, not
the only one — relai is a single binary and a unit file.

## License

MIT
