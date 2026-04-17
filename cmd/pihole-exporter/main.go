package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/alantoch/pihole-exporter/pkg/exporter"
	"github.com/alantoch/pihole-exporter/pkg/pihole"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	defaultListenAddr = ":9617"
	defaultTimeout    = 10 * time.Second
)

func main() {
	cfg := parseConfig()

	client, err := pihole.NewAuthClient(cfg.piHoleURL, cfg.password)
	if err != nil {
		log.Fatalf("create Pi-hole client: %v", err)
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(exporter.NewCollector(client, cfg.timeout))

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	server := &http.Server{
		Addr:              cfg.listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("pihole-exporter listening on %s, compiled for Pi-hole API %s", cfg.listenAddr, pihole.CompiledPiHoleAPIVersion)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}

type config struct {
	listenAddr string
	piHoleURL  string
	password   string
	timeout    time.Duration
}

func parseConfig() config {
	listenAddr := flag.String("listen", envOrDefault("LISTEN_ADDR", defaultListenAddr), "HTTP listen address")
	piHoleURL := flag.String("pihole-url", envOrDefault("PIHOLE_BASE_URL", ""), "Pi-hole base URL e.g. http://pi.hole:8080")
	password := flag.String("password", envOrDefault(pihole.DefaultAppPasswordEnv, ""), "Pi-hole app password generated in Pi-hole settings")
	timeout := flag.Duration("timeout", durationEnvOrDefault("SCRAPE_TIMEOUT", defaultTimeout), "Pi-hole scrape timeout")
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

	return config{
		listenAddr: *listenAddr,
		piHoleURL:  *piHoleURL,
		password:   *password,
		timeout:    *timeout,
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
