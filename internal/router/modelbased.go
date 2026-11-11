package router

import (
	"context"
	"sync/atomic"

	"github.com/bfenty/relai/internal/db"
)

// ModelBased routes to backends that support the requested model.
type ModelBased struct {
	db      *db.DB
	counter atomic.Int64
}

func NewModelBased(db *db.DB) *ModelBased {
	return &ModelBased{db: db}
}

func (m *ModelBased) SelectTarget(ctx context.Context, req *RoutingRequest) (*RoutingTarget, error) {
	targets, err := eligibleTargets(m.db, req)
	if err != nil {
		return nil, err
	}

	idx := int(m.counter.Add(1)) % len(targets)
	return &targets[idx], nil
}

func (m *ModelBased) SelectBackend(ctx context.Context, req *RoutingRequest) (*db.Backend, error) {
	target, err := m.SelectTarget(ctx, req)
	if err != nil {
		return nil, err
	}
	return target.Backend, nil
}
