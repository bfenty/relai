// Package influx exports routing health to InfluxDB v2.
//
// Relai already records cache reuse, cold arrivals and compression per request, but
// only keeps them queryable through its own analytics endpoints. Pushing them to
// InfluxDB puts them next to whatever else is being collected about the host, and
// keeps history beyond what the request log is pruned to.
package influx

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bfenty/relai/internal/db"
)

// Exporter periodically writes routing metrics for the window since its last write.
// Configuration is re-read each tick, so enabling or retargeting the export takes
// effect without a restart.
type Exporter struct {
	db     *db.DB
	client *http.Client
	last   time.Time
}

func New(d *db.DB) *Exporter {
	return &Exporter{
		db:     d,
		client: &http.Client{Timeout: 10 * time.Second},
		last:   time.Now(),
	}
}

// Run ticks until ctx is cancelled. It polls at a fixed short interval and writes
// only when the configured export interval has elapsed, so a settings change is
// picked up promptly without pinning the write cadence to it.
func (e *Exporter) Run(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cfg, err := e.db.Settings.GetInflux()
			if err != nil || !cfg.Enabled || cfg.Token == "" {
				continue
			}
			if time.Since(e.last) < time.Duration(cfg.IntervalSeconds)*time.Second {
				continue
			}
			if err := e.exportSince(ctx, cfg, e.last); err != nil {
				// A failing export must never take traffic down with it, so this is
				// logged and retried on the next tick rather than escalated.
				log.Printf("influx export failed: %v", err)
			}
			e.last = time.Now()
		}
	}
}

func (e *Exporter) exportSince(ctx context.Context, cfg db.InfluxSettings, from time.Time) error {
	to := time.Now()
	stats, err := e.db.RoutingAnalytics(from, to)
	if err != nil {
		return fmt.Errorf("collect: %w", err)
	}
	if stats.Total == 0 {
		return nil // nothing happened; do not write an empty point
	}
	return e.Write(ctx, cfg, LineProtocol(stats, to))
}

// LineProtocol renders routing stats as InfluxDB line protocol: one overall point
// plus one per host, so a dashboard can show the cluster or split it by machine.
func LineProtocol(s *db.RoutingStats, at time.Time) string {
	ts := at.UnixNano()
	var b strings.Builder

	fmt.Fprintf(&b, "relai_routing requests=%di,measured=%di,cache_reuse_rate=%.6f,"+
		"prompt_tokens=%di,cached_tokens=%di,reprocessed_tokens=%di,"+
		"sticky_hit_rate=%.6f,spill_rate=%.6f,mean_wait_ms=%di,p95_wait_ms=%di,"+
		"compressed_requests=%di,compression_bytes_saved=%di %d\n",
		s.Total, s.MeasuredRequests, s.CacheReuseRate,
		s.PromptTokens, s.CachedTokens, s.ReprocessedTokens,
		s.StickyHitRate, s.SpillRate, s.MeanWaitMs, s.P95WaitMs,
		s.CompressedRequests, s.BytesSaved, ts)

	for _, h := range s.Hosts {
		fmt.Fprintf(&b, "relai_routing_host,host=%s requests=%di,share=%.6f,"+
			"cache_reuse_rate=%.6f,mean_latency_ms=%di,compression_bytes_saved=%di %d\n",
			escapeTag(h.BackendName), h.Requests, h.Share,
			h.CacheReuseRate, h.MeanLatencyMs, h.BytesSaved, ts)
	}
	return b.String()
}

// escapeTag escapes the characters line protocol treats as structural in a tag value.
func escapeTag(s string) string {
	if s == "" {
		return "unknown"
	}
	r := strings.NewReplacer(",", `\,`, " ", `\ `, "=", `\=`)
	return r.Replace(s)
}

// Write posts line protocol to InfluxDB. Exported so the settings UI can verify a
// configuration before enabling it.
func (e *Exporter) Write(ctx context.Context, cfg db.InfluxSettings, body string) error {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	endpoint := fmt.Sprintf("%s/api/v2/write?bucket=%s&precision=ns",
		strings.TrimRight(cfg.URL, "/"), cfg.Bucket)
	if cfg.Org != "" {
		endpoint += "&org=" + cfg.Org
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader([]byte(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token "+cfg.Token)
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")

	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		buf := make([]byte, 400)
		n, _ := resp.Body.Read(buf)
		return fmt.Errorf("influxdb returned %d: %s", resp.StatusCode, strings.TrimSpace(string(buf[:n])))
	}
	return nil
}
