package hostlimit

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSlotCapacityEnforced(t *testing.T) {
	s := NewSlot(2)
	if !s.TryAcquire() || !s.TryAcquire() {
		t.Fatal("expected two acquisitions within capacity")
	}
	if s.TryAcquire() {
		t.Fatal("expected third acquisition to fail at capacity 2")
	}
	s.Release()
	if !s.TryAcquire() {
		t.Fatal("expected acquisition after release")
	}
}

func TestAcquireBlocksAndWakesOnRelease(t *testing.T) {
	s := NewSlot(1)
	if !s.TryAcquire() {
		t.Fatal("first acquire failed")
	}

	got := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		got <- s.Acquire(ctx)
	}()

	select {
	case <-got:
		t.Fatal("second acquire should block while slot is held")
	case <-time.After(20 * time.Millisecond):
	}

	s.Release()
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("acquire after release failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("acquire did not wake after release")
	}
}

func TestAcquireRespectsContextCancellation(t *testing.T) {
	s := NewSlot(1)
	s.TryAcquire()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := s.Acquire(ctx); err == nil {
		t.Fatal("expected context error")
	}
	if _, waiters, _ := s.Load(); waiters != 0 {
		t.Fatalf("expected zero waiters after cancellation, got %d", waiters)
	}
}

func TestSetCapacityGrowthWakesWaiters(t *testing.T) {
	s := NewSlot(1)
	s.TryAcquire()
	got := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		got <- s.Acquire(ctx)
	}()
	time.Sleep(20 * time.Millisecond)
	s.SetCapacity(2)
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("acquire after capacity growth failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter did not wake when capacity grew")
	}
}

func TestLimiterSharesSlotAcrossCallers(t *testing.T) {
	l := New()
	a := l.Slot(7, 1)
	b := l.Slot(7, 1)
	if a != b {
		t.Fatal("expected the same slot for the same host")
	}
	if l.Slot(8, 1) == a {
		t.Fatal("expected distinct slots for distinct hosts")
	}
}

func TestConcurrentLoadNeverExceedsCapacity(t *testing.T) {
	s := NewSlot(3)
	var inFlight, peak atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.Acquire(ctx); err != nil {
				t.Errorf("acquire failed: %v", err)
				return
			}
			n := inFlight.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			inFlight.Add(-1)
			s.Release()
		}()
	}
	wg.Wait()
	if peak.Load() > 3 {
		t.Fatalf("in-flight peak %d exceeded capacity 3", peak.Load())
	}
}
