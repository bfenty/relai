package db

import "testing"

// Issue #10: the widened INSERT (29 columns) must actually execute and the
// NULL-vs-value distinction must hold, or per-request classifier
// observability silently does nothing.
func TestThinkingLogInsertRoundTrip(t *testing.T) {
	d, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	lvl, conf, ms := "high", 0.91, 12
	id, err := d.Logs.Create(LogInput{
		Model: "m", Endpoint: "chat", StatusCode: 200,
		ThinkingLevel: &lvl, ThinkingConfidence: &conf, ClassifierMs: &ms,
		ThinkingReason: "classified",
	})
	if err != nil {
		t.Fatalf("insert with thinking columns failed: %v", err)
	}
	if id == 0 {
		t.Fatal("expected nonzero id")
	}
	var gotLvl, gotReason string
	var gotConf float64
	var gotMs int
	err = d.conn.QueryRow(`SELECT thinking_level, thinking_confidence, classifier_ms, thinking_reason FROM request_logs WHERE id=?`, id).
		Scan(&gotLvl, &gotConf, &gotMs, &gotReason)
	if err != nil {
		t.Fatal(err)
	}
	if gotLvl != "high" || gotConf != 0.91 || gotMs != 12 || gotReason != "classified" {
		t.Fatalf("round-trip mismatch: %q %v %d %q", gotLvl, gotConf, gotMs, gotReason)
	}

	// NULL path: rows without a classification stay NULL (distinguishable
	// from a classified "off").
	id2, err := d.Logs.Create(LogInput{Model: "m2", Endpoint: "chat", StatusCode: 200})
	if err != nil {
		t.Fatalf("insert without thinking columns failed: %v", err)
	}
	var v any
	if err := d.conn.QueryRow(`SELECT thinking_level FROM request_logs WHERE id=?`, id2).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != nil {
		t.Fatalf("thinking_level should be NULL, got %v", v)
	}
}

// Router thinking config must survive Create -> Get -> Update -> Get.
func TestRouterThinkingConfigRoundTrip(t *testing.T) {
	d, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	rt, err := d.Routers.Create(RouterInput{
		Name: "adaptive", Strategy: "adaptive_thinking",
		ThinkingSidecarURL: "http://c:8090/classify", ThinkingTimeoutMs: 40,
		ThinkingDefaultLevel: "low", ThinkingConfidenceFloor: 0.6,
		ThinkingMode: "shadow", ThinkingParamTemplateJSON: `{"high":{}}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rt.ThinkingSidecarURL != "http://c:8090/classify" || rt.ThinkingTimeoutMs != 40 ||
		rt.ThinkingDefaultLevel != "low" || rt.ThinkingConfidenceFloor != 0.6 ||
		rt.ThinkingMode != "shadow" || rt.ThinkingParamTemplateJSON != `{"high":{}}` {
		t.Fatalf("create did not persist thinking config: %+v", rt)
	}
	got, err := d.Routers.Get(rt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ThinkingMode != "shadow" || got.ThinkingConfidenceFloor != 0.6 {
		t.Fatalf("Get lost thinking config: %+v", got)
	}
	rt.ThinkingMode = "enforce"
	updated, err := d.Routers.Update(rt.ID, RouterInput{
		Name: "adaptive", Strategy: "adaptive_thinking",
		ThinkingSidecarURL: "http://c:8090/classify", ThinkingTimeoutMs: 40,
		ThinkingDefaultLevel: "low", ThinkingConfidenceFloor: 0.6,
		ThinkingMode: "enforce", ThinkingParamTemplateJSON: `{"high":{}}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ThinkingMode != "enforce" {
		t.Fatalf("Update lost thinking_mode: %+v", updated)
	}
	list, err := d.Routers.List()
	if err != nil || len(list) != 1 || list[0].ThinkingMode != "enforce" {
		t.Fatalf("List lost thinking config: %v %+v", err, list)
	}
}
