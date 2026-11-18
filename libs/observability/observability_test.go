package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

func TestMetricsDeleteGaugeRemovesSeries(t *testing.T) {
	registry := NewRegistry()
	if err := registry.SetGauge("hvac_mqtt_gateway_queue_depth", "Queue depth.", map[string]string{"gateway_id": "gw-1"}, 5); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetGauge("hvac_mqtt_gateway_queue_depth", "Queue depth.", map[string]string{"gateway_id": "gw-2"}, 2); err != nil {
		t.Fatal(err)
	}
	if got := registry.SeriesCardinality()["hvac_mqtt_gateway_queue_depth"]; got != 2 {
		t.Fatalf("initial cardinality = %d, want 2", got)
	}
	registry.DeleteGauge("hvac_mqtt_gateway_queue_depth", map[string]string{"gateway_id": "gw-1"})
	if got := registry.SeriesCardinality()["hvac_mqtt_gateway_queue_depth"]; got != 1 {
		t.Fatalf("post-delete cardinality = %d, want 1", got)
	}
	buffer := &bytes.Buffer{}
	registry.writePrometheus(buffer)
	output := buffer.String()
	if strings.Contains(output, `gateway_id="gw-1"`) || !strings.Contains(output, `gateway_id="gw-2"`) {
		t.Fatalf("unexpected metrics output after DeleteGauge: %s", output)
	}
	var nilRegistry *Registry
	nilRegistry.DeleteGauge("hvac_mqtt_gateway_queue_depth", map[string]string{"gateway_id": "gw-1"})
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
	var logs syncBuffer
	logger := NewJSONLogger(&logs, slog.LevelInfo)
	runtime := NewRuntime(RuntimeConfig{Service: "gateway", QueueSize: 1, Logger: logger})
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
	if !strings.Contains(body, `"name":"postgres-session"`) || !strings.Contains(body, `"error":"[REDACTED]"`) {
		t.Fatalf("not-ready response missing failing dependency or redaction: %s", body)
	}
	if strings.Contains(body, "database unavailable") {
		t.Fatalf("readiness response leaked raw error: %s", body)
	}
	logOutput := logs.String()
	if !strings.Contains(logOutput, `"level":"WARN"`) || !strings.Contains(logOutput, `"postgres-session"`) || !strings.Contains(logOutput, "database unavailable") {
		t.Fatalf("expected logger.Warn with dependency error details, got: %s", logOutput)
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

func TestDiagnosticsHealthzEndpoint(t *testing.T) {
	runtime := NewRuntime(RuntimeConfig{Service: "gateway", QueueSize: 1})
	rec := httptest.NewRecorder()
	runtime.DiagnosticsHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok\n" {
		t.Fatalf("/healthz = (%d, %q), want (200, \"ok\\n\")", rec.Code, rec.Body.String())
	}
}

func TestDiagnosticsPprofGatedByEnvAndLoopbackOnly(t *testing.T) {
	t.Setenv("ENABLE_DIAGNOSTICS_PPROF", "")
	runtime := NewRuntime(RuntimeConfig{Service: "gateway", QueueSize: 1})
	disabledHandler := runtime.DiagnosticsHandler()
	disabledRec := httptest.NewRecorder()
	disabledReq := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	disabledReq.RemoteAddr = "127.0.0.1:41000"
	disabledHandler.ServeHTTP(disabledRec, disabledReq)
	if disabledRec.Code != http.StatusNotFound {
		t.Fatalf("disabled pprof status = %d, want 404", disabledRec.Code)
	}

	t.Setenv("ENABLE_DIAGNOSTICS_PPROF", "true")
	enabledHandler := runtime.DiagnosticsHandler()

	for _, addr := range []string{"127.0.0.1:41000", "[::1]:41000", "[::ffff:127.0.0.1]:41000"} {
		for _, path := range []string{"/debug/pprof/", "/debug/pprof/cmdline", "/debug/pprof/symbol"} {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.RemoteAddr = addr
			enabledHandler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("loopback %s %s status = %d, want 200", addr, path, rec.Code)
			}
		}
	}

	for _, addr := range []string{"192.0.2.1:1234", "10.0.0.5:8080", "invalid-addr", ""} {
		for _, path := range []string{"/debug/pprof/", "/debug/pprof/cmdline", "/debug/pprof/profile", "/debug/pprof/symbol", "/debug/pprof/trace"} {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.RemoteAddr = addr
			req.Header.Set("X-Forwarded-For", "127.0.0.1")
			enabledHandler.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("non-loopback %q %s status = %d, want 403", addr, path, rec.Code)
			}
		}
	}
}

