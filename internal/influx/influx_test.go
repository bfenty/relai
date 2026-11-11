package influx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bfenty/relai/internal/db"
)

func sampleStats() *db.RoutingStats {
	return &db.RoutingStats{
		Total: 120, MeasuredRequests: 100,
		CacheReuseRate: 0.9312, StickyHitRate: 0.75, SpillRate: 0.05,
		PromptTokens: 2000000, CachedTokens: 1862400, ReprocessedTokens: 137600,
		MeanWaitMs: 12, P95WaitMs: 340,
		CompressedRequests: 8, BytesSaved: 40960,
		Hosts: []db.HostRoutingStats{
			{BackendName: "fedora-home", Requests: 90, Share: 0.75,
				CacheReuseRate: 0.95, MeanLatencyMs: 22314, BytesSaved: 40960},
			{BackendName: "fedora-2", Requests: 30, Share: 0.25,
				CacheReuseRate: 0.88, MeanLatencyMs: 18328},
		},
	}
}

func TestLineProtocolShape(t *testing.T) {
	at := time.Unix(1700000000, 0)
	out := LineProtocol(sampleStats(), at)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected one overall line plus one per host, got %d:\n%s", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "relai_routing ") {
		t.Errorf("first line is not the overall measurement: %s", lines[0])
	}
	for _, want := range []string{
		"requests=120i", "cache_reuse_rate=0.931200", "reprocessed_tokens=137600i",
		"compression_bytes_saved=40960i",
	} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("overall line missing %q:\n%s", want, lines[0])
		}
	}
	if !strings.Contains(lines[1], "relai_routing_host,host=fedora-home") {
		t.Errorf("host line missing its tag: %s", lines[1])
	}
	// Every line must carry the timestamp, or InfluxDB stamps them on arrival and
	// the series drifts from the window it describes.
	for i, l := range lines {
		if !strings.HasSuffix(l, "1700000000000000000") {
			t.Errorf("line %d has no nanosecond timestamp: %s", i, l)
		}
	}
}

// Host names are user-supplied and can contain the characters line protocol treats
// as structural. Unescaped, a name with a space silently corrupts the point.
func TestLineProtocolEscapesTags(t *testing.T) {
	s := sampleStats()
	s.Hosts = []db.HostRoutingStats{{BackendName: "my host,prod=1", Requests: 1}}
	out := LineProtocol(s, time.Unix(1, 0))
	if !strings.Contains(out, `host=my\ host\,prod\=1`) {
		t.Errorf("tag not escaped:\n%s", out)
	}
}

func TestLineProtocolHandlesMissingHostName(t *testing.T) {
	s := sampleStats()
	s.Hosts = []db.HostRoutingStats{{BackendName: "", Requests: 1}}
	if !strings.Contains(LineProtocol(s, time.Unix(1, 0)), "host=unknown") {
		t.Error("expected a placeholder tag for an unnamed host")
	}
}

func TestWritePostsLineProtocol(t *testing.T) {
	var gotPath, gotAuth, gotBody, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	e := New(nil)
	cfg := db.InfluxSettings{URL: srv.URL, Bucket: "relai", Org: "home", Token: "secret"}
	if err := e.Write(context.Background(), cfg, "relai_routing requests=1i 1\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if gotPath != "/api/v2/write" {
		t.Errorf("path = %q, want /api/v2/write", gotPath)
	}
	if !strings.Contains(gotQuery, "bucket=relai") || !strings.Contains(gotQuery, "org=home") ||
		!strings.Contains(gotQuery, "precision=ns") {
		t.Errorf("query missing parameters: %q", gotQuery)
	}
	if gotAuth != "Token secret" {
		t.Errorf("auth = %q, want Token secret", gotAuth)
	}
	if !strings.Contains(gotBody, "relai_routing requests=1i") {
		t.Errorf("body not forwarded: %q", gotBody)
	}
}

// A rejected write must surface InfluxDB's reason, not a bare status code - the
// usual causes (wrong org, missing bucket, bad token) are only distinguishable
// from the response body.
func TestWriteSurfacesUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"code":"not found","message":"bucket not found"}`))
	}))
	defer srv.Close()

	e := New(nil)
	err := e.Write(context.Background(), db.InfluxSettings{
		URL: srv.URL, Bucket: "missing", Token: "t"}, "x value=1i 1\n")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "bucket not found") {
		t.Errorf("error does not carry the upstream reason: %v", err)
	}
}

func TestWriteSkipsEmptyBody(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()
	e := New(nil)
	if err := e.Write(context.Background(), db.InfluxSettings{
		URL: srv.URL, Bucket: "b", Token: "t"}, "  \n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if called {
		t.Error("an empty body should not reach InfluxDB")
	}
}
