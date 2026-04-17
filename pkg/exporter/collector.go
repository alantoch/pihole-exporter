package exporter

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/alantoch/pihole-exporter/pkg/pihole"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	buildInfoDesc = prometheus.NewDesc(
		"pihole_exporter_build_info",
		"Build information for the Pi-hole exporter.",
		nil,
		prometheus.Labels{"pihole_api_version": pihole.CompiledPiHoleAPIVersion},
	)
	scrapeSuccessDesc = prometheus.NewDesc(
		"pihole_exporter_scrape_success",
		"Whether the last Pi-hole scrape succeeded.",
		nil,
		nil,
	)
	scrapeDurationDesc = prometheus.NewDesc(
		"pihole_exporter_scrape_duration_seconds",
		"Duration of the last Pi-hole scrape in seconds.",
		nil,
		nil,
	)
)

type Collector struct {
	client  *pihole.AuthClient
	timeout time.Duration

	mu    sync.Mutex
	descs map[string]*prometheus.Desc
}

func NewCollector(client *pihole.AuthClient, timeout time.Duration) *Collector {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	return &Collector{
		client:  client,
		timeout: timeout,
		descs:   make(map[string]*prometheus.Desc),
	}
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	for _, spec := range pihole.CompiledMetricSpecs() {
		ch <- c.desc(spec.Name, spec.Help, labelNames(spec))
	}

	ch <- buildInfoDesc
	ch <- scrapeSuccessDesc
	ch <- scrapeDurationDesc
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(
		buildInfoDesc,
		prometheus.GaugeValue,
		1,
	)

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	start := time.Now()
	metrics, err := c.client.CollectMetrics(ctx)
	duration := time.Since(start).Seconds()
	if err != nil {
		ch <- prometheus.MustNewConstMetric(scrapeSuccessDesc, prometheus.GaugeValue, 0)
		ch <- prometheus.MustNewConstMetric(scrapeDurationDesc, prometheus.GaugeValue, duration)
		ch <- prometheus.NewInvalidMetric(
			prometheus.NewDesc("pihole_exporter_scrape_error", "Pi-hole scrape error.", nil, nil),
			err,
		)
		return
	}

	ch <- prometheus.MustNewConstMetric(scrapeSuccessDesc, prometheus.GaugeValue, 1)
	ch <- prometheus.MustNewConstMetric(scrapeDurationDesc, prometheus.GaugeValue, duration)

	for _, metric := range metrics {
		valueType, err := prometheusValueType(metric.Kind)
		if err != nil {
			ch <- prometheus.NewInvalidMetric(
				prometheus.NewDesc("pihole_exporter_metric_error", "Pi-hole metric conversion error.", nil, nil),
				err,
			)
			continue
		}

		labelNames, labelValues := labels(metric.Labels)
		ch <- prometheus.MustNewConstMetric(
			c.desc(metric.Name, metric.Help, labelNames),
			valueType,
			metric.Value,
			labelValues...,
		)
	}
}

func (c *Collector) desc(name, help string, labels []string) *prometheus.Desc {
	key := name + "\xff" + fmt.Sprint(labels)

	c.mu.Lock()
	defer c.mu.Unlock()

	if desc, ok := c.descs[key]; ok {
		return desc
	}

	desc := prometheus.NewDesc(name, help, labels, nil)
	c.descs[key] = desc
	return desc
}

func prometheusValueType(kind pihole.MetricKind) (prometheus.ValueType, error) {
	switch kind {
	case pihole.MetricKindGauge:
		return prometheus.GaugeValue, nil
	default:
		return prometheus.GaugeValue, fmt.Errorf("unsupported metric kind %q", kind)
	}
}

func labelNames(spec pihole.MetricSpec) []string {
	if spec.Label == "" {
		return nil
	}
	return []string{spec.Label}
}

func labels(values map[string]string) ([]string, []string) {
	if len(values) == 0 {
		return nil, nil
	}

	if len(values) == 1 {
		for name, value := range values {
			return []string{name}, []string{value}
		}
	}

	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}

	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}

	labelValues := make([]string, 0, len(names))
	for _, name := range names {
		labelValues = append(labelValues, values[name])
	}

	return names, labelValues
}