func TestSetDependenciesConcurrentWithReadinessProbes(t *testing.T) {
	runtime := NewRuntime(RuntimeConfig{Service: "gateway", QueueSize: 1, Logger: NewJSONLogger(io.Discard, slog.LevelError)})
	runtime.MarkReady()
	handler := runtime.DiagnosticsHandler()

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := range 25 {
				if (worker+j)%2 == 0 {
					runtime.SetDependencies(
						Dependency{Name: "postgres", Required: true, Check: func(context.Context) error { return nil }},
						Dependency{Name: "redis", Required: false, Check: func(context.Context) error { return nil }},
					)
				} else {
					rec := httptest.NewRecorder()
					handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
					if rec.Code != http.StatusOK {
						t.Errorf("concurrent /health/ready status = %d", rec.Code)
					}
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestLoggerRedactsExtendedSensitiveFields(t *testing.T) {
	buffer := &bytes.Buffer{}
	logger := NewJSONLogger(buffer, slog.LevelInfo)
	logger.Info(
		"sensitive_fields_test",
		"password", "val-password",
		"secret", "val-secret",
		"token", "val-token",
		"dsn", "val-dsn",
		"authorization", "val-auth",
		"cookie", "val-cookie",
		"api_key", "val-apikey",
		"api-key", "val-apikey-dash",
		"private_key", "val-privkey",
		"credential", "val-cred",
		"safe_field", "kept-value",
	)
	output := buffer.String()
	for _, secretVal := range []string{
		"val-password", "val-secret", "val-token", "val-dsn", "val-auth",
		"val-cookie", "val-apikey", "val-apikey-dash", "val-privkey", "val-cred",
	} {
		if strings.Contains(output, secretVal) {
			t.Fatalf("log leaked sensitive value %q: %s", secretVal, output)
		}
	}
	var parsed map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &parsed); err != nil {
		t.Fatalf("expected valid single-line JSON log, got err=%v: %s", err, output)
	}
	for _, key := range []string{"password", "secret", "token", "dsn", "authorization", "cookie", "api_key", "api-key", "private_key", "credential"} {
		if parsed[key] != "[REDACTED]" {
			t.Fatalf("field %q = %v, want [REDACTED]", key, parsed[key])
		}
	}
	if parsed["safe_field"] != "kept-value" {
		t.Fatalf("safe_field = %v, want kept-value", parsed["safe_field"])
	}
}

type panicOnceExporter struct {
	mu       sync.Mutex
	calls    int
	exported []SpanData
}

func (e *panicOnceExporter) Export(_ context.Context, spans []SpanData) error {
	e.mu.Lock()
	e.calls++
	call := e.calls
	e.mu.Unlock()
	if call == 1 {
		panic("exporter-secret-boom")
	}
	e.mu.Lock()
	e.exported = append(e.exported, spans...)
	e.mu.Unlock()
	return nil
}

func (*panicOnceExporter) Shutdown(context.Context) error { return nil }

func TestAsyncExporterRecoversFromDelegatePanicAndContinues(t *testing.T) {
	var logs syncBuffer
	delegate := &panicOnceExporter{}
	runtime := NewRuntime(RuntimeConfig{
		Service:  "gateway",
		Exporter: delegate,
		QueueSize: 8,
		Logger:   NewJSONLogger(&logs, slog.LevelInfo),
	})

	_ = runtime.async.Export(context.Background(), []SpanData{{Service: "gateway", Name: "first"}})
	_ = runtime.async.Export(context.Background(), []SpanData{{Service: "gateway", Name: "second"}})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := runtime.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if runtime.FailedExports() != 1 {
		t.Fatalf("FailedExports = %d, want 1", runtime.FailedExports())
	}
	delegate.mu.Lock()
	exportedCount := len(delegate.exported)
	delegate.mu.Unlock()
	if exportedCount != 1 {
		t.Fatalf("exported spans after panic = %d, want 1", exportedCount)
	}
	logOutput := logs.String()
	if strings.Contains(logOutput, "exporter-secret-boom") {
		t.Fatalf("panic payload leaked into log: %s", logOutput)
	}
	prom := httptest.NewRecorder()
	runtime.Metrics.Handler().ServeHTTP(prom, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(prom.Body.String(), "hvac_goroutine_panics_total 1") {
		t.Fatalf("metrics missing panic counter: %s", prom.Body.String())
	}
}

type trackingReadCloser struct {
	reader  io.Reader
	drained bool
	closed  bool
}

func (t *trackingReadCloser) Read(p []byte) (int, error) {
	n, err := t.reader.Read(p)
	if err == io.EOF {
		t.drained = true
	}
	return n, err
}

func (t *trackingReadCloser) Close() error {
	t.closed = true
	return nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOTLPHTTPExporterDrainsResponseBodyBeforeClose(t *testing.T) {
	body := &trackingReadCloser{reader: strings.NewReader(`{"partialSuccess":{}}`)}
	exporter := NewOTLPHTTPExporter("http://otel-collector.test:4318", time.Second)
	exporter.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: body, Header: make(http.Header)}, nil
	})
	err := exporter.Export(context.Background(), []SpanData{{Service: "gateway", Name: "span"}})
	if err == nil {
		t.Fatal("expected error for 500 OTLP response")
	}
	if !body.drained || !body.closed {
		t.Fatalf("expected response body drained (%v) and closed (%v)", body.drained, body.closed)
	}
}
