package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
