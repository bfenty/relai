package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bfenty/relai/internal/db"
)

// #17: GET /api/analytics/cache must list every model map with its KV-cache
// telemetry joined to host/model names. Maps whose backend never exposed
// /cache appear with available=false so the UI can render them as "no
// telemetry" rather than hiding them.
func TestCacheTelemetryListsAllMaps(t *testing.T) {
	d, err := db.New(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	b, err := d.Backends.Create(db.BackendInput{
		Name: "frame-a", BaseURL: "http://127.0.0.1:1", Weight: 1, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatalf("create backend: %v", err)
	}
	mm, err := d.ModelMaps.Create(db.ModelMapInput{
		BackendID: b.ID, BaseURL: "http://127.0.0.1:1",
		LocalModel: "test-model", RemoteModel: "remote-model",
	})
	if err != nil {
		t.Fatalf("create model map: %v", err)
	}

	h := NewAdminHandler(d, nil, nil)
	rec := httptest.NewRecorder()
	h.HandleCacheTelemetry(rec, httptest.NewRequest(http.MethodGet, "/api/analytics/cache", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
	}

	var rows []struct {
		ModelMapID  int64  `json:"model_map_id"`
		Available   bool   `json:"available"`
		BackendName string `json:"backend_name"`
		ModelName   string `json:"model_name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("parse: %v (%s)", err, rec.Body.String())
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d: %s", len(rows), rec.Body.String())
	}
	r0 := rows[0]
	if r0.ModelMapID != mm.ID || r0.BackendName != "frame-a" || r0.ModelName != "test-model" {
		t.Fatalf("join wrong: %+v", r0)
	}
	if r0.Available {
		t.Fatalf("no telemetry was polled; available must be false")
	}
}
