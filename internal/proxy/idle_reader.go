package proxy

import (
	"context"
	"fmt"
	"io"
	"time"
)

// idleTimeoutReader cancels its request when an already-established stream has
// made no read progress for the configured duration. It deliberately does not
// govern initial response headers; that is handled by Transport's
// ResponseHeaderTimeout so very large prompts can still begin legitimately.
type idleTimeoutReader struct {
	reader  io.Reader
	timeout time.Duration
	cancel  context.CancelFunc
}

type readResult struct {
	n   int
	err error
}

func (r *idleTimeoutReader) Read(p []byte) (int, error) {
	if r.timeout <= 0 {
		return r.reader.Read(p)
	}

	result := make(chan readResult, 1)
	go func() {
		n, err := r.reader.Read(p)
		result <- readResult{n: n, err: err}
	}()

	timer := time.NewTimer(r.timeout)
	defer timer.Stop()

	select {
	case got := <-result:
		return got.n, got.err
	case <-timer.C:
		r.cancel()
		// Cancellation unblocks the transport read. Wait before returning so
		// io.Copy never starts a concurrent read against the response body.
		<-result
		return 0, fmt.Errorf("upstream stream idle for %s", r.timeout)
	}
}
