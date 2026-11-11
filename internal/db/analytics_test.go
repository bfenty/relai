package db

import (
	"testing"
	"time"
)

func TestUsageAnalyticsGroupsByModel(t *testing.T) {
	d, err := New(":memory:")
	if err != nil {
		t.Fatalf("db.New failed: %v", err)
	}
	defer d.Close()

	for i := 0; i < 3; i++ {
		if _, err := d.Logs.Create(LogInput{
			Model: "gpt-4", Endpoint: "/v1/chat/completions",
			TotalTokens: 100, StatusCode: 200,
		}); err != nil {
			t.Fatalf("create log: %v", err)
		}
	}
	if _, err := d.Logs.Create(LogInput{
		Model: "llama-3", Endpoint: "/v1/chat/completions",
		TotalTokens: 50, StatusCode: 500,
	}); err != nil {
		t.Fatalf("create log: %v", err)
	}

	now := time.Now()
	res, err := d.UsageAnalytics(AnalyticsOptions{
		Start:   now.Add(-2 * time.Hour),
		End:     now.Add(time.Hour),
		GroupBy: "model",
	})
	if err != nil {
		t.Fatalf("UsageAnalytics: %v", err)
	}

	if res.Interval != "hour" {
		t.Errorf("interval = %q, want hour", res.Interval)
	}
	if res.Summary.Requests != 4 {
		t.Errorf("summary requests = %d, want 4", res.Summary.Requests)
	}
	if res.Summary.Tokens != 350 {
		t.Errorf("summary tokens = %d, want 350", res.Summary.Tokens)
	}
	if res.Summary.Errors != 1 {
		t.Errorf("summary errors = %d, want 1", res.Summary.Errors)
	}

	byKey := map[string]AnalyticsGroup{}
	for _, g := range res.Groups {
		byKey[g.Key] = g
	}
	if byKey["gpt-4"].Requests != 3 {
		t.Errorf("gpt-4 requests = %d, want 3", byKey["gpt-4"].Requests)
	}
	if byKey["llama-3"].Tokens != 50 {
		t.Errorf("llama-3 tokens = %d, want 50", byKey["llama-3"].Tokens)
	}

	// All request counts across buckets must reconcile with the summary.
	total := 0
	for _, b := range res.Buckets {
		for _, c := range b.Groups {
			total += c.Requests
		}
	}
	if total != res.Summary.Requests {
		t.Errorf("bucket requests sum = %d, want %d", total, res.Summary.Requests)
	}
}

func TestUsageAnalyticsGroupsByUser(t *testing.T) {
	d, err := New(":memory:")
	if err != nil {
		t.Fatalf("db.New failed: %v", err)
	}
	defer d.Close()

	uid, err := d.Users.Create(UserInput{Name: "Alice", Email: "alice@example.com"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	key, _, err := d.APIKeys.Create(APIKeyInput{Name: "alice-key", UserID: &uid.ID})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if _, err := d.Logs.Create(LogInput{
		APIKeyID: &key.ID, Model: "gpt-4", Endpoint: "/v1/chat/completions",
		TotalTokens: 100, StatusCode: 200,
	}); err != nil {
		t.Fatalf("create log: %v", err)
	}
	// An anonymous request (no api key) should land under "(unassigned)".
	if _, err := d.Logs.Create(LogInput{
		Model: "gpt-4", Endpoint: "/v1/chat/completions",
		TotalTokens: 10, StatusCode: 200,
	}); err != nil {
		t.Fatalf("create anon log: %v", err)
	}

	now := time.Now()
	res, err := d.UsageAnalytics(AnalyticsOptions{
		Start:   now.Add(-2 * time.Hour),
		End:     now.Add(time.Hour),
		GroupBy: "user",
	})
	if err != nil {
		t.Fatalf("UsageAnalytics: %v", err)
	}

	labels := map[string]int{}
	for _, g := range res.Groups {
		labels[g.Label] = g.Tokens
	}
	if labels["Alice"] != 100 {
		t.Errorf("Alice tokens = %d, want 100", labels["Alice"])
	}
	if labels["(unassigned)"] != 10 {
		t.Errorf("unassigned tokens = %d, want 10", labels["(unassigned)"])
	}
}
