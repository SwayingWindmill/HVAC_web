package observability

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var forbiddenMetricLabels = map[string]struct{}{
	"session": {}, "session_id": {}, "principal": {}, "principal_id": {}, "subject": {},
	"request": {}, "request_id": {}, "trace": {}, "trace_id": {}, "span_id": {},
	"resource": {}, "resource_id": {}, "message": {}, "message_id": {},
	"organization": {}, "organization_id": {}, "tenant": {}, "tenant_id": {},
}

type Registry struct {
	mu         sync.RWMutex
	counters   map[string]*counterPoint
	gauges     map[string]*gaugePoint
	histograms map[string]*histogramPoint
}

type counterPoint struct {
	name   string
	help   string
	labels map[string]string
	value  float64
}

type gaugePoint struct {
	name   string
	help   string
	labels map[string]string
	value  float64
}

type histogramPoint struct {
	name    string
	help    string
	labels  map[string]string
	buckets []float64
	counts  []uint64
	count   uint64
	sum     float64
}

func NewRegistry() *Registry {
	return &Registry{counters: map[string]*counterPoint{}, gauges: map[string]*gaugePoint{}, histograms: map[string]*histogramPoint{}}
}

func ValidateMetricLabels(labels map[string]string) error {
	for key, value := range labels {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if _, forbidden := forbiddenMetricLabels[normalized]; forbidden {
			return fmt.Errorf("metric label %q is forbidden", key)
		}
		if len(value) > 128 || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("metric label %q has an unsafe value", key)
		}
	}
	return nil
}

func (registry *Registry) AddCounter(name, help string, labels map[string]string, delta float64) error {
	if err := ValidateMetricLabels(labels); err != nil {
		return err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	key := metricKey(name, labels)
	point := registry.counters[key]
	if point == nil {
		point = &counterPoint{name: name, help: help, labels: cloneLabels(labels)}
		registry.counters[key] = point
	}
	point.value += delta
	return nil
}

func (registry *Registry) SetGauge(name, help string, labels map[string]string, value float64) error {
	if err := ValidateMetricLabels(labels); err != nil {
		return err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.gauges[metricKey(name, labels)] = &gaugePoint{name: name, help: help, labels: cloneLabels(labels), value: value}
	return nil
}

func (registry *Registry) ObserveHistogram(name, help string, labels map[string]string, value float64, buckets []float64) error {
	if err := ValidateMetricLabels(labels); err != nil {
		return err
	}
	if len(buckets) == 0 {
		buckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	key := metricKey(name, labels)
	point := registry.histograms[key]
	if point == nil {
		point = &histogramPoint{name: name, help: help, labels: cloneLabels(labels), buckets: append([]float64(nil), buckets...), counts: make([]uint64, len(buckets))}
		registry.histograms[key] = point
	}
	point.count++
	point.sum += value
	for index, upper := range point.buckets {
		if value <= upper {
			point.counts[index]++
		}
	}
	return nil
}

func (registry *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; version=0.0.4")
		registry.writePrometheus(writer)
	})
}

func (registry *Registry) writePrometheus(writer io.Writer) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	for _, key := range sortedKeys(registry.counters) {
		point := registry.counters[key]
		fmt.Fprintf(writer, "# HELP %s %s\n# TYPE %s counter\n%s%s %s\n", point.name, escapeHelp(point.help), point.name, point.name, renderLabels(point.labels, "", ""), strconv.FormatFloat(point.value, 'f', -1, 64))
	}
	for _, key := range sortedKeys(registry.gauges) {
		point := registry.gauges[key]
		fmt.Fprintf(writer, "# HELP %s %s\n# TYPE %s gauge\n%s%s %s\n", point.name, escapeHelp(point.help), point.name, point.name, renderLabels(point.labels, "", ""), strconv.FormatFloat(point.value, 'f', -1, 64))
	}
	for _, key := range sortedKeys(registry.histograms) {
		point := registry.histograms[key]
		fmt.Fprintf(writer, "# HELP %s %s\n# TYPE %s histogram\n", point.name, escapeHelp(point.help), point.name)
		for index, upper := range point.buckets {
			fmt.Fprintf(writer, "%s_bucket%s %d\n", point.name, renderLabels(point.labels, "le", strconv.FormatFloat(upper, 'f', -1, 64)), point.counts[index])
		}
		fmt.Fprintf(writer, "%s_bucket%s %d\n", point.name, renderLabels(point.labels, "le", "+Inf"), point.count)
		fmt.Fprintf(writer, "%s_sum%s %s\n", point.name, renderLabels(point.labels, "", ""), strconv.FormatFloat(point.sum, 'f', -1, 64))
		fmt.Fprintf(writer, "%s_count%s %d\n", point.name, renderLabels(point.labels, "", ""), point.count)
	}
}

func metricKey(name string, labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteString(name)
	for _, key := range keys {
		builder.WriteByte('|')
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(labels[key])
	}
	return builder.String()
}

func renderLabels(labels map[string]string, extraKey, extraValue string) string {
	merged := cloneLabels(labels)
	if extraKey != "" {
		merged[extraKey] = extraValue
	}
	if len(merged) == 0 {
		return ""
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"=\""+strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n").Replace(merged[key])+"\"")
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func cloneLabels(labels map[string]string) map[string]string {
	copyOfLabels := make(map[string]string, len(labels))
	for key, value := range labels {
		copyOfLabels[key] = value
	}
	return copyOfLabels
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func escapeHelp(value string) string { return strings.ReplaceAll(value, "\\", "\\\\") }
