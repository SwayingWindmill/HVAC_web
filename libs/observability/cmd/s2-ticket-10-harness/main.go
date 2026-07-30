package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

type harnessReport struct {
	SchemaVersion int                                     `json:"schemaVersion"`
	Status        string                                  `json:"status"`
	Cardinality   []observability.MetricCardinalityFamily `json:"cardinality"`
	Trace         traceReport                             `json:"trace"`
	Redaction     redactionReport                         `json:"redaction"`
	Outage        outageReport                            `json:"outage"`
}

type traceReport struct {
	TraceID             string                   `json:"traceId"`
	Services            []string                 `json:"services"`
	SpanCount           int                      `json:"spanCount"`
	ParentChainComplete bool                     `json:"parentChainComplete"`
	References          map[string]string        `json:"references"`
	Spans               []observability.SpanData `json:"spans"`
}

type redactionReport struct {
	RawSensitiveOccurrences int      `json:"rawSensitiveOccurrences"`
	HMACReferenceCount      int      `json:"hmacReferenceCount"`
	RejectedRawFields       []string `json:"rejectedRawFields"`
}

type outageReport struct {
	BusinessTransactionCompleted bool   `json:"businessTransactionCompleted"`
	BusinessDurationMilliseconds int64  `json:"businessDurationMilliseconds"`
	ExportFailures               uint64 `json:"exportFailures"`
	ExportDrops                  uint64 `json:"exportDrops"`
	LastErrorCode                string `json:"lastErrorCode"`
	SensitiveOccurrences         int    `json:"sensitiveOccurrences"`
}

type failingExporter struct{}

func (failingExporter) Export(context.Context, []observability.SpanData) error {
	return errors.New("collector unavailable")
}
func (failingExporter) Shutdown(context.Context) error { return nil }

