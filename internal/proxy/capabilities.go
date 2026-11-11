package proxy

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Backend capability cache.
//
// Halogen (0.17.5+) answers GET /health with a `version` object and accepts
// `return_progress: true` on streamed requests, which makes the queue view's
// prefill bar engine-truthful instead of an EWMA guess. Detection is a one-shot
// probe per backend base URL: a `version` block on /health is a Halogen-specific
// shape that plain llama.cpp-style servers do not emit, so no config flag or
// admin toggle is needed. Negative results are cached too — a probe failure
// simply leaves the estimated bar in place.

type capabilityEntry struct {
	promptProgress bool
	contextLength  int // 0 = unknown
	expires        time.Time
}

// capabilityTTL bounds how long a probe answer is trusted. A backend upgraded
// mid-deployment is picked up at the next expiry without a restart.
const capabilityTTL = 30 * time.Minute

// probeTimeout is short on purpose: the probe runs inline before the first
// request to a cold backend, and a dead host must not add handshake latency.
const probeTimeout = 2 * time.Second

func (p *Proxy) supportsPromptProgress(baseURL string) bool {
	return p.capabilities(baseURL).promptProgress
}

// ContextLength reports the context window a backend advertises, or 0 when the
// backend does not report one (plain llama.cpp without /props, dead host,
// external provider). Probed through the same cached capability entry as
// prompt-progress detection, so a backend costs at most one probe per TTL.
func (p *Proxy) ContextLength(baseURL string) int {
	return p.capabilities(baseURL).contextLength
}

func (p *Proxy) capabilities(baseURL string) capabilityEntry {
	key := strings.TrimRight(baseURL, "/")
	if key == "" {
		return capabilityEntry{}
	}
	now := time.Now()
	p.capsMu.Lock()
	if e, ok := p.caps[key]; ok && now.Before(e.expires) {
		p.capsMu.Unlock()
		return e
	}
	p.capsMu.Unlock()

	e := probeBackend(p.client, key)
	e.expires = now.Add(capabilityTTL)

	p.capsMu.Lock()
	if p.caps == nil {
		p.caps = make(map[string]capabilityEntry)
	}
	p.caps[key] = e
	p.capsMu.Unlock()
	return e
}

// probeBackend fetches /health once and derives both capability facts from it:
// the Halogen version block (prompt progress) and the advertised context. A
// non-Halogen server that answers /props (llama.cpp) contributes its n_ctx.
// Any error, timeout, or non-200 counts as "no information" — the conservative
// answer keeps the old behavior.
func probeBackend(client *http.Client, baseURL string) capabilityEntry {
	req, err := http.NewRequest(http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return capabilityEntry{}
	}
	probe := *client
	probe.Timeout = probeTimeout
	resp, err := probe.Do(req)
	if err != nil {
		return capabilityEntry{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return entryFromProps(&probe, baseURL)
	}
	var body struct {
		Version map[string]any `json:"version"`
		Context int            `json:"context"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return capabilityEntry{}
	}
	return capabilityEntry{
		promptProgress: len(body.Version) > 0,
		contextLength:  positiveOr(body.Context, 0),
	}
}

// entryFromProps is the llama.cpp fallback: GET /props carries n_ctx.
func entryFromProps(probe *http.Client, baseURL string) capabilityEntry {
	req, err := http.NewRequest(http.MethodGet, baseURL+"/props", nil)
	if err != nil {
		return capabilityEntry{}
	}
	resp, err := probe.Do(req)
	if err != nil {
		return capabilityEntry{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return capabilityEntry{}
	}
	var body struct {
		NCtx int `json:"n_ctx"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return capabilityEntry{}
	}
	return capabilityEntry{contextLength: body.NCtx}
}

func positiveOr(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}
