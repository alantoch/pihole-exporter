package main

import (
	"flag"
	"io"
	"os"
	"testing"

	"github.com/alantoch/pihole-exporter/pkg/pihole"
)

func TestParseConfigUnsetsPasswordEnvByDefault(t *testing.T) {
	t.Setenv("PIHOLE_BASE_URL", "http://127.0.0.1:8080")
	t.Setenv(pihole.DefaultAppPasswordEnv, "app-password")

	withTestCommandLine(t, "pihole-exporter")

	cfg := parseConfig()

	if cfg.password != "app-password" {
		t.Fatalf("password = %q, want app-password", cfg.password)
	}
	if _, ok := os.LookupEnv(pihole.DefaultAppPasswordEnv); ok {
		t.Fatalf("%s is still set", pihole.DefaultAppPasswordEnv)
	}
}

func TestParseConfigDefaultsToPrometheusMetricsExporter(t *testing.T) {
	t.Setenv("PIHOLE_BASE_URL", "http://127.0.0.1:8080")
	t.Setenv(pihole.DefaultAppPasswordEnv, "app-password")

	withTestCommandLine(t, "pihole-exporter")

	cfg := parseConfig()

	if cfg.metricsExporter != metricsExporterPrometheus {
		t.Fatalf("metricsExporter = %q, want %q", cfg.metricsExporter, metricsExporterPrometheus)
	}
}

func TestParseConfigReadsOpenTelemetryMetricsExporterEnv(t *testing.T) {
	t.Setenv("PIHOLE_BASE_URL", "http://127.0.0.1:8080")
	t.Setenv(pihole.DefaultAppPasswordEnv, "app-password")
	t.Setenv("OTEL_METRICS_EXPORTER", " OTLPHTTP ")

	withTestCommandLine(t, "pihole-exporter")

	cfg := parseConfig()

	if cfg.metricsExporter != metricsExporterOTLPHTTP {
		t.Fatalf("metricsExporter = %q, want %q", cfg.metricsExporter, metricsExporterOTLPHTTP)
	}
}

func withTestCommandLine(t *testing.T, args ...string) {
	t.Helper()

	oldArgs := os.Args
	oldCommandLine := flag.CommandLine

	os.Args = args
	flag.CommandLine = flag.NewFlagSet(args[0], flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)

	t.Cleanup(func() {
		os.Args = oldArgs
		flag.CommandLine = oldCommandLine
	})
}
