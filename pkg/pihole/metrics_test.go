package pihole

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCollectMetricsFetchesStatsSummary(t *testing.T) {
	requests := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests[r.URL.Path]++

		switch r.URL.Path {
		case "/api/auth":
			writeAuthResponse(t, w, "sid-1", "csrf-1", 300)
		case "/api/stats/summary":
			if got := r.Header.Get("X-FTL-SID"); got != "sid-1" {
				t.Fatalf("X-FTL-SID = %q, want sid-1", got)
			}
			writeJSON(t, w, map[string]any{
				"queries": map[string]any{
					"total":           100,
					"blocked":         25,
					"percent_blocked": 25.0,
					"unique_domains":  42,
					"forwarded":       50,
					"cached":          25,
					"frequency":       1.5,
					"types": map[string]any{
						"A":     70,
						"AAAA":  20,
						"HTTPS": 10,
					},
					"status": map[string]any{
						"GRAVITY":   25,
						"FORWARDED": 50,
						"CACHE":     25,
					},
					"replies": map[string]any{
						"IP":     80,
						"NODATA": 20,
					},
				},
				"clients": map[string]any{
					"active": 5,
					"total":  8,
				},
				"gravity": map[string]any{
					"domains_being_blocked": 123456,
					"last_update":           1725194639,
				},
				"took": 0.001,
			})
		case "/api/stats/query_types":
			writeJSON(t, w, map[string]any{
				"types": map[string]any{
					"A":    10,
					"AAAA": 5,
				},
				"took": 0.001,
			})
		case "/api/stats/top_clients", "/api/stats/top_domains":
			writeJSON(t, w, map[string]any{
				"total_queries":   300,
				"blocked_queries": 60,
				"clients":         []map[string]any{},
				"domains":         []map[string]any{},
				"took":            0.001,
			})
		case "/api/stats/upstreams":
			writeJSON(t, w, map[string]any{
				"forwarded_queries": 200,
				"total_queries":     300,
				"upstreams":         []map[string]any{},
				"took":              0.001,
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewAuthClient(server.URL, "app-password")
	if err != nil {
		t.Fatalf("NewAuthClient() error = %v", err)
	}

	metrics, err := client.CollectMetrics(context.Background())
	if err != nil {
		t.Fatalf("CollectMetrics() error = %v", err)
	}

	if requests["/api/auth"] != 1 {
		t.Fatalf("auth requests = %d, want 1", requests["/api/auth"])
	}
	for _, spec := range CompiledMetricSpecs() {
		path := "/api" + spec.Endpoint
		if requests[path] != 1 {
			t.Fatalf("%s requests = %d, want 1", path, requests[path])
		}
	}

	assertMetric(t, metrics, "pihole_summary_queries_total", nil, 100)
	assertMetric(t, metrics, "pihole_summary_queries_percent_blocked", nil, 25)
	assertMetric(t, metrics, "pihole_summary_clients_active", nil, 5)
	assertMetric(t, metrics, "pihole_summary_gravity_last_update_timestamp_seconds", nil, 1725194639)
	assertMetric(t, metrics, "pihole_summary_queries_by_type", map[string]string{"type": "A"}, 70)
	assertMetric(t, metrics, "pihole_summary_queries_by_status", map[string]string{"status": "GRAVITY"}, 25)
	assertMetric(t, metrics, "pihole_summary_queries_by_reply", map[string]string{"reply": "IP"}, 80)
	assertMetric(t, metrics, "pihole_upstreams_forwarded_queries", nil, 200)
	assertMetric(t, metrics, "pihole_query_types_by_type", map[string]string{"type": "AAAA"}, 5)
}

func TestCompiledMetricSpecsAreCopied(t *testing.T) {
	specs := CompiledMetricSpecs()
	if len(specs) == 0 {
		t.Fatal("CompiledMetricSpecs() returned no specs")
	}

	specs[0].Name = "mutated"
	if CompiledMetricSpecs()[0].Name == "mutated" {
		t.Fatal("CompiledMetricSpecs() returned mutable backing storage")
	}
}

func assertMetric(t *testing.T, metrics []Metric, name string, labels map[string]string, want float64) {
	t.Helper()

	for _, metric := range metrics {
		if metric.Name != name || labelString(metric.Labels) != labelString(labels) {
			continue
		}
		if metric.Value != want {
			t.Fatalf("%s{%s} = %v, want %v", name, labelString(labels), metric.Value, want)
		}
		return
	}

	t.Fatalf("metric %s{%s} not found in %#v", name, labelString(labels), metrics)
}

func writeJSON(t *testing.T, w http.ResponseWriter, value map[string]any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("write response: %v", err)
	}
}
