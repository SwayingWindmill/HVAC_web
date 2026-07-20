package observability

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type RuntimeConfig struct {
	Service       string
	OTLPEndpoint  string
	QueueSize     int
	ExportTimeout time.Duration
	Exporter      Exporter
}

type Runtime struct {
	Service string
	Tracer  *Tracer
	Metrics *Registry
	async   *AsyncExporter
}

func NewRuntime(config RuntimeConfig) *Runtime {
	delegate := config.Exporter
	if delegate == nil && config.OTLPEndpoint != "" {
		delegate = NewOTLPHTTPExporter(config.OTLPEndpoint, config.ExportTimeout)
	}
	if delegate == nil {
		delegate = NopExporter{}
	}
	async := NewAsyncExporter(delegate, config.QueueSize)
	return &Runtime{Service: config.Service, Tracer: NewTracer(config.Service, async), Metrics: NewRegistry(), async: async}
}

func (runtime *Runtime) Shutdown(ctx context.Context) error {
	if runtime == nil || runtime.async == nil {
		return nil
	}
	return runtime.async.Shutdown(ctx)
}

func (runtime *Runtime) DroppedSpans() uint64 {
	if runtime == nil || runtime.async == nil {
		return 0
	}
	return runtime.async.Dropped()
}

func (runtime *Runtime) FailedExports() uint64 {
	if runtime == nil || runtime.async == nil {
		return 0
	}
	return runtime.async.Failed()
}

func (runtime *Runtime) DiagnosticsHandler() http.Handler {
	mux := http.NewServeMux()
	metricsHandler := runtime.Metrics.Handler()
	mux.HandleFunc("/metrics", func(writer http.ResponseWriter, request *http.Request) {
		_ = runtime.Metrics.SetGauge("s0_telemetry_dropped_spans", "Spans dropped by the bounded telemetry queue.", map[string]string{"service": runtime.Service}, float64(runtime.DroppedSpans()))
		_ = runtime.Metrics.SetGauge("s0_telemetry_failed_exports", "Spans rejected by the telemetry exporter.", map[string]string{"service": runtime.Service}, float64(runtime.FailedExports()))
		metricsHandler.ServeHTTP(writer, request)
	})
	health := func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]string{"status": "ok", "service": runtime.Service})
	}
	mux.HandleFunc("/health/live", health)
	mux.HandleFunc("/health/ready", health)
	mux.HandleFunc("/diagnostics", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"status": "ok", "service": runtime.Service,
			"telemetry": map[string]any{"droppedSpans": runtime.DroppedSpans(), "failedExports": runtime.FailedExports()},
		})
	})
	return mux
}

func NewJSONLogger(writer io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: level, ReplaceAttr: redactLogAttribute}))
}

func redactLogAttribute(_ []string, attribute slog.Attr) slog.Attr {
	key := strings.ToLower(attribute.Key)
	for _, fragment := range []string{"authorization", "cookie", "token", "grant", "secret", "password", "body", "connection_string", "database_url", "dsn"} {
		if strings.Contains(key, fragment) {
			return slog.String(attribute.Key, "[REDACTED]")
		}
	}
	if key == "error" {
		return slog.String(attribute.Key, "[REDACTED]")
	}
	return attribute
}
