package observability

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTraceContextContinuesAndCreatesChildSpans(t *testing.T) {
	exporter := &MemoryExporter{}
	runtime := NewRuntime(RuntimeConfig{Service: "gateway", Exporter: exporter, QueueSize: 8})
	ctx := runtime.Tracer.ExtractHTTP(context.Background(), http.Header{"Traceparent": []string{"00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"}})
	ctx, parent := runtime.Tracer.Start(ctx, "gateway.request", SpanKindServer, nil)
	_, child := Start(ctx, "postgres.session", SpanKindClient, map[string]any{"db.system": "postgresql"})
	child.End()
	parent.End()
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Shutdown(shutdownContext); err != nil {
		t.Fatal(err)
	}
	spans := exporter.Spans()
	if len(spans) != 2 {
		t.Fatalf("spans = %d", len(spans))
	}
	if spans[0].TraceID != "0123456789abcdef0123456789abcdef" || spans[1].TraceID != spans[0].TraceID {
		t.Fatalf("trace continuity failed: %#v", spans)
	}
	if spans[0].ParentSpanID != spans[1].SpanID {
		t.Fatalf("child parent = %q, want %q", spans[0].ParentSpanID, spans[1].SpanID)
	}
}

func TestAsyncExporterDoesNotBlockWhenDelegateIsUnavailable(t *testing.T) {
	blocking := blockingExporter{release: make(chan struct{})}
	exporter := NewAsyncExporter(blocking, 1)
	started := time.Now()
	for index := 0; index < 1000; index++ {
		_ = exporter.Export(context.Background(), []SpanData{{Service: "gateway", Name: "request"}})
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("non-blocking export took %s", elapsed)
	}
	close(blocking.release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = exporter.Shutdown(ctx)
	if exporter.Dropped() == 0 {
		t.Fatal("expected dropped spans when queue is full")
	}
}

func TestMetricsRejectHighCardinalityLabelsAndExposeSafeSeries(t *testing.T) {
	registry := NewRegistry()
	if err := registry.AddCounter("platform_requests_total", "Requests", map[string]string{"trace_id": "abc"}, 1); err == nil {
		t.Fatal("expected trace_id label rejection")
	}
	if err := registry.AddCounter("platform_requests_total", "Requests", map[string]string{"service": "gateway", "result": "ok"}, 1); err != nil {
		t.Fatal(err)
	}
	buffer := &bytes.Buffer{}
	registry.writePrometheus(buffer)
	output := buffer.String()
	if !strings.Contains(output, `platform_requests_total{result="ok",service="gateway"} 1`) || strings.Contains(output, "trace_id") {
		t.Fatalf("unexpected metrics output: %s", output)
	}
}

func TestLoggerRedactsCredentialFields(t *testing.T) {
	buffer := &bytes.Buffer{}
	logger := NewJSONLogger(buffer, 0)
	logger.Error("request_failed", "error_code", "AUTH_FAILED", "authorization", "Bearer seeded-secret", "error", "postgres://seeded-secret")
	output := buffer.String()
	if strings.Contains(output, "seeded-secret") || !strings.Contains(output, "[REDACTED]") {
		t.Fatalf("logger output was not redacted: %s", output)
	}
}

func TestDiagnosticsSeparateStartupLivenessAndReadiness(t *testing.T) {
	runtime := NewRuntime(RuntimeConfig{Service: "gateway", QueueSize: 1})
	handler := runtime.DiagnosticsHandler()

	for _, path := range []string{"/health/startup", "/health/live"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, response.Code)
		}
	}

	notReady := httptest.NewRecorder()
	handler.ServeHTTP(notReady, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if notReady.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready status = %d", notReady.Code)
	}

	runtime.MarkReady()
	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if ready.Code != http.StatusOK {
		t.Fatalf("ready status = %d", ready.Code)
	}

	runtime.MarkNotReady()
	if runtime.Ready() {
		t.Fatal("runtime remained ready during drain")
	}
}

type blockingExporter struct{ release chan struct{} }

func (exporter blockingExporter) Export(ctx context.Context, _ []SpanData) error {
	select {
	case <-exporter.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (blockingExporter) Shutdown(context.Context) error { return nil }
