package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bfenty/relai/internal/db"
	"github.com/bfenty/relai/internal/queue"
)

// transformRequest injects return_progress only when the backend is Halogen.
func TestTransformInjectsReturnProgressForHalogen(t *testing.T) {
	body := []byte(`{"model":"local","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	out, _, err := transformRequestWithCompression(body, "local", "remote", "{}", "", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["return_progress"] != true {
		t.Fatalf("expected return_progress=true, got %v", got["return_progress"])
	}
}

func TestTransformNoReturnProgressForOthers(t *testing.T) {
	body := []byte(`{"model":"local","messages":[{"role":"user","content":"hi"}],"stream":true}`)
	out, _, err := transformRequestWithCompression(body, "local", "remote", "{}", "", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["return_progress"]; ok {
		t.Fatal("return_progress must be absent for non-Halogen backends")
	}
}

// A client that set return_progress itself is never overwritten.
func TestTransformPreservesClientReturnProgress(t *testing.T) {
	body := []byte(`{"model":"local","messages":[],"stream":true,"return_progress":false}`)
	out, _, err := transformRequestWithCompression(body, "local", "remote", "{}", "", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["return_progress"] != false {
		t.Fatalf("client value must win, got %v", got["return_progress"])
	}
}

// The Halogen probe accepts a /health with a version block and rejects anything
// else, including errors and non-200s.
func TestProbeHalogen(t *testing.T) {
	halogen := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok","version":{"api":"0.18.1","engine":"0.18.1","match":true}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer halogen.Close()

	llama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok","model":"some-model"}`))
	}))
	defer llama.Close()

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer dead.Close()

	if !probeBackend(&http.Client{}, halogen.URL).promptProgress {
		t.Fatal("expected Halogen probe to succeed")
	}
	if probeBackend(&http.Client{}, llama.URL).promptProgress {
		t.Fatal("plain server without version block must not be treated as Halogen")
	}
	if probeBackend(&http.Client{}, dead.URL).promptProgress {
		t.Fatal("non-200 must not be treated as Halogen")
	}
}

// StreamingReader must feed the tracker from prompt_progress chunks and strip
// them from the client-visible stream.
func TestStreamingReaderConsumesPromptProgress(t *testing.T) {
	tracker := queue.NewTracker()
	id := tracker.Add(1, "watch", "qwen-next-med")

	s := &StreamingReader{
		Proxy:         &Proxy{},
		Backend:       &db.Backend{ID: 1, Name: "watch"},
		TrackID:       id,
		Tracker:       tracker,
		StripProgress: true,
	}

	progress := `data: {"id":"x","object":"chat.completion.chunk","choices":[],"prompt_progress":{"total":26378,"cache":0,"processed":10766,"time_ms":6076}}` + "\n\n"
	token := `data: {"id":"x","object":"chat.completion.chunk","choices":[{"delta":{"content":"Hi"}}]}` + "\n\n"
	done := "data: [DONE]\n\n"

	// Feed through the line processor directly (Read path is covered by the
	// strip assertions below via processLine + isProgressLine).
	s.processLine(strings.TrimSuffix(progress, "\n\n"))

	snap := tracker.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(snap))
	}
	e := snap[0]
	if e.PromptTotal != 26378 || e.PromptProcessed != 10766 || e.PromptEngineMs != 6076 {
		t.Fatalf("tracker not fed: %+v", e)
	}
	if e.EstPromptTokens != 26378 {
		t.Fatalf("engine total should replace the estimate, got %d", e.EstPromptTokens)
	}

	if !isProgressLine([]byte(strings.TrimSuffix(progress, "\n\n"))) {
		t.Fatal("progress line must be detected for stripping")
	}
	if isProgressLine([]byte(strings.TrimSuffix(token, "\n\n"))) {
		t.Fatal("token line must not be stripped")
	}
	if isProgressLine([]byte(strings.TrimSuffix(done, "\n\n"))) {
		t.Fatal("DONE line must not be stripped")
	}
}
