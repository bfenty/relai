// Package hostlimit provides per-host (backend machine) concurrency limiting.
// All requests destined for the same host share one counting semaphore,
// regardless of which model on that host they target or whether they arrive
// through a virtual router or a direct model mapping.
package hostlimit

import (
	"context"
	"sync"
)

// Limiter holds one Slot per host (backend ID).
type Limiter struct {
	mu    sync.Mutex
	hosts map[int64]*Slot
}

func New() *Limiter {
	return &Limiter{hosts: make(map[int64]*Slot)}
}

// Slot returns the semaphore for a host, creating it if needed and updating
// its capacity. Capacity values below 1 are treated as 1.
func (l *Limiter) Slot(backendID int64, capacity int) *Slot {
	if capacity < 1 {
		capacity = 1
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.hosts[backendID]
	if s == nil {
		s = NewSlot(capacity)
		l.hosts[backendID] = s
	} else {
		s.SetCapacity(capacity)
	}
	return s
}

// Slot is a counting semaphore with broadcast wakeup on release.
type Slot struct {
	mu       sync.Mutex
	active   int
	capacity int
	waiters  int
	idleCh   chan struct{}
}

func NewSlot(capacity int) *Slot {
	if capacity < 1 {
		capacity = 1
	}
	return &Slot{capacity: capacity, idleCh: make(chan struct{})}
}

// SetCapacity updates the slot's capacity; raising it wakes waiters so they
// can claim the new room.
func (s *Slot) SetCapacity(capacity int) {
	if capacity < 1 {
		capacity = 1
	}
	s.mu.Lock()
	grew := capacity > s.capacity
	s.capacity = capacity
	var old chan struct{}
	if grew {
		old = s.idleCh
		s.idleCh = make(chan struct{})
	}
	s.mu.Unlock()
	if old != nil {
		close(old)
	}
}

// TryAcquire claims a slot if capacity allows, without blocking.
func (s *Slot) TryAcquire() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active >= s.capacity {
		return false
	}
	s.active++
	return true
}

// TryAcquireOrWait claims a slot if possible. Otherwise it registers a waiter
// and returns a channel that is closed on the next release or broadcast; the
// caller must call EndWait after the channel fires (or on cancellation), then
// retry.
func (s *Slot) TryAcquireOrWait() (bool, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active < s.capacity {
		s.active++
		return true, nil
	}
	s.waiters++
	return false, s.idleCh
}

// EndWait unregisters a waiter previously registered by TryAcquireOrWait.
func (s *Slot) EndWait() {
	s.mu.Lock()
	s.waiters--
	s.mu.Unlock()
}

// Acquire blocks until a slot is free or ctx is done.
func (s *Slot) Acquire(ctx context.Context) error {
	for {
		ok, ch := s.TryAcquireOrWait()
		if ok {
			return nil
		}
		select {
		case <-ch:
			s.EndWait()
		case <-ctx.Done():
			s.EndWait()
			return ctx.Err()
		}
	}
}

// Release frees one slot and wakes all waiters.
func (s *Slot) Release() {
	s.mu.Lock()
	if s.active > 0 {
		s.active--
	}
	old := s.idleCh
	s.idleCh = make(chan struct{})
	s.mu.Unlock()
	close(old)
}

// Broadcast wakes all waiters without changing the active count. Used when a
// router member's health changes so blocked acquirers can re-evaluate.
func (s *Slot) Broadcast() {
	s.mu.Lock()
	old := s.idleCh
	s.idleCh = make(chan struct{})
	s.mu.Unlock()
	close(old)
}

// Load reports the current active count, waiter count, and capacity.
func (s *Slot) Load() (active, waiters, capacity int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active, s.waiters, s.capacity
}
