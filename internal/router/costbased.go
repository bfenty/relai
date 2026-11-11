package router

import (
	"context"
	"math"

	"github.com/bfenty/relai/internal/db"
)

// CostBased routes to the backend with the lowest cost for the model.
type CostBased struct {
	db *db.DB
}

func NewCostBased(db *db.DB) *CostBased {
	return &CostBased{db: db}
}

func (c *CostBased) SelectTarget(ctx context.Context, req *RoutingRequest) (*RoutingTarget, error) {
	targets, err := eligibleTargets(c.db, req)
	if err != nil {
		return nil, err
	}

	// Fall back to lowest weight as a proxy for cost
	var cheapest *RoutingTarget
	minCost := math.MaxFloat64
	for i := range targets {
		cost := float64(1) / float64(targets[i].Backend.Weight)
		if cost < minCost {
			minCost = cost
			cheapest = &targets[i]
		}
	}

	if cheapest == nil {
		return &targets[0], nil
	}
	return cheapest, nil
}

func (c *CostBased) SelectBackend(ctx context.Context, req *RoutingRequest) (*db.Backend, error) {
	target, err := c.SelectTarget(ctx, req)
	if err != nil {
		return nil, err
	}
	return target.Backend, nil
}
