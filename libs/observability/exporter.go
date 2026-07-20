package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type NopExporter struct{}

func (NopExporter) Export(context.Context, []SpanData) error { return nil }
func (NopExporter) Shutdown(context.Context) error           { return nil }

type MemoryExporter struct {
	mu    sync.Mutex
	spans []SpanData
}

func (exporter *MemoryExporter) Export(_ context.Context, spans []SpanData) error {
	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	for _, span := range spans {
		span.Attributes = cloneAttributes(span.Attributes)
		exporter.spans = append(exporter.spans, span)
	}
	return nil
}

func (exporter *MemoryExporter) Shutdown(context.Context) error { return nil }

func (exporter *MemoryExporter) Spans() []SpanData {
	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	result := make([]SpanData, len(exporter.spans))
	copy(result, exporter.spans)
	return result
}

type AsyncExporter struct {
	delegate Exporter
	queue    chan []SpanData
	done     chan struct{}
	once     sync.Once
	dropped  atomic.Uint64
	failed   atomic.Uint64
	mu       sync.Mutex
	lastErr  string
}

func NewAsyncExporter(delegate Exporter, queueSize int) *AsyncExporter {
	if delegate == nil {
		delegate = NopExporter{}
	}
	if queueSize <= 0 {
		queueSize = 256
	}
	exporter := &AsyncExporter{delegate: delegate, queue: make(chan []SpanData, queueSize), done: make(chan struct{})}
	go exporter.run()
	return exporter
}

func (exporter *AsyncExporter) Export(_ context.Context, spans []SpanData) error {
	copyOfSpans := append([]SpanData(nil), spans...)
	select {
	case exporter.queue <- copyOfSpans:
		return nil
	default:
		exporter.dropped.Add(uint64(len(copyOfSpans)))
		return nil
	}
}

func (exporter *AsyncExporter) Shutdown(ctx context.Context) error {
	exporter.once.Do(func() { close(exporter.queue) })
	select {
	case <-exporter.done:
		return exporter.delegate.Shutdown(ctx)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (exporter *AsyncExporter) Dropped() uint64 { return exporter.dropped.Load() }
func (exporter *AsyncExporter) Failed() uint64  { return exporter.failed.Load() }

func (exporter *AsyncExporter) LastErrorCode() string {
	exporter.mu.Lock()
	defer exporter.mu.Unlock()
	return exporter.lastErr
}

func (exporter *AsyncExporter) run() {
	defer close(exporter.done)
	for spans := range exporter.queue {
		ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
		err := exporter.delegate.Export(ctx, spans)
		cancel()
		if err != nil {
			exporter.failed.Add(uint64(len(spans)))
			exporter.mu.Lock()
			exporter.lastErr = "OTEL_EXPORT_FAILED"
			exporter.mu.Unlock()
		}
	}
}

type OTLPHTTPExporter struct {
	endpoint string
	client   *http.Client
}

func NewOTLPHTTPExporter(endpoint string, timeout time.Duration) *OTLPHTTPExporter {
	if timeout <= 0 {
		timeout = 500 * time.Millisecond
	}
	endpoint = strings.TrimRight(endpoint, "/")
	if endpoint != "" && !strings.HasSuffix(endpoint, "/v1/traces") {
		endpoint += "/v1/traces"
	}
	return &OTLPHTTPExporter{endpoint: endpoint, client: &http.Client{Timeout: timeout}}
}

func (exporter *OTLPHTTPExporter) Export(ctx context.Context, spans []SpanData) error {
	if exporter.endpoint == "" || len(spans) == 0 {
		return nil
	}
	payload, err := json.Marshal(buildOTLPPayload(spans))
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, exporter.endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := exporter.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("OTEL_EXPORT_REJECTED")
	}
	return nil
}

func (exporter *OTLPHTTPExporter) Shutdown(context.Context) error { return nil }

type otlpPayload struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

type otlpResourceSpans struct {
	Resource   otlpResource    `json:"resource"`
	ScopeSpans []otlpScopeSpan `json:"scopeSpans"`
}

type otlpResource struct {
	Attributes []otlpAttribute `json:"attributes"`
}

type otlpScopeSpan struct {
	Scope otlpScope  `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

type otlpScope struct {
	Name string `json:"name"`
}

type otlpSpan struct {
	TraceID           string          `json:"traceId"`
	SpanID            string          `json:"spanId"`
	ParentSpanID      string          `json:"parentSpanId,omitempty"`
	TraceState        string          `json:"traceState,omitempty"`
	Name              string          `json:"name"`
	Kind              SpanKind        `json:"kind"`
	StartTimeUnixNano string          `json:"startTimeUnixNano"`
	EndTimeUnixNano   string          `json:"endTimeUnixNano"`
	Attributes        []otlpAttribute `json:"attributes,omitempty"`
	Status            otlpStatus      `json:"status,omitempty"`
}

type otlpStatus struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type otlpAttribute struct {
	Key   string    `json:"key"`
	Value otlpValue `json:"value"`
}

type otlpValue struct {
	StringValue string  `json:"stringValue,omitempty"`
	BoolValue   bool    `json:"boolValue,omitempty"`
	IntValue    string  `json:"intValue,omitempty"`
	DoubleValue float64 `json:"doubleValue,omitempty"`
}

func buildOTLPPayload(spans []SpanData) otlpPayload {
	byService := map[string][]otlpSpan{}
	for _, span := range spans {
		attributes := make([]otlpAttribute, 0, len(span.Attributes))
		for key, value := range span.Attributes {
			attributes = append(attributes, attributeToOTLP(key, value))
		}
		statusCode := 0
		if span.StatusCode == "error" {
			statusCode = 2
		} else if span.StatusCode == "ok" {
			statusCode = 1
		}
		byService[span.Service] = append(byService[span.Service], otlpSpan{
			TraceID: span.TraceID, SpanID: span.SpanID, ParentSpanID: span.ParentSpanID, TraceState: span.TraceState,
			Name: span.Name, Kind: span.Kind,
			StartTimeUnixNano: strconvInt64(span.StartTime.UnixNano()), EndTimeUnixNano: strconvInt64(span.EndTime.UnixNano()),
			Attributes: attributes, Status: otlpStatus{Code: statusCode, Message: span.StatusText},
		})
	}
	payload := otlpPayload{}
	for service, serviceSpans := range byService {
		payload.ResourceSpans = append(payload.ResourceSpans, otlpResourceSpans{
			Resource:   otlpResource{Attributes: []otlpAttribute{attributeToOTLP("service.name", service)}},
			ScopeSpans: []otlpScopeSpan{{Scope: otlpScope{Name: "github.com/quanlaihe/hvac-web/libs/observability"}, Spans: serviceSpans}},
		})
	}
	return payload
}

func attributeToOTLP(key string, value any) otlpAttribute {
	attribute := otlpAttribute{Key: key}
	switch typed := value.(type) {
	case bool:
		attribute.Value.BoolValue = typed
	case int:
		attribute.Value.IntValue = strconvInt64(int64(typed))
	case int64:
		attribute.Value.IntValue = strconvInt64(typed)
	case uint64:
		attribute.Value.IntValue = strconvInt64(int64(typed))
	case float64:
		attribute.Value.DoubleValue = typed
	case string:
		attribute.Value.StringValue = strings.TrimSpace(typed)
	default:
		attribute.Value.StringValue = strings.TrimSpace(fmt.Sprint(value))
	}
	return attribute
}

func strconvInt64(value int64) string { return fmt.Sprintf("%d", value) }
