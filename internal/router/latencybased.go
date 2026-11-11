package router

import (
	"context"
	"math"

	"github.com/bfenty/relai/internal/db"
)

// LatencyBased routes to the backend with the lowest recent latency.
type LatencyBased struct {
	db *db.DB
}

func NewLatencyBased(db *db.DB) *LatencyBased {
	return &LatencyBased{db: db}
}

func (l *LatencyBased) SelectTarget(ctx context.Context, req *RoutingRequest) (*RoutingTarget, error) {
	targets, err := eligibleTargets(l.db, req)
	if err != nil {
		return nil, err
	}

	// Find backend with lowest latency
	var fastest *RoutingTarget
	minLatency := math.MaxInt32
	for i := range targets {
		latency := targets[i].Backend.LastLatencyMs
		if latency == 0 {
			latency = math.MaxInt32 - 1 // Treat unknown as very slow
		}
		if latency < minLatency {
			minLatency = latency
			fastest = &targets[i]
		}
	}

	if fastest == nil {
		return &targets[0], nil
	}
	return fastest, nil
}

func (l *LatencyBased) SelectBackend(ctx context.Context, req *RoutingRequest) (*db.Backend, error) {
	target, err := l.SelectTarget(ctx, req)
	if err != nil {
		return nil, err
	}
	return target.Backend, nil
}
