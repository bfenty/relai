package proxy

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestNewSeparatesHeaderAndWholeStreamDeadlines(t *testing.T) {
	p := New(nil, nil, nil)
	transport, ok := p.client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected HTTP transport")
	}
	if got, want := p.client.Timeout, time.Duration(0); got != want {
		t.Fatalf("whole-request client timeout = %s, want %s", got, want)
	}
	if got, want := transport.ResponseHeaderTimeout, 15*time.Minute; got != want {
		t.Fatalf("response header timeout = %s, want %s", got, want)
	}
	if got, want := p.timeouts.StreamIdle, 2*time.Minute; got != want {
		t.Fatalf("stream idle timeout = %s, want %s", got, want)
	}
}

func TestIdleTimeoutReaderCancelsSilentStream(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	cancelled := make(chan struct{})

	reader := readerFunc(func([]byte) (int, error) {
		close(started)
		<-release
		return 0, context.Canceled
	})
	wrapped := &idleTimeoutReader{
		reader:  reader,
		timeout: 20 * time.Millisecond,
		cancel:  func() { close(cancelled); close(release) },
	}

	_, err := wrapped.Read(make([]byte, 1))
	if err == nil || err.Error() != "upstream stream idle for 20ms" {
		t.Fatalf("idle read error = %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("underlying read did not start")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("idle timeout did not cancel upstream request")
	}
}

func TestIdleTimeoutReaderPassesProgress(t *testing.T) {
	wrapped := &idleTimeoutReader{
		reader:  readerFunc(func(p []byte) (int, error) { copy(p, "ok"); return 2, nil }),
		timeout: time.Second,
		cancel:  func() { t.Fatal("unexpected cancellation") },
	}
	buf := make([]byte, 2)
	n, err := wrapped.Read(buf)
	if err != nil || n != 2 || string(buf) != "ok" {
		t.Fatalf("read = (%d, %q, %v), want (2, ok, nil)", n, string(buf), err)
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }
