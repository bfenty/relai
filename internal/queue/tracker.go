package queue

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// Phase constants — no prompt or response content is ever stored.
const (
	PhaseQueued     = "queued"
	PhaseProcessing = "processing_prompt"
	PhaseThinking   = "thinking"
	PhaseStreaming  = "streaming"
)

// Entry is a content-free snapshot of one in-flight request.
type Entry struct {
	ID          string `json:"id"`
	BackendID   int64  `json:"backend_id"`
	BackendName string `json:"backend_name"`
	Model       string `json:"model"`
	Status      string `json:"status"`
	ClientKey   string `json:"client_key,omitempty"`
	ElapsedMs   int64  `json:"elapsed_ms"`
	PhaseMs     int64  `json:"phase_ms"`

	// Live throughput, computed at snapshot time so the values never go stale.
	// PromptTokS is estimated (prompt size comes from the forwarded request body,
	// ~4 bytes/token) divided by the measured prefill duration; it is 0 until the
	// prefill phase completes. DecodeTokS is measured: counted stream deltas over
	// the time since the first token.
	PromptTokS float64 `json:"prompt_tok_s"`
	DecodeTokS float64 `json:"decode_tok_s"`
	PromptMs   int64   `json:"prompt_ms"`
	// EstPromptTokens is the body-size estimate of the prompt; 0 when the request
	// was not a chat completion we could size. When the backend reports real
	// prompt progress this holds the engine's exact total instead.
	EstPromptTokens int `json:"est_prompt_tokens"`
	// GeneratedTokens counts streamed content/reasoning deltas so far.
	GeneratedTokens int `json:"generated_tokens"`
	// Real prefill progress, reported by the engine (Halogen's prompt_progress
	// stream events). Non-zero PromptTotal means the bar is engine-truthful,
	// not an estimate: Processed/Total tokens actually evaluated, Cached served
	// from KV, EngineMs of host time spent so far.
	PromptTotal     int `json:"prompt_total"`
	PromptProcessed int `json:"prompt_processed"`
	PromptCached    int `json:"prompt_cached"`
	PromptEngineMs  int `json:"prompt_engine_ms"`
}

// Update carries live per-request counters from the proxy's SSE reader.
type Update struct {
	// PromptMs is the measured prefill duration, set once the phase leaves
	// processing_prompt.
	PromptMs int64
	// EstPromptTokens is the estimated prompt size from the request body.
	EstPromptTokens int
	// FirstTokenAt is when the first content/reasoning delta was seen.
	FirstTokenAt time.Time
	// GeneratedTokens is the running count of streamed deltas.
	GeneratedTokens int
	// EstPromptTokensOk marks the estimate as usable (chat requests only).
	EstPromptTokensOk bool
	// Real prompt progress from the engine. PromptTotal > 0 marks the request
	// as engine-reported; the fields overwrite the estimate wholesale.
	PromptTotal     int
	PromptProcessed int
	PromptCached    int
	PromptEngineMs  int
}

type live struct {
	Entry
	startedAt time.Time
	phaseAt   time.Time

	// throughput inputs, guarded by the Tracker mutex
	promptMs        int64
	estPromptTokens int
	estPromptOK     bool
	firstTokenAt    time.Time
	generated       int

	// engine-reported prompt progress (Halogen prompt_progress events)
	promptTotal     int
	promptProcessed int
	promptCached    int
	promptEngineMs  int
}

// Tracker is a thread-safe registry of active proxy requests.
type Tracker struct {
	mu      sync.RWMutex
	entries map[string]*live
}

func NewTracker() *Tracker {
	return &Tracker{entries: make(map[string]*live)}
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Add registers an in-flight request and returns its tracking ID.
func (t *Tracker) Add(backendID int64, backendName, model string) string {
	return t.add(backendID, backendName, model, "", PhaseProcessing)
}

// AddQueued registers a request waiting for Relai admission before proxying.
func (t *Tracker) AddQueued(routerName, model, clientKey string) string {
	return t.add(0, routerName, model, clientKey, PhaseQueued)
}

func (t *Tracker) add(backendID int64, backendName, model, clientKey, status string) string {
	id := newID()
	now := time.Now()
	t.mu.Lock()
	t.entries[id] = &live{
		Entry:     Entry{ID: id, BackendID: backendID, BackendName: backendName, Model: model, Status: status, ClientKey: clientKey},
		startedAt: now,
		phaseAt:   now,
	}
	t.mu.Unlock()
	return id
}

// Transition moves an entry to a new phase.
func (t *Tracker) Transition(id, status string) {
	t.mu.Lock()
	if e, ok := t.entries[id]; ok {
		e.Status = status
		e.phaseAt = time.Now()
	}
	t.mu.Unlock()
}

// Update merges live counters into an entry. Missing fields are left alone, and
// updates for removed entries are dropped (the request finished mid-chunk).
func (t *Tracker) Update(id string, u Update) {
	t.mu.Lock()
	if e, ok := t.entries[id]; ok {
		if u.PromptMs > 0 {
			e.promptMs = u.PromptMs
		}
		if u.EstPromptTokensOk {
			e.estPromptTokens = u.EstPromptTokens
			e.estPromptOK = true
		}
		if !u.FirstTokenAt.IsZero() && e.firstTokenAt.IsZero() {
			e.firstTokenAt = u.FirstTokenAt
		}
		if u.GeneratedTokens > 0 {
			e.generated = u.GeneratedTokens
		}
		if u.PromptTotal > 0 {
			e.promptTotal = u.PromptTotal
			e.promptProcessed = u.PromptProcessed
			e.promptCached = u.PromptCached
			e.promptEngineMs = u.PromptEngineMs
			// The engine's total replaces the body-size guess everywhere.
			e.estPromptTokens = u.PromptTotal
			e.estPromptOK = true
		}
	}
	t.mu.Unlock()
}

// Remove deletes a completed entry.
func (t *Tracker) Remove(id string) {
	t.mu.Lock()
	delete(t.entries, id)
	t.mu.Unlock()
}

// Snapshot returns a point-in-time copy of all active entries with throughput
// rates computed against the current instant.
func (t *Tracker) Snapshot() []Entry {
	now := time.Now()
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]Entry, 0, len(t.entries))
	for _, e := range t.entries {
		snap := e.Entry
		snap.ElapsedMs = now.Sub(e.startedAt).Milliseconds()
		snap.PhaseMs = now.Sub(e.phaseAt).Milliseconds()

		snap.PromptMs = e.promptMs
		snap.EstPromptTokens = e.estPromptTokens
		snap.GeneratedTokens = e.generated
		snap.PromptTotal = e.promptTotal
		snap.PromptProcessed = e.promptProcessed
		snap.PromptCached = e.promptCached
		snap.PromptEngineMs = e.promptEngineMs
		if e.promptTotal > 0 && e.promptEngineMs > 0 && e.promptProcessed > 0 {
			// Engine-measured prefill rate: tokens evaluated (cache hits were
			// never evaluated) over host time.
			snap.PromptTokS = float64(e.promptProcessed) / (float64(e.promptEngineMs) / 1000.0)
		} else if e.promptMs > 0 && e.estPromptOK && e.estPromptTokens > 0 {
			snap.PromptTokS = float64(e.estPromptTokens) / (float64(e.promptMs) / 1000.0)
		}
		if !e.firstTokenAt.IsZero() && e.generated > 0 {
			if secs := now.Sub(e.firstTokenAt).Seconds(); secs > 0 {
				snap.DecodeTokS = float64(e.generated) / secs
			}
		}
		out = append(out, snap)
	}
	return out
}
