package router

import (
	"context"

	"github.com/bfenty/relai/internal/db"
)

// LeastConnections routes to the backend target with the fewest active requests.
type LeastConnections struct {
	db     *db.DB
	active *ActiveTracker
}

func NewLeastConnections(db *db.DB, active *ActiveTracker) *LeastConnections {
	return &LeastConnections{db: db, active: active}
}

func (l *LeastConnections) SelectTarget(ctx context.Context, req *RoutingRequest) (*RoutingTarget, error) {
	targets, err := eligibleTargets(l.db, req)
	if err != nil {
		return nil, err
	}

	best := &targets[0]
	bestCount := l.active.Count(best.Backend.ID)
	for i := 1; i < len(targets); i++ {
		count := l.active.Count(targets[i].Backend.ID)
		if count < bestCount {
			best = &targets[i]
			bestCount = count
		}
	}
	return best, nil
}

func (l *LeastConnections) SelectBackend(ctx context.Context, req *RoutingRequest) (*db.Backend, error) {
	target, err := l.SelectTarget(ctx, req)
	if err != nil {
		return nil, err
	}
	return target.Backend, nil
}
