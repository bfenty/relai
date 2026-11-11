package db

import "testing"

// TestAPIKeyPriority covers the interactive-priority column: default 0,
// settable on create, omission-safe on update (nil keeps the stored value),
// and carried through the Validate hot path.
func TestAPIKeyPriority(t *testing.T) {
	d := setupTestDB(t)

	one := 1
	key, plain, err := d.APIKeys.Create(APIKeyInput{Name: "interactive", Priority: &one})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if key.Priority != 1 {
		t.Fatalf("created priority = %d, want 1", key.Priority)
	}

	ordinary, _, err := d.APIKeys.Create(APIKeyInput{Name: "ordinary"})
	if err != nil {
		t.Fatalf("create ordinary: %v", err)
	}
	if ordinary.Priority != 0 {
		t.Fatalf("default priority = %d, want 0", ordinary.Priority)
	}

	// Update without priority must preserve it.
	if _, err := d.APIKeys.Update(key.ID, APIKeyInput{Name: "interactive-renamed"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := d.APIKeys.Get(key.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Priority != 1 {
		t.Fatalf("priority after omission-safe update = %d, want 1", got.Priority)
	}

	// Validate (the auth hot path) must carry priority through.
	validated, err := d.APIKeys.Validate(plain)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if validated.Priority != 1 {
		t.Fatalf("validated priority = %d, want 1", validated.Priority)
	}

	// Update with priority=0 must demote (explicit zero, not omission).
	zero := 0
	if _, err := d.APIKeys.Update(key.ID, APIKeyInput{Priority: &zero}); err != nil {
		t.Fatalf("demote: %v", err)
	}
	got, _ = d.APIKeys.Get(key.ID)
	if got.Priority != 0 {
		t.Fatalf("priority after demote = %d, want 0", got.Priority)
	}
}
