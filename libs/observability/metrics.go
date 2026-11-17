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
	"site": {}, "site_id": {}, "device": {}, "device_id": {},
	"subscription": {}, "subscription_id": {}, "cursor": {}, "recovery_cursor": {},
	"revision": {}, "business_revision": {}, "key": {}, "telemetry_key": {}, "value": {},
	"token": {}, "channel": {}, "authorization": {}, "cookie": {}, "csrf": {}, "source_credential": {},
}

type Registry struct {
	mu         sync.RWMutex
	counters   map[string]*counterPoint
	gauges     map[string]*gaugePoint
	histograms map[string]*histogramPoint
	children   []*Registry
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

// DeleteGauge removes a specific labeled gauge series so reaped dynamic entities do not leak cardinality.
func (registry *Registry) DeleteGauge(name string, labels map[string]string) {
	if registry == nil {
		return
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	delete(registry.gauges, metricKey(name, labels))
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

// AttachChild links a child Registry whose metrics are federated during
// Prometheus scrapes and cardinality checks without mutating the parent map.
func (registry *Registry) AttachChild(child *Registry) {
	if registry == nil || child == nil || child == registry {
		return
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for _, existing := range registry.children {
		if existing == child {
			return
		}
	}
	registry.children = append(registry.children, child)
}

func (registry *Registry) collectPoints(visited map[*Registry]bool) ([]counterPoint, []gaugePoint, []histogramPoint) {
	if registry == nil || visited[registry] {
		return nil, nil, nil
	}
	visited[registry] = true

	registry.mu.RLock()
	counters := make([]counterPoint, 0, len(registry.counters))
	for _, key := range sortedKeys(registry.counters) {
		point := registry.counters[key]
		counters = append(counters, counterPoint{
			name: point.name, help: point.help, labels: cloneLabels(point.labels), value: point.value,
		})
	}
	gauges := make([]gaugePoint, 0, len(registry.gauges))
	for _, key := range sortedKeys(registry.gauges) {
		point := registry.gauges[key]
		gauges = append(gauges, gaugePoint{
			name: point.name, help: point.help, labels: cloneLabels(point.labels), value: point.value,
		})
	}
	histograms := make([]histogramPoint, 0, len(registry.histograms))
	for _, key := range sortedKeys(registry.histograms) {
		point := registry.histograms[key]
		histograms = append(histograms, histogramPoint{
			name: point.name, help: point.help, labels: cloneLabels(point.labels),
			buckets: append([]float64(nil), point.buckets...),
			counts:  append([]uint64(nil), point.counts...),
			count:   point.count,
			sum:     point.sum,
		})
	}
	children := append([]*Registry(nil), registry.children...)
	registry.mu.RUnlock()

	for _, child := range children {
		cCounters, cGauges, cHistograms := child.collectPoints(visited)
		counters = append(counters, cCounters...)
		gauges = append(gauges, cGauges...)
		histograms = append(histograms, cHistograms...)
	}
	return counters, gauges, histograms
}

func (registry *Registry) SeriesCardinality() map[string]int {
	counters, gauges, histograms := registry.collectPoints(map[*Registry]bool{})
	seen := map[string]struct{}{}
	result := map[string]int{}
	for _, point := range counters {
		key := metricKey(point.name, point.labels)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result[point.name]++
	}
	for _, point := range gauges {
		key := metricKey(point.name, point.labels)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result[point.name]++
	}
	for _, point := range histograms {
		key := metricKey(point.name, point.labels)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result[point.name]++
	}
	return result
}

type prometheusFamily struct {
	name    string
	help    string
	kind    string
	samples []string
}

func (registry *Registry) writePrometheus(writer io.Writer) {
	counters, gauges, histograms := registry.collectPoints(map[*Registry]bool{})
	families := map[string]*prometheusFamily{}
	seenSeries := map[string]struct{}{}

	ensureFamily := func(name, help, kind string) *prometheusFamily {
		family, exists := families[name]
		if !exists {
			family = &prometheusFamily{name: name, help: help, kind: kind}
			families[name] = family
		}
		return family
	}

	for _, point := range counters {
		seriesKey := "counter|" + metricKey(point.name, point.labels)
		if _, duplicate := seenSeries[seriesKey]; duplicate {
			continue
		}
		seenSeries[seriesKey] = struct{}{}
		family := ensureFamily(point.name, point.help, "counter")
		family.samples = append(family.samples, fmt.Sprintf("%s%s %s\n", point.name, renderLabels(point.labels, "", ""), strconv.FormatFloat(point.value, 'f', -1, 64)))
	}
	for _, point := range gauges {
		seriesKey := "gauge|" + metricKey(point.name, point.labels)
		if _, duplicate := seenSeries[seriesKey]; duplicate {
			continue
		}
		seenSeries[seriesKey] = struct{}{}
		family := ensureFamily(point.name, point.help, "gauge")
		family.samples = append(family.samples, fmt.Sprintf("%s%s %s\n", point.name, renderLabels(point.labels, "", ""), strconv.FormatFloat(point.value, 'f', -1, 64)))
	}
	for _, point := range histograms {
		seriesKey := "histogram|" + metricKey(point.name, point.labels)
		if _, duplicate := seenSeries[seriesKey]; duplicate {
			continue
		}
		seenSeries[seriesKey] = struct{}{}
		family := ensureFamily(point.name, point.help, "histogram")
		var block strings.Builder
		for index, upper := range point.buckets {
			fmt.Fprintf(&block, "%s_bucket%s %d\n", point.name, renderLabels(point.labels, "le", strconv.FormatFloat(upper, 'f', -1, 64)), point.counts[index])
		}
		fmt.Fprintf(&block, "%s_bucket%s %d\n", point.name, renderLabels(point.labels, "le", "+Inf"), point.count)
		fmt.Fprintf(&block, "%s_sum%s %s\n", point.name, renderLabels(point.labels, "", ""), strconv.FormatFloat(point.sum, 'f', -1, 64))
		fmt.Fprintf(&block, "%s_count%s %d\n", point.name, renderLabels(point.labels, "", ""), point.count)
		family.samples = append(family.samples, block.String())
	}

	names := sortedKeys(families)
	for _, name := range names {
		family := families[name]
		if family.kind != "histogram" {
			sort.Strings(family.samples)
		}
		fmt.Fprintf(writer, "# HELP %s %s\n# TYPE %s %s\n", family.name, escapeHelp(family.help), family.name, family.kind)
		for _, sample := range family.samples {
			_, _ = io.WriteString(writer, sample)
		}
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
