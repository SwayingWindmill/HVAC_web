package observability

import (
	"bytes"
	"context"
	"errors"
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

func TestReadinessTracksRequiredDependencyFailureAndRecovery(t *testing.T) {
	dependencyReady := false
	runtime := NewRuntime(RuntimeConfig{Service: "scheduler", QueueSize: 1})
	runtime.SetDependencies(Dependency{
		Name:     "postgres",
		Required: true,
		Check: func(context.Context) error {
			if !dependencyReady {
				return errors.New("database unavailable")
			}
			return nil
		},
	})
	runtime.MarkReady()
	handler := runtime.DiagnosticsHandler()

	failed := httptest.NewRecorder()
	handler.ServeHTTP(failed, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if failed.Code != http.StatusServiceUnavailable {
		t.Fatalf("dependency failure status = %d", failed.Code)
	}

	dependencyReady = true
	recovered := httptest.NewRecorder()
	handler.ServeHTTP(recovered, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if recovered.Code != http.StatusOK {
		t.Fatalf("dependency recovery status = %d", recovered.Code)
	}
}

func TestReadinessStaysServingWhenOptionalDependencyFails(t *testing.T) {
	runtime := NewRuntime(RuntimeConfig{Service: "telemetry-runtime", QueueSize: 1})
	runtime.SetDependencies(
		Dependency{Name: "postgres", Required: true, Check: func(context.Context) error { return nil }},
		Dependency{Name: "redis-latest-cache", Required: false, Check: func(context.Context) error { return errors.New("connection refused") }},
	)
	runtime.MarkReady()
	handler := runtime.DiagnosticsHandler()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("optional dependency failure status = %d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, `"status":"degraded"`) || !strings.Contains(body, `"name":"redis-latest-cache"`) || !strings.Contains(body, `"status":"down"`) {
		t.Fatalf("degraded response missing dependency detail: %s", body)
	}
}

func TestReadinessRequiredDependencyFailureNamesTheDependency(t *testing.T) {
	runtime := NewRuntime(RuntimeConfig{Service: "gateway", QueueSize: 1})
	runtime.SetDependencies(
		Dependency{Name: "postgres-session", Required: true, Check: func(context.Context) error { return errors.New("database unavailable") }},
		Dependency{Name: "redis", Required: false, Check: func(context.Context) error { return nil }},
	)
	runtime.MarkReady()
	handler := runtime.DiagnosticsHandler()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("required dependency failure status = %d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, `"name":"postgres-session"`) || !strings.Contains(body, `"error":"database unavailable"`) {
		t.Fatalf("not-ready response missing failing dependency: %s", body)
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

func TestRuntimeFederatesChildMetricsWithoutDuplicateFamilyHeaders(t *testing.T) {
	parent := NewRuntime(RuntimeConfig{Service: "platform-gateway", QueueSize: 1})
	cmdRuntime := NewRuntime(RuntimeConfig{Service: "energy-api-command", QueueSize: 1})
	alarmRuntime := NewRuntime(RuntimeConfig{Service: "energy-api-alarm", QueueSize: 1})

	_ = parent.Metrics.AddCounter("s0_http_requests_total", "HTTP requests.", map[string]string{"service": "platform-gateway", "result": "2xx"}, 3)
	_ = cmdRuntime.Metrics.AddCounter("hvac_command_verifications_total", "Command verification results by final outcome.", map[string]string{"outcome": "verified"}, 4)
	_ = cmdRuntime.Metrics.AddCounter("hvac_command_verifications_total", "Command verification results by final outcome.", map[string]string{"outcome": "timed_out"}, 1)
	_ = alarmRuntime.Metrics.AddCounter("hvac_alarm_http_requests_total", "Alarm HTTP requests.", map[string]string{"service": "alarm-service", "method": "GET", "route": "alarms.list", "status_class": "2xx"}, 2)
	_ = alarmRuntime.Metrics.ObserveHistogram("hvac_alarm_http_request_duration_seconds", "Alarm request duration.", map[string]string{"service": "alarm-service", "method": "GET", "route": "alarms.list", "status_class": "2xx"}, 0.02, nil)

	parent.AttachChildRuntime(cmdRuntime)
	parent.AttachChildRuntime(cmdRuntime)
	parent.AttachChildMetrics("alarm", alarmRuntime.Metrics)

	rec := httptest.NewRecorder()
	parent.DiagnosticsHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`s0_http_requests_total{result="2xx",service="platform-gateway"} 3`,
		`hvac_command_verifications_total{outcome="verified"} 4`,
		`hvac_command_verifications_total{outcome="timed_out"} 1`,
		`hvac_alarm_http_requests_total{method="GET",route="alarms.list",service="alarm-service",status_class="2xx"} 2`,
		`hvac_alarm_http_request_duration_seconds_count{method="GET",route="alarms.list",service="alarm-service",status_class="2xx"} 1`,
		`s0_telemetry_dropped_spans{service="platform-gateway"} 0`,
		`s0_telemetry_dropped_spans{service="energy-api-command"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("federated /metrics missing %q in:\n%s", want, body)
		}
	}
	if count := strings.Count(body, "# TYPE hvac_command_verifications_total counter"); count != 1 {
		t.Fatalf("expected deduplicated # TYPE hvac_command_verifications_total counter header, got %d:\n%s", count, body)
	}
	if count := strings.Count(body, "# HELP s0_telemetry_dropped_spans "); count != 1 {
		t.Fatalf("expected deduplicated # HELP s0_telemetry_dropped_spans header, got %d:\n%s", count, body)
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
