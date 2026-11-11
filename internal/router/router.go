package router

import (
	"context"
	"fmt"
	"sync"

	"github.com/bfenty/relai/internal/db"
)

// Router selects a backend model target for a given request.
type Router interface {
	SelectTarget(ctx context.Context, req *RoutingRequest) (*RoutingTarget, error)
}

// RoutingRequest contains the info needed to select a backend.
type RoutingRequest struct {
	Model    string
	Mode     db.ModelMode
	APIKeyID int64
	Metadata map[string]string
}

// RoutingTarget is a selected backend plus the explicit model mapping to use.
type RoutingTarget struct {
	Backend  *db.Backend
	ModelMap *db.ModelMap
}

// Registry manages multiple routers and selects based on rules.
type Registry struct {
	mu          sync.RWMutex
	db          *db.DB
	routers     map[string]Router
	active      *ActiveTracker
	defaultName string
}

// NewRegistry creates a new router registry.
func NewRegistry(d *db.DB) *Registry {
	r := &Registry{
		db:          d,
		routers:     make(map[string]Router),
		active:      NewActiveTracker(),
		defaultName: "round_robin",
	}
	r.registerDefaults()
	return r
}

func (r *Registry) registerDefaults() {
	r.routers["round_robin"] = NewRoundRobin(r.db)
	r.routers["weighted"] = NewWeighted(r.db)
	r.routers["model_based"] = NewModelBased(r.db)
	r.routers["cost_based"] = NewCostBased(r.db)
	r.routers["latency_based"] = NewLatencyBased(r.db)
	r.routers["least_connections"] = NewLeastConnections(r.db, r.active)
}

// SelectTarget evaluates routing rules and returns the selected backend model target.
func (r *Registry) SelectTarget(ctx context.Context, req *RoutingRequest) (*RoutingTarget, error) {
	rules, err := r.db.Routing.ListActive()
	if err != nil {
		return nil, fmt.Errorf("list routing rules: %w", err)
	}

	// Evaluate rules by priority
	for _, rule := range rules {
		if r.matchesConditions(rule, req) {
			strategy, ok := r.routers[rule.Strategy]
			if !ok {
				continue
			}
			target, err := strategy.SelectTarget(ctx, req)
			if err == nil && target != nil {
				return target, nil
			}
		}
	}

	// Fall back to default router
	defaultRouter := r.routers[r.defaultName]
	if defaultRouter == nil {
		return nil, fmt.Errorf("no default router available")
	}
	return defaultRouter.SelectTarget(ctx, req)
}

// SelectBackend evaluates routing rules and returns the selected backend.
func (r *Registry) SelectBackend(ctx context.Context, req *RoutingRequest) (*db.Backend, error) {
	target, err := r.SelectTarget(ctx, req)
	if err != nil {
		return nil, err
	}
	return target.Backend, nil
}

// TrackActive increments the active request count for a selected target.
func (r *Registry) TrackActive(target *RoutingTarget) func() {
	if target == nil || target.Backend == nil {
		return func() {}
	}
	return r.active.Increment(target.Backend.ID)
}

// ActiveConnections returns the current active request count for a backend.
func (r *Registry) ActiveConnections(backendID int64) int {
	return r.active.Count(backendID)
}

// matchesConditions checks if a request matches a rule's conditions.
func (r *Registry) matchesConditions(rule db.RoutingRule, req *RoutingRequest) bool {
	if rule.Conditions == nil {
		return true
	}

	// Check model condition
	if models, ok := rule.Conditions["models"]; ok {
		if modelList, ok := models.([]interface{}); ok {
			if len(modelList) == 0 {
				return true // Empty list means match all
			}
			for _, m := range modelList {
				if modelStr, ok := m.(string); ok && modelMatches(modelStr, req.Model) {
					return true
				}
			}
			return false
		}
	}

	return true
}

// modelMatches checks if a model name matches a pattern (supports glob).
func modelMatches(pattern, model string) bool {
	if pattern == model {
		return true
	}
	// Simple wildcard support
	if pattern == "*" {
		return true
	}
	if len(pattern) > 1 && pattern[len(pattern)-1] == '*' {
		prefix := pattern[:len(pattern)-1]
		return len(model) >= len(prefix) && model[:len(prefix)] == prefix
	}
	return false
}

// SetDefaultRouter changes the default fallback router.
func (r *Registry) SetDefaultRouter(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.routers[name]; ok {
		r.defaultName = name
	}
}

func eligibleTargets(d *db.DB, req *RoutingRequest) ([]RoutingTarget, error) {
	model := req.Model
	if model == "" {
		return nil, fmt.Errorf("friendly model name is required")
	}

	mappings, err := d.ModelMaps.ResolveModel(model)
	if err != nil {
		return nil, fmt.Errorf("resolve model %q: %w", model, err)
	}
	if len(mappings) == 0 {
		return nil, fmt.Errorf("no backend targets configured for friendly model %q", model)
	}

	targets := make([]RoutingTarget, 0, len(mappings))
	for _, mapping := range mappings {
		backend, err := d.Backends.Get(mapping.BackendID)
		if err != nil || backend.Status != "active" {
			continue
		}
		if req.Mode != "" && !hasMode(mapping.Modes, req.Mode) {
			continue
		}
		mappingCopy := mapping
		targets = append(targets, RoutingTarget{
			Backend:  backend,
			ModelMap: &mappingCopy,
		})
	}
	if len(targets) == 0 {
		if req.Mode != "" {
			return nil, fmt.Errorf("no active backend targets configured for friendly model %q with mode %q", model, req.Mode)
		}
		return nil, fmt.Errorf("no active backend targets configured for friendly model %q", model)
	}
	return targets, nil
}

func hasMode(modes []db.ModelMode, mode db.ModelMode) bool {
	for _, m := range modes {
		if m == mode {
			return true
		}
	}
	return false
}
