package router

import (
	"context"
	"sync/atomic"

	"github.com/bfenty/relai/internal/db"
)

// Weighted distributes requests proportionally by backend weight.
type Weighted struct {
	db      *db.DB
	counter atomic.Int64
}

func NewWeighted(db *db.DB) *Weighted {
	return &Weighted{db: db}
}

func (w *Weighted) SelectTarget(ctx context.Context, req *RoutingRequest) (*RoutingTarget, error) {
	targets, err := eligibleTargets(w.db, req)
	if err != nil {
		return nil, err
	}

	// Build weighted list
	totalWeight := 0
	for _, target := range targets {
		totalWeight += target.Backend.Weight
	}
	if totalWeight <= 0 {
		totalWeight = len(targets)
		for i := range targets {
			targets[i].Backend.Weight = 1
		}
	}

	counter := w.counter.Add(1)
	pos := int(counter % int64(totalWeight))

	current := 0
	for i := range targets {
		current += targets[i].Backend.Weight
		if pos < current {
			return &targets[i], nil
		}
	}

	return &targets[len(targets)-1], nil
}

func (w *Weighted) SelectBackend(ctx context.Context, req *RoutingRequest) (*db.Backend, error) {
	target, err := w.SelectTarget(ctx, req)
	if err != nil {
		return nil, err
	}
	return target.Backend, nil
}
