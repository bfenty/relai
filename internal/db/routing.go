package db

import (
	"database/sql"
	"encoding/json"
	"time"
)

// RoutingRule defines a routing rule with strategy and conditions.
type RoutingRule struct {
	ID         int64                  `json:"id"`
	Name       string                 `json:"name"`
	Strategy   string                 `json:"strategy"`
	Priority   int                    `json:"priority"`
	Conditions map[string]interface{} `json:"conditions"`
	Config     map[string]interface{} `json:"config"`
	IsActive   bool                   `json:"is_active"`
	CreatedAt  time.Time              `json:"created_at"`
	UpdatedAt  time.Time              `json:"updated_at"`
}

// RoutingRuleInput is used for creating/updating routing rules.
type RoutingRuleInput struct {
	Name       string                 `json:"name"`
	Strategy   string                 `json:"strategy"`
	Priority   int                    `json:"priority"`
	Conditions map[string]interface{} `json:"conditions,omitempty"`
	Config     map[string]interface{} `json:"config,omitempty"`
	IsActive   *bool                  `json:"is_active,omitempty"`
}

// RoutingRepo handles CRUD operations for routing rules.
type RoutingRepo struct {
	db *sql.DB
}

func newRoutingRepo(db *sql.DB) *RoutingRepo {
	return &RoutingRepo{db: db}
}

// Create inserts a new routing rule.
func (r *RoutingRepo) Create(input RoutingRuleInput) (*RoutingRule, error) {
	conditions, _ := json.Marshal(input.Conditions)
	config, _ := json.Marshal(input.Config)

	isActive := true
	if input.IsActive != nil {
		isActive = *input.IsActive
	}

	result, err := r.db.Exec(`
		INSERT INTO routing_rules (name, strategy, priority, conditions, config, is_active)
		VALUES (?, ?, ?, ?, ?, ?)`,
		input.Name, input.Strategy, input.Priority,
		string(conditions), string(config), isActive)
	if err != nil {
		return nil, err
	}

	id, _ := result.LastInsertId()
	return r.Get(id)
}

// Get returns a routing rule by ID.
func (r *RoutingRepo) Get(id int64) (*RoutingRule, error) {
	rule := &RoutingRule{}
	var conditionsStr, configStr string
	err := r.db.QueryRow(`
		SELECT id, name, strategy, priority, conditions, config, is_active, created_at, updated_at
		FROM routing_rules WHERE id = ?`, id).Scan(
		&rule.ID, &rule.Name, &rule.Strategy, &rule.Priority,
		&conditionsStr, &configStr, &rule.IsActive, &rule.CreatedAt, &rule.UpdatedAt)
	if err != nil {
		return nil, err
	}
	// Parse JSON fields
	json.Unmarshal([]byte(conditionsStr), &rule.Conditions)
	json.Unmarshal([]byte(configStr), &rule.Config)
	return rule, nil
}

// List returns all routing rules ordered by priority.
func (r *RoutingRepo) List() ([]RoutingRule, error) {
	rows, err := r.db.Query(`
		SELECT id, name, strategy, priority, conditions, config, is_active, created_at, updated_at
		FROM routing_rules ORDER BY priority`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rules := make([]RoutingRule, 0)
	for rows.Next() {
		var rule RoutingRule
		var conditionsStr, configStr string
		err := rows.Scan(&rule.ID, &rule.Name, &rule.Strategy, &rule.Priority,
			&conditionsStr, &configStr, &rule.IsActive, &rule.CreatedAt, &rule.UpdatedAt)
		if err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(conditionsStr), &rule.Conditions)
		json.Unmarshal([]byte(configStr), &rule.Config)
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

// ListActive returns only active routing rules.
func (r *RoutingRepo) ListActive() ([]RoutingRule, error) {
	rows, err := r.db.Query(`
		SELECT id, name, strategy, priority, conditions, config, is_active, created_at, updated_at
		FROM routing_rules WHERE is_active = 1 ORDER BY priority`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rules := make([]RoutingRule, 0)
	for rows.Next() {
		var rule RoutingRule
		var conditionsStr, configStr string
		err := rows.Scan(&rule.ID, &rule.Name, &rule.Strategy, &rule.Priority,
			&conditionsStr, &configStr, &rule.IsActive, &rule.CreatedAt, &rule.UpdatedAt)
		if err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(conditionsStr), &rule.Conditions)
		json.Unmarshal([]byte(configStr), &rule.Config)
		rules = append(rules, rule)
	}
	return rules, rows.Err()
}

// Update modifies an existing routing rule.
func (r *RoutingRepo) Update(id int64, input RoutingRuleInput) (*RoutingRule, error) {
	conditions, _ := json.Marshal(input.Conditions)
	config, _ := json.Marshal(input.Config)

	_, err := r.db.Exec(`
		UPDATE routing_rules SET name=?, strategy=?, priority=?, conditions=?, config=?, updated_at=CURRENT_TIMESTAMP
		WHERE id=?`,
		input.Name, input.Strategy, input.Priority,
		string(conditions), string(config), id)
	if err != nil {
		return nil, err
	}
	return r.Get(id)
}

// Delete removes a routing rule.
func (r *RoutingRepo) Delete(id int64) error {
	_, err := r.db.Exec("DELETE FROM routing_rules WHERE id=?", id)
	return err
}

// UpsertByName creates or updates a routing rule matched by name.
func (r *RoutingRepo) UpsertByName(rr RoutingRule) (*RoutingRule, error) {
	conditions, _ := json.Marshal(rr.Conditions)
	config, _ := json.Marshal(rr.Config)

	var id int64
	err := r.db.QueryRow(`SELECT id FROM routing_rules WHERE name = ? LIMIT 1`, rr.Name).Scan(&id)
	if err == sql.ErrNoRows {
		result, err := r.db.Exec(`
			INSERT INTO routing_rules (name, strategy, priority, conditions, config, is_active)
			VALUES (?, ?, ?, ?, ?, ?)`,
			rr.Name, rr.Strategy, rr.Priority, string(conditions), string(config), rr.IsActive)
		if err != nil {
			return nil, err
		}
		id, _ = result.LastInsertId()
	} else if err != nil {
		return nil, err
	} else {
		_, err = r.db.Exec(`
			UPDATE routing_rules
			SET strategy=?, priority=?, conditions=?, config=?, is_active=?, updated_at=CURRENT_TIMESTAMP
			WHERE id=?`,
			rr.Strategy, rr.Priority, string(conditions), string(config), rr.IsActive, id)
		if err != nil {
			return nil, err
		}
	}
	return r.Get(id)
}

// ToggleActive toggles the active status.
func (r *RoutingRepo) ToggleActive(id int64, isActive bool) error {
	_, err := r.db.Exec("UPDATE routing_rules SET is_active=?, updated_at=CURRENT_TIMESTAMP WHERE id=?",
		isActive, id)
	return err
}
