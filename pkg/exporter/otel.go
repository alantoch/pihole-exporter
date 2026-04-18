package exporter

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/alantoch/pihole-exporter/pkg/pihole"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

const (
	otelScopeName = "github.com/alantoch/pihole-exporter"

	otelExporterOTLP     = "otlp"
	otelExporterOTLPGRPC = "otlpgrpc"
	otelExporterOTLPHTTP = "otlphttp"
	otelExporterStdout   = "stdout"
)

// NewOpenTelemetryMeterProvider builds a periodic OpenTelemetry metrics pipeline
// for push-style exporters. Prometheus keeps using the native collector because
// it is a pull exporter and already has stable /metrics behavior.
func NewOpenTelemetryMeterProvider(ctx context.Context, client *pihole.AuthClient, timeout time.Duration, exporterName string) (*sdkmetric.MeterProvider, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	reader, err := newOpenTelemetryReader(ctx, exporterName)
	if err != nil {
		return nil, err
	}

	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	meter := provider.Meter(otelScopeName)
	if err := registerOpenTelemetryMetrics(meter, client, timeout); err != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = provider.Shutdown(shutdownCtx)
		return nil, err
	}

	return provider, nil
}

func newOpenTelemetryReader(ctx context.Context, exporterName string) (sdkmetric.Reader, error) {
	var metricExporter sdkmetric.Exporter
	var err error

	switch exporterName {
	case otelExporterOTLP:
		metricExporter, err = newOTLPMetricExporter(ctx)
	case otelExporterOTLPGRPC:
		metricExporter, err = otlpmetricgrpc.New(ctx)
	case otelExporterOTLPHTTP:
		metricExporter, err = otlpmetrichttp.New(ctx)
	case otelExporterStdout:
		metricExporter, err = stdoutmetric.New(stdoutmetric.WithPrettyPrint())
	default:
		return nil, fmt.Errorf("unsupported OpenTelemetry metrics exporter %q", exporterName)
	}
	if err != nil {
		return nil, err
	}

	return sdkmetric.NewPeriodicReader(metricExporter, periodicReaderOptions()...), nil
}

func newOTLPMetricExporter(ctx context.Context) (sdkmetric.Exporter, error) {
	switch otlpProtocol() {
	case "", "grpc":
		return otlpmetricgrpc.New(ctx)
	case "http/protobuf":
		return otlpmetrichttp.New(ctx)
	default:
		return nil, fmt.Errorf("unsupported OTLP metrics protocol %q; supported values are grpc and http/protobuf", otlpProtocol())
	}
}

func otlpProtocol() string {
	if value := os.Getenv("OTEL_EXPORTER_OTLP_METRICS_PROTOCOL"); value != "" {
		return value
	}
	return os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")
}

func periodicReaderOptions() []sdkmetric.PeriodicReaderOption {
	var options []sdkmetric.PeriodicReaderOption

	if interval, ok := otelDurationFromMilliseconds("OTEL_METRIC_EXPORT_INTERVAL"); ok {
		options = append(options, sdkmetric.WithInterval(interval))
	}
	if timeout, ok := otelDurationFromMilliseconds("OTEL_METRIC_EXPORT_TIMEOUT"); ok {
		options = append(options, sdkmetric.WithTimeout(timeout))
	}

	return options
}

func otelDurationFromMilliseconds(key string) (time.Duration, bool) {
	value := os.Getenv(key)
	if value == "" {
		return 0, false
	}

	milliseconds, err := strconv.Atoi(value)
	if err != nil || milliseconds <= 0 {
		log.Printf("invalid %s=%q, using OpenTelemetry default", key, value)
		return 0, false
	}

	return time.Duration(milliseconds) * time.Millisecond, true
}

func registerOpenTelemetryMetrics(meter metric.Meter, client *pihole.AuthClient, timeout time.Duration) error {
	instruments := make(map[string]metric.Float64ObservableGauge)
	observables := make([]metric.Observable, 0, len(pihole.CompiledMetricSpecs())+3)

	for _, spec := range pihole.CompiledMetricSpecs() {
		if _, ok := instruments[spec.Name]; ok {
			continue
		}

		gauge, err := meter.Float64ObservableGauge(spec.Name, metric.WithDescription(spec.Help))
		if err != nil {
			return err
		}
		instruments[spec.Name] = gauge
		observables = append(observables, gauge)
	}

	buildInfoGauge, err := meter.Float64ObservableGauge(
		"pihole_exporter_build_info",
		metric.WithDescription("Build information for the Pi-hole exporter."),
	)
	if err != nil {
		return err
	}
	scrapeSuccessGauge, err := meter.Float64ObservableGauge(
		"pihole_exporter_scrape_success",
		metric.WithDescription("Whether the last Pi-hole scrape succeeded."),
	)
	if err != nil {
		return err
	}
	scrapeDurationGauge, err := meter.Float64ObservableGauge(
		"pihole_exporter_scrape_duration_seconds",
		metric.WithDescription("Duration of the last Pi-hole scrape in seconds."),
	)
	if err != nil {
		return err
	}
	observables = append(observables, buildInfoGauge, scrapeSuccessGauge, scrapeDurationGauge)

	_, err = meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		observer.ObserveFloat64(
			buildInfoGauge,
			1,
			metric.WithAttributes(attribute.String("pihole_api_version", pihole.CompiledPiHoleAPIVersion)),
		)

		scrapeCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		start := time.Now()
		metrics, err := client.CollectMetrics(scrapeCtx)
		duration := time.Since(start).Seconds()

		observer.ObserveFloat64(scrapeDurationGauge, duration)
		if err != nil {
			observer.ObserveFloat64(scrapeSuccessGauge, 0)
			log.Printf("scrape Pi-hole metrics for OpenTelemetry exporter: %v", err)
			return nil
		}
		observer.ObserveFloat64(scrapeSuccessGauge, 1)

		for _, collectedMetric := range metrics {
			gauge, ok := instruments[collectedMetric.Name]
			if !ok {
				continue
			}
			observer.ObserveFloat64(
				gauge,
				collectedMetric.Value,
				metric.WithAttributes(openTelemetryAttributes(collectedMetric.Labels)...),
			)
		}

		return nil
	}, observables...)
	if err != nil {
		return err
	}

	return nil
}

func openTelemetryAttributes(labels map[string]string) []attribute.KeyValue {
	if len(labels) == 0 {
		return nil
	}

	names := make([]string, 0, len(labels))
	for name := range labels {
		names = append(names, name)
	}
	sort.Strings(names)

	attributes := make([]attribute.KeyValue, 0, len(names))
	for _, name := range names {
		attributes = append(attributes, attribute.String(name, labels[name]))
	}
	return attributes
}
