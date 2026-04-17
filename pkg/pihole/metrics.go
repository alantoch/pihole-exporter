package pihole

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type MetricKind string

const (
	MetricKindGauge MetricKind = "gauge"
)

type MetricSpec struct {
	Name     string
	Help     string
	Kind     MetricKind
	Endpoint string
	Path     []string
	Label    string
}

type Metric struct {
	Name   string
	Help   string
	Kind   MetricKind
	Labels map[string]string
	Value  float64
}

func CompiledMetricSpecs() []MetricSpec {
	specs := make([]MetricSpec, len(compiledMetricSpecs))
	copy(specs, compiledMetricSpecs)
	return specs
}

func (c *AuthClient) CollectMetrics(ctx context.Context) ([]Metric, error) {
	specsByEndpoint := make(map[string][]MetricSpec)
	for _, spec := range compiledMetricSpecs {
		specsByEndpoint[spec.Endpoint] = append(specsByEndpoint[spec.Endpoint], spec)
	}

	endpoints := make([]string, 0, len(specsByEndpoint))
	for endpoint := range specsByEndpoint {
		endpoints = append(endpoints, endpoint)
	}
	sort.Strings(endpoints)

	var metrics []Metric
	for _, endpoint := range endpoints {
		body, err := c.GetJSONMap(ctx, endpoint)
		if err != nil {
			return nil, err
		}

		for _, spec := range specsByEndpoint[endpoint] {
			extracted, err := extractMetric(body, spec)
			if err != nil {
				return nil, err
			}
			metrics = append(metrics, extracted...)
		}
	}

	sort.Slice(metrics, func(i, j int) bool {
		if metrics[i].Name != metrics[j].Name {
			return metrics[i].Name < metrics[j].Name
		}
		return labelString(metrics[i].Labels) < labelString(metrics[j].Labels)
	})

	return metrics, nil
}

func extractMetric(root map[string]any, spec MetricSpec) ([]Metric, error) {
	value, ok := lookup(root, spec.Path)
	if !ok {
		return nil, nil
	}

	if spec.Label != "" {
		values, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("metric %s path %s is not an object", spec.Name, strings.Join(spec.Path, "."))
		}

		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		metrics := make([]Metric, 0, len(keys))
		for _, key := range keys {
			number, ok := numberValue(values[key])
			if !ok {
				continue
			}
			metrics = append(metrics, Metric{
				Name:   spec.Name,
				Help:   spec.Help,
				Kind:   spec.Kind,
				Labels: map[string]string{spec.Label: key},
				Value:  number,
			})
		}
		return metrics, nil
	}

	number, ok := numberValue(value)
	if !ok {
		return nil, fmt.Errorf("metric %s path %s is not numeric", spec.Name, strings.Join(spec.Path, "."))
	}

	return []Metric{{
		Name:  spec.Name,
		Help:  spec.Help,
		Kind:  spec.Kind,
		Value: number,
	}}, nil
}

func lookup(root map[string]any, path []string) (any, bool) {
	var current any = root
	for _, part := range path {
		obj, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = obj[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func numberValue(value any) (float64, bool) {
	switch v := value.(type) {
	case json.Number:
		number, err := v.Float64()
		return number, err == nil
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	default:
		return 0, false
	}
}

func labelString(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}

	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+labels[key])
	}
	return strings.Join(parts, ",")
}
