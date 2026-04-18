package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/alantoch/pihole-exporter/pkg/exporter"
	"github.com/alantoch/pihole-exporter/pkg/pihole"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	defaultListenAddr      = ":9617"
	defaultTimeout         = 10 * time.Second
	defaultMetricsExporter = "prometheus"
)

func main() {
	cfg := parseConfig()

	client, err := pihole.NewAuthClient(cfg.piHoleURL, cfg.password)
	if err != nil {
		log.Fatalf("create Pi-hole client: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	switch cfg.metricsExporter {
	case metricsExporterPrometheus:
		registry := prometheus.NewRegistry()
		registry.MustRegister(exporter.NewCollector(client, cfg.timeout))
		mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	case metricsExporterOTLP, metricsExporterOTLPGRPC, metricsExporterOTLPHTTP, metricsExporterStdout:
		provider, err := exporter.NewOpenTelemetryMeterProvider(context.Background(), client, cfg.timeout, string(cfg.metricsExporter))
		if err != nil {
			log.Fatalf("create %s metrics exporter: %v", cfg.metricsExporter, err)
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := provider.Shutdown(ctx); err != nil {
				log.Printf("shutdown metrics exporter: %v", err)
			}
		}()
	default:
		log.Fatalf("unsupported metrics exporter %q", cfg.metricsExporter)
	}

	server := &http.Server{
		Addr:              cfg.listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("pihole-exporter listening on %s, metrics exporter %s, compiled for Pi-hole API %s", cfg.listenAddr, cfg.metricsExporter, pihole.CompiledPiHoleAPIVersion)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}

type config struct {
	listenAddr      string
	piHoleURL       string
	password        string
	timeout         time.Duration
	metricsExporter metricsExporter
}

func parseConfig() config {
	listenAddr := flag.String("listen", envOrDefault("LISTEN_ADDR", defaultListenAddr), "HTTP listen address")
	piHoleURL := flag.String("pihole-url", envOrDefault("PIHOLE_BASE_URL", ""), "Pi-hole base URL e.g. http://pi.hole:8080")
	password := flag.String("password", envOrDefault(pihole.DefaultAppPasswordEnv, ""), "Pi-hole app password generated in Pi-hole settings")
	timeout := flag.Duration("timeout", durationEnvOrDefault("SCRAPE_TIMEOUT", defaultTimeout), "Pi-hole scrape timeout")
	metricsExporterValue := flag.String("metrics-exporter", envOrDefault("OTEL_METRICS_EXPORTER", defaultMetricsExporter), "metrics exporter: prometheus, otlp, otlpgrpc, otlphttp, or stdout")
	flag.Parse()

	if *piHoleURL == "" {
		fatalUsage("PIHOLE_BASE_URL or -pihole-url is required")
	}
	if *password == "" {
		fatalUsage("%s or -password is required", pihole.DefaultAppPasswordEnv)
	}
	if err := os.Unsetenv(pihole.DefaultAppPasswordEnv); err != nil {
		log.Printf("failed to unset %s: %v", pihole.DefaultAppPasswordEnv, err)
	}

	metricsExporter, err := parseMetricsExporter(*metricsExporterValue)
	if err != nil {
		fatalUsage("%v", err)
	}

	return config{
		listenAddr:      *listenAddr,
		piHoleURL:       *piHoleURL,
		password:        *password,
		timeout:         *timeout,
		metricsExporter: metricsExporter,
	}
}

type metricsExporter string

const (
	metricsExporterPrometheus metricsExporter = "prometheus"
	metricsExporterOTLP       metricsExporter = "otlp"
	metricsExporterOTLPGRPC   metricsExporter = "otlpgrpc"
	metricsExporterOTLPHTTP   metricsExporter = "otlphttp"
	metricsExporterStdout     metricsExporter = "stdout"
)

func parseMetricsExporter(value string) (metricsExporter, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))

	switch metricsExporter(normalized) {
	case "", metricsExporterPrometheus:
		return metricsExporterPrometheus, nil
	case metricsExporterOTLP, metricsExporterOTLPGRPC, metricsExporterOTLPHTTP, metricsExporterStdout:
		return metricsExporter(normalized), nil
	default:
		return "", fmt.Errorf("unsupported metrics exporter %q; supported values are prometheus, otlp, otlpgrpc, otlphttp, stdout", value)
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func durationEnvOrDefault(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	duration, err := time.ParseDuration(value)
	if err == nil {
		return duration
	}

	seconds, err := strconv.Atoi(value)
	if err == nil {
		return time.Duration(seconds) * time.Second
	}

	log.Printf("invalid %s=%q, using %s", key, value, fallback)
	return fallback
}

func fatalUsage(format string, args ...any) {
	_, _ = fmt.Fprintf(flag.CommandLine.Output(), format+"\n\n", args...)
	flag.Usage()
	os.Exit(2)
}
