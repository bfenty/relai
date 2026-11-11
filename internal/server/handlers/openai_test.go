package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/bfenty/relai/internal/proxy"
)

func TestDirectAdmissionQueuesSecondRequestFromSameClient(t *testing.T) {
	h := NewOpenAIHandler(nil, nil, nil, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client := &proxy.RequestContext{ClientKey: "api_key:1", RequestedModel: "model-a"}

	releaseFirst, queued, err := h.acquireAdmission(ctx, "model-a", client)
	if err != nil {
		t.Fatalf("first acquireAdmission failed: %v", err)
	}
	if queued {
		t.Fatal("first request should not queue")
	}

	acquiredSecond := make(chan bool, 1)
	go func() {
		releaseSecond, queuedSecond, err := h.acquireAdmission(ctx, "model-a", client)
		if err != nil {
			t.Errorf("second acquireAdmission failed: %v", err)
			acquiredSecond <- false
			return
		}
		defer releaseSecond()
		acquiredSecond <- queuedSecond
	}()

	select {
	case <-acquiredSecond:
		t.Fatal("second request from same client should wait while first is active")
	case <-time.After(20 * time.Millisecond):
	}

	releaseFirst()
	select {
	case queuedSecond := <-acquiredSecond:
		if !queuedSecond {
			t.Fatal("second request should report that it queued")
		}
	case <-time.After(time.Second):
		t.Fatal("second request did not leave admission queue after first released")
	}
}

func TestDirectAdmissionAllowsDifferentClientWhileFirstClientQueued(t *testing.T) {
	h := NewOpenAIHandler(nil, nil, nil, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	releaseA, _, err := h.acquireAdmission(ctx, "model-a", &proxy.RequestContext{ClientKey: "api_key:1"})
	if err != nil {
		t.Fatalf("client A first acquire failed: %v", err)
	}

	aQueued := make(chan struct{}, 1)
	go func() {
		releaseA2, _, err := h.acquireAdmission(ctx, "model-a", &proxy.RequestContext{ClientKey: "api_key:1"})
		if err != nil {
			t.Errorf("client A second acquire failed: %v", err)
			return
		}
		defer releaseA2()
		aQueued <- struct{}{}
	}()
	time.Sleep(20 * time.Millisecond)

	releaseB, queuedB, err := h.acquireAdmission(ctx, "model-a", &proxy.RequestContext{ClientKey: "api_key:2"})
	if err != nil {
		t.Fatalf("client B acquire failed: %v", err)
	}
	defer releaseB()
	if !queuedB {
		t.Fatal("client B should report queue path because an older waiter existed")
	}

	select {
	case <-aQueued:
		t.Fatal("client A second request should still be queued")
	default:
	}

	releaseA()
	select {
	case <-aQueued:
	case <-time.After(time.Second):
		t.Fatal("client A second request did not release after client A first completed")
	}
}
