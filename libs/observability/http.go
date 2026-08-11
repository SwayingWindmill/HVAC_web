package observability

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var metricNamespacePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)

type HTTPInstrumentationConfig struct {
	Namespace string
	Service   string
	SpanName  string
	Route     func(*http.Request) string
}

type httpInstrumentation struct {
	handler    http.Handler
	runtime    *Runtime
	config     HTTPInstrumentationConfig
	inFlight   atomic.Int64
	metricBase string
}

type instrumentationResponseWriter struct {
	http.ResponseWriter
	status int
}

func (writer *instrumentationResponseWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *instrumentationResponseWriter) Write(body []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(body)
}

func InstrumentHTTP(handler http.Handler, runtime *Runtime, config HTTPInstrumentationConfig) http.Handler {
	if handler == nil || runtime == nil || runtime.Metrics == nil || runtime.Tracer == nil {
		return handler
	}
	config.Namespace = strings.TrimSpace(config.Namespace)
	config.Service = strings.TrimSpace(config.Service)
	config.SpanName = strings.TrimSpace(config.SpanName)
	if !metricNamespacePattern.MatchString(config.Namespace) || config.Service == "" || config.Route == nil {
		return handler
	}
	if config.SpanName == "" {
		config.SpanName = "http.request"
	}
	return &httpInstrumentation{handler: handler, runtime: runtime, config: config, metricBase: config.Namespace + "_http"}
}

func (instrumentation *httpInstrumentation) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	route := strings.TrimSpace(instrumentation.config.Route(request))
	if route == "" || len(route) > 96 || strings.ContainsAny(route, "\r\n") {
		route = "unknown"
	}
	method := strings.ToUpper(strings.TrimSpace(request.Method))
	if method == "" {
		method = "UNKNOWN"
	}
	inFlightLabels := map[string]string{"service": instrumentation.config.Service}
	inFlight := instrumentation.inFlight.Add(1)
	_ = instrumentation.runtime.Metrics.SetGauge(instrumentation.metricBase+"_in_flight", "In-flight HTTP requests.", inFlightLabels, float64(inFlight))
	defer func() {
		remaining := instrumentation.inFlight.Add(-1)
		_ = instrumentation.runtime.Metrics.SetGauge(instrumentation.metricBase+"_in_flight", "In-flight HTTP requests.", inFlightLabels, float64(remaining))
	}()

	ctx := instrumentation.runtime.Tracer.ExtractHTTP(request.Context(), request.Header)
	ctx, span := instrumentation.runtime.Tracer.Start(ctx, instrumentation.config.SpanName, SpanKindServer, map[string]any{
		"http.method": method,
		"http.route":  route,
	})
	started := time.Now()
	captured := &instrumentationResponseWriter{ResponseWriter: writer}
	instrumentation.handler.ServeHTTP(captured, request.WithContext(ctx))
	if captured.status == 0 {
		captured.status = http.StatusOK
	}
	statusClass := strconv.Itoa(captured.status/100) + "xx"
	resultLabels := map[string]string{"service": instrumentation.config.Service, "method": method, "route": route, "status_class": statusClass}
	_ = instrumentation.runtime.Metrics.AddCounter(instrumentation.metricBase+"_requests_total", "HTTP requests by bounded route and status class.", resultLabels, 1)
	_ = instrumentation.runtime.Metrics.ObserveHistogram(instrumentation.metricBase+"_request_duration_seconds", "HTTP request duration by bounded route and status class.", resultLabels, time.Since(started).Seconds(), nil)
	span.SetAttributes(map[string]any{"http.status_code": captured.status, "http.status_class": statusClass})
	if captured.status >= http.StatusInternalServerError {
		span.SetStatus("error", statusClass)
	} else {
		span.SetStatus("ok", "")
	}
	span.End()
}
