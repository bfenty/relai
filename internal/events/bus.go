package events

import (
	"encoding/json"
	"sync"
)

// StatDelta holds per-request changes pushed to the Dashboard.
type StatDelta struct {
	Requests int64   `json:"requests"`
	Cost     float64 `json:"cost"`
	Tokens   int64   `json:"tokens"`
	Latency  float64 `json:"latency"`
	Errors   int64   `json:"errors"`
}

// ErrorLogEntry holds a live error log push.
type ErrorLogEntry struct {
	Model        string `json:"model"`
	ErrorMessage string `json:"error_message"`
	StatusCode   int    `json:"status_code"`
	CreatedAt    string `json:"created_at"`
}

// Event pushes stat deltas or error logs to subscribed clients over SSE.
type Event struct {
	Type  string         `json:"type"`
	Delta StatDelta      `json:"delta,omitempty"`
	Error *ErrorLogEntry `json:"error,omitempty"`
}

// Bus is an in-memory pub/sub for dashboard stat updates.
type Bus struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

// NewBus creates a new event bus.
func NewBus() *Bus {
	return &Bus{subs: make(map[chan Event]struct{})}
}

// Subscribe returns a channel that receives stat delta events.
// Returns a close function to unsubscribe.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	b.mu.Lock()
	ch := make(chan Event, 16)
	b.subs[ch] = struct{}{}
	b.mu.Unlock()

	return ch, func() {
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
		close(ch)
	}
}

// PublishStat sends a stat delta to all subscribers.
func (b *Bus) PublishStat(delta StatDelta) {
	b.publish(Event{Type: "stat", Delta: delta})
}

// PublishError sends an error log entry to all subscribers.
func (b *Bus) PublishError(entry ErrorLogEntry) {
	b.publish(Event{Type: "error", Error: &entry})
}

// publish dispatches to all subscribers.
func (b *Bus) publish(ev Event) {
	b.mu.RLock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
			// drop if subscriber is slow
		}
	}
	b.mu.RUnlock()
}

// FormatSSE formats an event as an SSE message string. The data payload is
// the bare delta or error entry (not the Event envelope) — the dashboard
// client parses event.data directly into that shape.
func FormatSSE(ev Event) string {
	var data []byte
	switch {
	case ev.Type == "error" && ev.Error != nil:
		data, _ = json.Marshal(ev.Error)
	default:
		data, _ = json.Marshal(ev.Delta)
	}
	return "event: " + ev.Type + "\n" + "data: " + string(data) + "\n\n"
}
