// Package thinking implements the client side of the adaptive-thinking
// classifier sidecar (issue #10, epic #9).
//
// The classifier is ADVISORY and never load-bearing: any timeout, transport
// error, non-200, malformed response, or below-floor confidence resolves to the
// router's default level and the request proceeds untouched. A sidecar outage
// must never turn into a proxy outage.
package thinking

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Levels are the effort classes the sidecar may return. They match the
// sidecar contract in the epic: "off"|"low"|"medium"|"high".
const (
	LevelOff    = "off"
	LevelLow    = "low"
	LevelMedium = "medium"
	LevelHigh   = "high"
)

// ValidLevel reports whether s is one of the four effort classes.
func ValidLevel(s string) bool {
	switch s {
	case LevelOff, LevelLow, LevelMedium, LevelHigh:
		return true
	}
	return false
}

// Fallback reasons recorded on every non-classified decision so the request
// log and system events say exactly why the default was used.
const (
	ReasonClassified    = "classified"
	ReasonDisabled      = "disabled"         // no sidecar configured for this router
	ReasonClientSet     = "client_specified" // client already chose a level; it wins
	ReasonShadow        = "shadow"           // classified, logged, deliberately not injected
	ReasonTimeout       = "timeout"
	ReasonError         = "error"
	ReasonBadResponse   = "bad_response"
	ReasonLowConfidence = "low_confidence"
	ReasonNoTemplate    = "no_template"
)

// Decision is the sidecar's answer for one request.
type Decision struct {
	Level      string  `json:"level"`
	Confidence float64 `json:"confidence"`
}

// Classifier answers the effort class for a request body. Implementations
// must honour ctx cancellation — the router enforces a strict timeout budget
// (default 50ms) around the call.
//
// The interface is the swap point required by the epic: a deterministic
// heuristic ships first and the Laya fine-tune later replaces the sidecar
// behind the same HTTP contract, with zero relai changes.
type Classifier interface {
	Classify(ctx context.Context, body []byte) (Decision, error)
}

// maxTextBytes bounds the prompt text forwarded to the sidecar. The
// classifier only needs enough of the conversation to judge difficulty;
// shipping whole multi-MB contexts would add latency to a call whose entire
// budget is tens of milliseconds.
const maxTextBytes = 4096

// sidecarRequest is the wire format POSTed to the sidecar: a truncated
// transcript plus cheap structural features, so a heuristic sidecar can work
// without re-parsing the body.
type sidecarRequest struct {
	Text     string `json:"text"`
	Messages int    `json:"messages"`
	Bytes    int    `json:"bytes"`
}

// HTTPClassifier talks to the external sidecar over HTTP.
type HTTPClassifier struct {
	URL    string
	Client *http.Client
}

// NewHTTPClassifier builds a classifier client for the given sidecar base
// URL. The per-call timeout is enforced via context by the caller, so the
// underlying client keeps a generous ceiling of its own.
func NewHTTPClassifier(url string) *HTTPClassifier {
	return &HTTPClassifier{
		URL:    url,
		Client: &http.Client{Timeout: 5 * time.Second},
	}
}

// StatusError reports a sidecar response with a non-200 status. The fallback
// policy treats it as a bad response; the type keeps the classification free
// of string matching.
type StatusError struct{ Code int }

func (e *StatusError) Error() string {
	return fmt.Sprintf("classifier returned status %d", e.Code)
}

// BadResponseError reports a sidecar response that could not be decoded into
// a usable decision (malformed JSON, unknown level).
type BadResponseError struct{ Reason string }

func (e *BadResponseError) Error() string { return "classifier response invalid: " + e.Reason }

// Classify POSTs the request body's salient text to the sidecar and decodes
// {level, confidence}. Errors are returned to the caller, which owns the
// fallback policy — this type never silently substitutes a default.
func (c *HTTPClassifier) Classify(ctx context.Context, body []byte) (Decision, error) {
	payload, err := buildSidecarRequest(body)
	if err != nil {
		return Decision{}, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Decision{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(raw))
	if err != nil {
		return Decision{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.Client.Do(req)
	if err != nil {
		return Decision{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return Decision{}, &StatusError{Code: resp.StatusCode}
	}

	var d Decision
	dec := json.NewDecoder(io.LimitReader(resp.Body, 64*1024))
	if err := dec.Decode(&d); err != nil {
		return Decision{}, &BadResponseError{Reason: "not JSON: " + err.Error()}
	}
	if !ValidLevel(d.Level) {
		return Decision{}, &BadResponseError{Reason: "invalid level " + d.Level}
	}
	return d, nil
}

// buildSidecarRequest extracts the truncated transcript and cheap features
// from an OpenAI chat body. A body that is not a chat completion (no
// messages array) is rejected — the classifier has nothing to grade.
func buildSidecarRequest(body []byte) (sidecarRequest, error) {
	var req struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return sidecarRequest{}, fmt.Errorf("request body not JSON: %w", err)
	}
	if len(req.Messages) == 0 {
		return sidecarRequest{}, errors.New("request has no messages to classify")
	}
	var text bytes.Buffer
	for _, m := range req.Messages {
		// content may be a string or a content-parts array; stringify cheaply.
		var s string
		if err := json.Unmarshal(m.Content, &s); err == nil {
			text.WriteString(m.Role)
			text.WriteByte(':')
			text.WriteString(s)
		} else {
			text.WriteString(m.Role)
			text.WriteByte(':')
			text.Write(m.Content)
		}
		text.WriteByte('\n')
		if text.Len() >= maxTextBytes {
			break
		}
	}
	truncated := text.String()
	if len(truncated) > maxTextBytes {
		truncated = truncated[:maxTextBytes]
	}
	return sidecarRequest{Text: truncated, Messages: len(req.Messages), Bytes: len(body)}, nil
}

// ClassifyWithFallback applies the router's fallback policy around a
// classifier call: strict timeout budget, then default level on ANY failure
// or sub-floor confidence. It never returns an error — a decision plus the
// reason it was made is always available for logging.
//
// defaultLevel is what to use when the classifier cannot answer. An empty
// defaultLevel means "current behavior": no level is applied and Reason
// records the failure.
func ClassifyWithFallback(ctx context.Context, c Classifier, body []byte, timeout time.Duration, defaultLevel string, floor float64) (Decision, string) {
	if c == nil {
		return Decision{Level: defaultLevel}, ReasonDisabled
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	d, err := c.Classify(callCtx, body)
	if err == nil && d.Confidence < floor {
		return Decision{Level: defaultLevel, Confidence: d.Confidence}, ReasonLowConfidence
	}
	if err == nil {
		return d, ReasonClassified
	}

	// Any failure resolves to the default level; only the reason differs.
	var se *StatusError
	var be *BadResponseError
	switch {
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(callCtx.Err(), context.DeadlineExceeded):
		return Decision{Level: defaultLevel}, ReasonTimeout
	case errors.As(err, &se) || errors.As(err, &be):
		return Decision{Level: defaultLevel}, ReasonBadResponse
	default:
		var ne interface{ Timeout() bool }
		if errors.As(err, &ne) && ne.Timeout() {
			return Decision{Level: defaultLevel}, ReasonTimeout
		}
		return Decision{Level: defaultLevel}, ReasonError
	}
}
