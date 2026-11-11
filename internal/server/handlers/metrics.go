package handlers

import (
	"fmt"
	"net/http"
	"strings"
)

// HandleMetrics returns a Prometheus-compatible text metrics snapshot.
//
// Prometheus scrape_config example:
//
//   - job_name: relai
//     static_configs:
//   - targets: ['relai:8080']
//     authorization:
//     credentials: "<RELAI_ADMIN_KEY>"
func (h *AdminHandler) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	m, err := h.db.Logs.MetricsData()
	if err != nil {
		writeJSONError(w, "failed to collect metrics", http.StatusInternalServerError)
		return
	}
	queueDepth := len(h.queue.Snapshot())

	var b strings.Builder

	promMetric(&b, "relai_requests_total", "counter", "Total proxy requests handled")
	fmt.Fprintf(&b, "relai_requests_total %d\n\n", m.TotalRequests)

	promMetric(&b, "relai_errors_total", "counter", "Proxy requests returning 4xx or 5xx")
	fmt.Fprintf(&b, "relai_errors_total %d\n\n", m.TotalErrors)

	promMetric(&b, "relai_tokens_input_total", "counter", "Total input tokens processed")
	fmt.Fprintf(&b, "relai_tokens_input_total %d\n\n", m.InputTokens)

	promMetric(&b, "relai_tokens_output_total", "counter", "Total output tokens generated")
	fmt.Fprintf(&b, "relai_tokens_output_total %d\n\n", m.OutputTokens)

	promMetric(&b, "relai_tokens_thinking_total", "counter", "Total thinking tokens")
	fmt.Fprintf(&b, "relai_tokens_thinking_total %d\n\n", m.ThinkingTokens)

	promMetric(&b, "relai_cost_dollars_total", "counter", "Total cost in USD across all requests")
	fmt.Fprintf(&b, "relai_cost_dollars_total %.6f\n\n", m.CostDollars)

	promMetric(&b, "relai_queue_depth", "gauge", "Number of currently in-flight requests")
	fmt.Fprintf(&b, "relai_queue_depth %d\n\n", queueDepth)

	if len(m.BackendMetrics) > 0 {
		promMetric(&b, "relai_backend_requests_total", "counter", "Total requests per backend")
		for _, bm := range m.BackendMetrics {
			fmt.Fprintf(&b, "relai_backend_requests_total{backend=%q} %d\n", bm.Name, bm.Requests)
		}
		b.WriteByte('\n')

		promMetric(&b, "relai_backend_errors_total", "counter", "Total errors per backend")
		for _, bm := range m.BackendMetrics {
			fmt.Fprintf(&b, "relai_backend_errors_total{backend=%q} %d\n", bm.Name, bm.Errors)
		}
		b.WriteByte('\n')

		promMetric(&b, "relai_backend_up", "gauge", "Backend health status (1=active, 0=not active)")
		for _, bm := range m.BackendMetrics {
			up := 0
			if bm.Status == "active" {
				up = 1
			}
			fmt.Fprintf(&b, "relai_backend_up{backend=%q} %d\n", bm.Name, up)
		}
		b.WriteByte('\n')
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(b.String()))
}

func promMetric(b *strings.Builder, name, typ, help string) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
}
