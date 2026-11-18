package observability

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestInstrumentHTTPUsesBoundedRouteAndTraceContext(t *testing.T) {
	runtime := NewRuntime(RuntimeConfig{Service: "command-service"})
	handler := InstrumentHTTP(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if TraceID(request.Context()) != "0123456789abcdef0123456789abcdef" {
			t.Fatalf("trace id=%q", TraceID(request.Context()))
		}
		writer.WriteHeader(http.StatusCreated)
	}), runtime, HTTPInstrumentationConfig{
		Namespace: "hvac_command",
		Service:   "command-service",
		SpanName:  "http.command.request",
		Route:     func(*http.Request) string { return "commands.create" },
	})
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/commands/018f3e00-0000-7000-8000-000000000001", nil)
	request.Header.Set("traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d", recorder.Code)
	}
	metrics := httptest.NewRecorder()
	runtime.Metrics.Handler().ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metrics.Body.String()
	for _, marker := range []string{
		`hvac_command_http_requests_total{method="POST",route="commands.create",service="command-service",status_class="2xx"} 1`,
		`hvac_command_http_request_duration_seconds_count{method="POST",route="commands.create",service="command-service",status_class="2xx"} 1`,
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("missing %q in metrics:\n%s", marker, body)
		}
	}
	if strings.Contains(body, "018f3e00") || strings.Contains(body, "trace_id") {
		t.Fatalf("metrics leaked high-cardinality identity: %s", body)
	}
}

func TestInstrumentHTTPFailsClosedToUnknownRouteLabel(t *testing.T) {
	runtime := NewRuntime(RuntimeConfig{Service: "alarm-service"})
	handler := InstrumentHTTP(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
	}), runtime, HTTPInstrumentationConfig{
		Namespace: "hvac_alarm",
		Service:   "alarm-service",
		Route:     func(*http.Request) string { return strings.Repeat("x", 200) },
	})
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/secret/identifier", nil))
	metrics := httptest.NewRecorder()
	runtime.Metrics.Handler().ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), `route="unknown"`) {
		t.Fatalf("metrics=%s", metrics.Body.String())
	}
}

type hijackFlushResponseWriter struct {
	*httptest.ResponseRecorder
	hijacked bool
	flushed  bool
}

func (w *hijackFlushResponseWriter) Flush() {
	w.flushed = true
	w.ResponseRecorder.Flush()
}

func (w *hijackFlushResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	c1, c2 := net.Pipe()
	_ = c2.Close()
	return c1, bufio.NewReadWriter(bufio.NewReader(c1), bufio.NewWriter(c1)), nil
}

func TestInstrumentHTTPSupportsUnwrapAndFlush(t *testing.T) {
	runtime := NewRuntime(RuntimeConfig{Service: "gateway", Logger: NewJSONLogger(io.Discard, slog.LevelInfo)})
	handler := InstrumentHTTP(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		flusher, ok := writer.(http.Flusher)
		if !ok {
			t.Fatal("instrumented ResponseWriter does not implement http.Flusher")
		}
		flusher.Flush()
		conn, _, err := http.NewResponseController(writer).Hijack()
		if err != nil {
			t.Fatalf("ResponseController.Hijack failed: %v", err)
		}
		_ = conn.Close()
	}), runtime, HTTPInstrumentationConfig{
		Namespace: "hvac_gateway",
		Service:   "gateway",
		Route:     func(*http.Request) string { return "realtime.ws" },
	})

	underlying := &hijackFlushResponseWriter{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(underlying, httptest.NewRequest(http.MethodGet, "/realtime/websocket", nil))
	if !underlying.flushed || !underlying.hijacked {
		t.Fatalf("expected flushed (%v) and hijacked (%v)", underlying.flushed, underlying.hijacked)
	}
}

func TestInstrumentHTTPRecoversPanicAndRethrowsErrAbortHandler(t *testing.T) {
	var logs syncBuffer
	exporter := &MemoryExporter{}
	runtime := NewRuntime(RuntimeConfig{
		Service:   "command-service",
		Exporter:  exporter,
		QueueSize: 16,
		Logger:    NewJSONLogger(&logs, slog.LevelInfo),
	})

	panicBeforeHeader := InstrumentHTTP(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("handler-secret-panic")
	}), runtime, HTTPInstrumentationConfig{
		Namespace: "hvac_command",
		Service:   "command-service",
		Route:     func(*http.Request) string { return "commands.panic_early" },
	})
	rec1 := httptest.NewRecorder()
	panicBeforeHeader.ServeHTTP(rec1, httptest.NewRequest(http.MethodPost, "/internal/v1/commands", nil))
	if rec1.Code != http.StatusInternalServerError {
		t.Fatalf("panic before header status = %d, want 500", rec1.Code)
	}

	panicAfterHeader := InstrumentHTTP(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusCreated)
		panic("handler-secret-late-panic")
	}), runtime, HTTPInstrumentationConfig{
		Namespace: "hvac_command",
		Service:   "command-service",
		Route:     func(*http.Request) string { return "commands.panic_late" },
	})
	rec2 := httptest.NewRecorder()
	panicAfterHeader.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/internal/v1/commands", nil))
	if rec2.Code != http.StatusCreated {
		t.Fatalf("panic after header status = %d, want 201", rec2.Code)
	}

	abortHandler := InstrumentHTTP(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}), runtime, HTTPInstrumentationConfig{
		Namespace: "hvac_command",
		Service:   "command-service",
		Route:     func(*http.Request) string { return "commands.abort" },
	})
	var abortRecovered any
	func() {
		defer func() { abortRecovered = recover() }()
		abortHandler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/internal/v1/commands", nil))
	}()
	if abortRecovered != http.ErrAbortHandler {
		t.Fatalf("abortRecovered = %v, want http.ErrAbortHandler", abortRecovered)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := runtime.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	spans := exporter.Spans()
	if len(spans) != 3 {
		t.Fatalf("expected 3 ended spans, got %d", len(spans))
	}
	for i, span := range spans {
		if span.StatusCode != "error" {
			t.Fatalf("span[%d] status = %q, want error", i, span.StatusCode)
		}
	}

	logOutput := logs.String()
	if strings.Contains(logOutput, "handler-secret-") {
		t.Fatalf("panic payload leaked into log: %s", logOutput)
	}
	if !strings.Contains(logOutput, `"stack":[`) {
		t.Fatalf("expected structured stack frames in log: %s", logOutput)
	}

	metrics := httptest.NewRecorder()
	runtime.Metrics.Handler().ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metrics.Body.String()
	for _, want := range []string{
		`hvac_goroutine_panics_total 2`,
		`hvac_command_http_in_flight{service="command-service"} 0`,
		`hvac_command_http_requests_total{method="POST",route="commands.panic_early",service="command-service",status_class="5xx"} 1`,
		`hvac_command_http_requests_total{method="POST",route="commands.panic_late",service="command-service",status_class="2xx"} 1`,
		`hvac_command_http_requests_total{method="GET",route="commands.abort",service="command-service",status_class="5xx"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q in:\n%s", want, body)
		}
	}
}
