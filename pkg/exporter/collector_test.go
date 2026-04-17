package exporter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alantoch/pihole-exporter/pkg/pihole"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestCollectorEmitsScrapeHealthMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth":
			writeJSON(t, w, map[string]any{
				"session": map[string]any{
					"valid":    true,
					"totp":     false,
					"sid":      "sid-1",
					"csrf":     "csrf-1",
					"validity": 300,
				},
			})
		case "/api/stats/query_types":
			writeJSON(t, w, map[string]any{"types": map[string]any{"A": 1}})
		case "/api/stats/summary":
			writeJSON(t, w, map[string]any{
				"queries": map[string]any{
					"total":           1,
					"blocked":         0,
					"percent_blocked": 0,
					"unique_domains":  1,
					"forwarded":       1,
					"cached":          0,
					"frequency":       1,
					"types":           map[string]any{"A": 1},
					"status":          map[string]any{"FORWARDED": 1},
					"replies":         map[string]any{"IP": 1},
				},
				"clients": map[string]any{
					"active": 1,
					"total":  1,
				},
				"gravity": map[string]any{
					"domains_being_blocked": 1,
					"last_update":           1,
				},
			})
		case "/api/stats/top_clients", "/api/stats/top_domains":
			writeJSON(t, w, map[string]any{
				"total_queries":   1,
				"blocked_queries": 0,
			})
		case "/api/stats/upstreams":
			writeJSON(t, w, map[string]any{
				"forwarded_queries": 1,
				"total_queries":     1,
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := pihole.NewAuthClient(server.URL, "app-password")
	if err != nil {
		t.Fatalf("NewAuthClient() error = %v", err)
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(NewCollector(client, 5*time.Second))

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	if got := metricValue(t, families, "pihole_exporter_scrape_success"); got != 1 {
		t.Fatalf("pihole_exporter_scrape_success = %v, want 1", got)
	}
	if got := metricValue(t, families, "pihole_exporter_scrape_duration_seconds"); got < 0 {
		t.Fatalf("pihole_exporter_scrape_duration_seconds = %v, want >= 0", got)
	}
}

func metricValue(t *testing.T, families []*dto.MetricFamily, name string) float64 {
	t.Helper()

	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		metrics := family.GetMetric()
		if len(metrics) != 1 {
			t.Fatalf("%s has %d metrics, want 1", name, len(metrics))
		}
		return metrics[0].GetGauge().GetValue()
	}

	t.Fatalf("metric %s not found", name)
	return 0
}

func writeJSON(t *testing.T, w http.ResponseWriter, value map[string]any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("write response: %v", err)
	}
}