func main() {
	catalogPath := flag.String("catalog", "deploy/s2/observability/metric-catalog.v1.json", "metric catalog path")
	outputPath := flag.String("output", "out/s2-security-observability/observability-harness.json", "output report path")
	flag.Parse()

	payload, err := os.ReadFile(*catalogPath)
	must(err)
	var catalog observability.S2MetricCatalog
	must(json.Unmarshal(payload, &catalog))
	cardinality, err := observability.ValidateS2MetricCatalog(catalog)
	must(err)
	registry := observability.NewRegistry()
	for _, family := range catalog.Families {
		labels := map[string]string{}
		for _, label := range family.Labels {
			labels[label] = catalog.SampleValues[label][0]
		}
		must(observability.RecordS2MetricSample(registry, family, labels, 1))
		if len(family.Labels) > 0 && len(catalog.SampleValues[family.Labels[0]]) > 1 {
			second := cloneString(labels)
			second[family.Labels[0]] = catalog.SampleValues[family.Labels[0]][1]
			must(observability.RecordS2MetricSample(registry, family, second, 1))
		}
	}
	observed := registry.SeriesCardinality()
	for index := range cardinality {
		cardinality[index].ObservedCardinality = observed[cardinality[index].Name]
		cardinality[index].WithinBudget = cardinality[index].WithinBudget && cardinality[index].ObservedCardinality <= cardinality[index].SeriesBudget
		if cardinality[index].ObservedCardinality == 0 || !cardinality[index].WithinBudget {
			panic("S2 metric family was not exposed within its series budget")
		}
	}
	recorder := httptest.NewRecorder()
	registry.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	for _, family := range catalog.Families {
		if !strings.Contains(recorder.Body.String(), family.Name) {
			panic("S2 metric exposition omitted " + family.Name)
		}
	}

	raw := map[string]string{
		"request":      "req-018f6a00-1000-7000-8000-000000000001",
		"event":        "evt-018f6a00-2000-7000-8000-000000000002",
		"subscription": "centrifugo:subscription:private-device-channel",
		"revision":     "business-revision-4242",
	}
	hasher, err := observability.NewReferenceHasher([]byte(strings.Repeat("ticket-10-ephemeral-hmac-key-", 2)))
	must(err)
	operationalFields, err := observability.HMACOperationalReferences(hasher, raw)
	must(err)
	operationalFields["operation"] = "snapshot-to-publication"
	operationalFields["outcome"] = "success"
	must(observability.ValidateOperationalRecord(operationalFields))

	trace := buildTrace(operationalFields)
	redaction := buildRedaction(operationalFields, raw, trace.Spans)
	outage := buildOutage(operationalFields, raw)

	report := harnessReport{
		SchemaVersion: 1,
		Status:        "passed",
		Cardinality:   cardinality,
		Trace:         trace,
		Redaction:     redaction,
		Outage:        outage,
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	must(err)
	for _, value := range raw {
		if strings.Contains(string(encoded), value) {
			panic("harness evidence leaked a raw correlation identifier")
		}
	}
	must(os.MkdirAll(filepath.Dir(*outputPath), 0o755))
	must(os.WriteFile(*outputPath, append(encoded, '\n'), 0o644))
	fmt.Printf("S2 Ticket 10 observability harness passed: %s\n", *outputPath)
}

func buildTrace(fields map[string]any) traceReport {
	exporter := &observability.MemoryExporter{}
	services := []string{
		"platform-gateway",
		"iam-service",
		"telemetry-runtime-service",
		"outbox-relay",
		"centrifugo-api",
		"telemetry-live-client",
		"audit-ledger-service",
	}
	ctx := context.Background()
	ctx = observability.ContextWithRemoteParent(
		ctx,
		observability.NewTracer(services[0], exporter),
		"00-11111111111111111111111111111111-2222222222222222-01",
		"",
	)
	for index, service := range services {
		tracer := observability.NewTracer(service, exporter)
		attributes := clone(fields)
		attributes["phase"] = []string{"request", "authorize", "evaluate", "relay", "publish", "recover", "audit"}[index]
		var span *observability.Span
		ctx, span = tracer.Start(ctx, service+".s2", observability.SpanKindInternal, attributes)
		span.SetStatus("ok", "")
		span.End()
	}
	spans := exporter.Spans()
	if len(spans) != len(services) {
		panic("trace harness did not export every service span")
	}
	traceID := spans[0].TraceID
	parentComplete := true
	for index, span := range spans {
		if span.TraceID != traceID {
			panic("trace ID changed across the S2 chain")
		}
		if index > 0 && span.ParentSpanID != spans[index-1].SpanID {
			parentComplete = false
		}
		must(observability.ValidateOperationalRecord(span.Attributes))
	}
	if !parentComplete {
		panic("trace parent chain is incomplete")
	}
	refs := map[string]string{}
	for key, value := range fields {
		if strings.HasSuffix(key, "_ref") {
			refs[key] = fmt.Sprint(value)
		}
	}
	if len(refs) != 4 {
		panic("trace chain does not contain every required HMAC reference")
	}
	return traceReport{TraceID: traceID, Services: services, SpanCount: len(spans), ParentChainComplete: parentComplete, References: refs, Spans: spans}
}

func buildRedaction(fields map[string]any, raw map[string]string, spans []observability.SpanData) redactionReport {
	encoded, err := json.Marshal(spans)
	must(err)
	occurrences := 0
	for _, value := range raw {
		occurrences += strings.Count(string(encoded), value)
	}
	if occurrences != 0 {
		panic("trace evidence contains raw sensitive values")
	}
	rejected := []string{}
	for _, key := range []string{"authorization", "cookie", "csrf_token", "recovery_cursor", "channel", "telemetry_value", "source_credential"} {
		if observability.ValidateOperationalRecord(map[string]any{key: "raw-sensitive-value"}) != nil {
			rejected = append(rejected, key)
		}
	}
	if len(rejected) != 7 {
		panic("redaction validator accepted a raw sensitive field")
	}
	references := 0
	for _, value := range fields {
		if observability.IsHMACReference(fmt.Sprint(value)) {
			references++
		}
	}
	return redactionReport{RawSensitiveOccurrences: occurrences, HMACReferenceCount: references, RejectedRawFields: rejected}
}

func buildOutage(fields map[string]any, raw map[string]string) outageReport {
	exporter := observability.NewAsyncExporter(failingExporter{}, 1)
	tracer := observability.NewTracer("telemetry-runtime-service", exporter)
	started := time.Now()
	ctx, span := tracer.Start(context.Background(), "business.transaction", observability.SpanKindInternal, clone(fields))
	_ = ctx
	span.SetStatus("ok", "")
	span.End()
	businessDuration := time.Since(started)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	must(exporter.Shutdown(shutdownCtx))
	if exporter.Failed() == 0 {
		panic("failing exporter did not record an export failure")
	}
	if businessDuration > 250*time.Millisecond {
		panic("observability outage blocked the business transaction")
	}
	encoded, err := json.Marshal(fields)
	must(err)
	sensitive := 0
	for _, value := range raw {
		sensitive += strings.Count(string(encoded), value)
	}
	if sensitive != 0 {
		panic("outage evidence leaked raw sensitive values")
	}
	return outageReport{
		BusinessTransactionCompleted: true,
		BusinessDurationMilliseconds: businessDuration.Milliseconds(),
		ExportFailures:               exporter.Failed(),
		ExportDrops:                  exporter.Dropped(),
		LastErrorCode:                exporter.LastErrorCode(),
		SensitiveOccurrences:         sensitive,
	}
}

func cloneString(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func clone(input map[string]any) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
