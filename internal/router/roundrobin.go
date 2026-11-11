package router

import (
	"context"
	"sync/atomic"

	"github.com/bfenty/relai/internal/db"
)

// RoundRobin distributes requests evenly across backends.
type RoundRobin struct {
	db      *db.DB
	counter atomic.Int64
}

func NewRoundRobin(db *db.DB) *RoundRobin {
	return &RoundRobin{db: db}
}

func (r *RoundRobin) SelectTarget(ctx context.Context, req *RoutingRequest) (*RoutingTarget, error) {
	targets, err := eligibleTargets(r.db, req)
	if err != nil {
		return nil, err
	}

	idx := int(r.counter.Add(1)) % len(targets)
	return &targets[idx], nil
}

func (r *RoundRobin) SelectBackend(ctx context.Context, req *RoutingRequest) (*db.Backend, error) {
	target, err := r.SelectTarget(ctx, req)
	if err != nil {
		return nil, err
	}
	return target.Backend, nil
}
