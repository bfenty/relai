package handlers

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/bfenty/relai/internal/db"
)

// POST /api/backends/{id}/health must parse the ID with the /health suffix present.
// Previously it did TrimPrefix only, so parseID saw "3/health" and every refresh
// from the Hosts page returned "invalid ID".
func TestBackendHealthParsesIDWithHealthSuffix(t *testing.T) {
	d, err := db.New(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	// Point at a closed port so the health check fails to connect; we only care that
	// the ID parsed and the handler got as far as looking the backend up.
	b, err := d.Backends.Create(db.BackendInput{
		Name: "host-a", BaseURL: "http://127.0.0.1:1", Weight: 1, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatalf("create backend: %v", err)
	}

	h := NewAdminHandler(d, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/backends/"+strconv.FormatInt(b.ID, 10)+"/health", nil)
	rec := httptest.NewRecorder()
	h.HandleBackendHealth(rec, req)

	if rec.Code == http.StatusBadRequest && strings.Contains(rec.Body.String(), "invalid ID") {
		t.Fatalf("health refresh rejected the ID: %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if rec.Code == http.StatusNotFound {
		t.Fatalf("handler parsed an ID that did not resolve to the backend: %s", strings.TrimSpace(rec.Body.String()))
	}
	// 502 is the expected outcome here: ID resolved, backend found, connection refused.
	if rec.Code != http.StatusBadGateway {
		t.Logf("note: got %d (%s)", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

// A malformed ID must still be rejected.
func TestBackendHealthRejectsNonNumericID(t *testing.T) {
	d, err := db.New(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	h := NewAdminHandler(d, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/backends/not-a-number/health", nil)
	rec := httptest.NewRecorder()
	h.HandleBackendHealth(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a non-numeric ID, got %d", rec.Code)
	}
}
