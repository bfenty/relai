package db

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Every authenticated request takes a write transaction via CheckRateLimit. SQLite
// permits one writer at a time even in WAL mode, so without a busy timeout concurrent
// requests on a single API key fail immediately with SQLITE_BUSY - surfaced to callers
// as 500 "rate limit check failed".
func TestCheckRateLimitUnderConcurrency(t *testing.T) {
	dir := t.TempDir()
	d, err := New(filepath.Join(dir, "rl.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	key, _, err := d.APIKeys.Create(APIKeyInput{Name: "conc", RateLimitRPM: 100000})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}

	const workers = 16
	var wg sync.WaitGroup
	errs := make([]error, workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // release them together to maximise write contention
			_, err := d.APIKeys.CheckRateLimit(key.ID)
			errs[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("CheckRateLimit failed under concurrency (worker %d): %v", i, err)
		}
	}
}

// The DSN must carry the pragmas so every pooled connection gets them, not just
// whichever one a conn.Exec happened to land on.
func TestSQLiteDSNCarriesPragmas(t *testing.T) {
	dsn := sqliteDSN("/tmp/x.db")
	for _, want := range []string{"busy_timeout", "journal_mode(WAL)", "foreign_keys(ON)", "_txlock=immediate"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("DSN %q missing %s", dsn, want)
		}
	}
	// A path that already has query params must keep them.
	dsn2 := sqliteDSN("/tmp/x.db?cache=shared")
	if !strings.Contains(dsn2, "cache=shared") || !strings.Contains(dsn2, "busy_timeout") {
		t.Errorf("DSN with existing params malformed: %q", dsn2)
	}
}
