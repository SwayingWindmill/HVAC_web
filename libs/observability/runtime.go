package observability

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Dependency is a readiness probe against one external dependency.
// A failed Required dependency makes the service not-ready; a failed optional
// dependency only reports degraded status so one flaky side dependency cannot
// take the whole service out of rotation.
type Dependency struct {
	Name     string
	Required bool
	Check    func(context.Context) error
}

type RuntimeConfig struct {
	Service       string
	OTLPEndpoint  string
	QueueSize     int
	ExportTimeout time.Duration
	Exporter      Exporter
}

type Runtime struct {
	Service        string
	Tracer         *Tracer
	Metrics        *Registry
	async          *AsyncExporter
	dependencies   []Dependency
	ready          atomic.Bool
	started        time.Time
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
	return &Runtime{
		Service: config.Service,
		Tracer:  NewTracer(config.Service, async),
		Metrics:        NewRegistry(),
		async:          async,
		started:        time.Now().UTC(),
	}
}

// SetDependencies replaces the dependency probe set. Probes with a nil Check
// are ignored.
func (runtime *Runtime) SetDependencies(dependencies ...Dependency) {
	if runtime == nil {
		return
	}
	kept := make([]Dependency, 0, len(dependencies))
	for _, dependency := range dependencies {
		if dependency.Check == nil {
			continue
		}
		kept = append(kept, dependency)
	}
	runtime.dependencies = kept
}

func (runtime *Runtime) MarkReady() {
	if runtime != nil {
		runtime.ready.Store(true)
	}
}

func (runtime *Runtime) MarkNotReady() {
	if runtime != nil {
		runtime.ready.Store(false)
	}
}

func (runtime *Runtime) Ready() bool {
	return runtime != nil && runtime.ready.Load()
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

type dependencyStatus struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

func (runtime *Runtime) probeDependencies(parent context.Context) []dependencyStatus {
	dependencies := runtime.dependencies
	statuses := make([]dependencyStatus, len(dependencies))
	var group sync.WaitGroup
	for index, dependency := range dependencies {
		group.Add(1)
		go func() {
			defer group.Done()
			statuses[index] = dependencyStatus{Name: dependency.Name, Required: dependency.Required, Status: "up"}
			ctx, cancel := context.WithTimeout(parent, time.Second)
			defer cancel()
			if err := dependency.Check(ctx); err != nil {
				statuses[index].Status = "down"
				statuses[index].Error = err.Error()
			}
		}()
	}
	group.Wait()
	return statuses
}

func (runtime *Runtime) writeHealth(writer http.ResponseWriter, status string, code int, dependencies []dependencyStatus) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(code)
	payload := map[string]any{
		"status":    status,
		"service":   runtime.Service,
		"startedAt": runtime.started.Format(time.RFC3339Nano),
	}
	if dependencies != nil {
		payload["dependencies"] = dependencies
	}
	_ = json.NewEncoder(writer).Encode(payload)
}

func (runtime *Runtime) DiagnosticsHandler() http.Handler {
	mux := http.NewServeMux()
	metricsHandler := runtime.Metrics.Handler()
	mux.HandleFunc("/metrics", func(writer http.ResponseWriter, request *http.Request) {
		_ = runtime.Metrics.SetGauge("s0_telemetry_dropped_spans", "Spans dropped by the bounded telemetry queue.", map[string]string{"service": runtime.Service}, float64(runtime.DroppedSpans()))
		_ = runtime.Metrics.SetGauge("s0_telemetry_failed_exports", "Spans rejected by the telemetry exporter.", map[string]string{"service": runtime.Service}, float64(runtime.FailedExports()))
		metricsHandler.ServeHTTP(writer, request)
	})
	mux.HandleFunc("/health/startup", func(writer http.ResponseWriter, _ *http.Request) {
		runtime.writeHealth(writer, "started", http.StatusOK, nil)
	})
	mux.HandleFunc("/health/live", func(writer http.ResponseWriter, _ *http.Request) {
		runtime.writeHealth(writer, "live", http.StatusOK, nil)
	})
	mux.HandleFunc("/health/ready", func(writer http.ResponseWriter, request *http.Request) {
		if !runtime.Ready() {
			runtime.writeHealth(writer, "not-ready", http.StatusServiceUnavailable, nil)
			return
		}
		statuses := runtime.probeDependencies(request.Context())
		requiredDown, optionalDown := false, false
		for _, status := range statuses {
			if status.Status != "down" {
				continue
			}
			if status.Required {
				requiredDown = true
			} else {
				optionalDown = true
			}
		}
		for _, status := range statuses {
			up := 0.0
			if status.Status == "up" {
				up = 1
			}
			required := "false"
			if status.Required {
				required = "true"
			}
			_ = runtime.Metrics.SetGauge("s0_dependency_up", "Dependency health reported by the readiness probes.", map[string]string{"service": runtime.Service, "dependency": status.Name, "required": required}, up)
		}
		switch {
		case requiredDown:
			runtime.writeHealth(writer, "not-ready", http.StatusServiceUnavailable, statuses)
		case optionalDown:
			runtime.writeHealth(writer, "degraded", http.StatusOK, statuses)
		default:
			runtime.writeHealth(writer, "ready", http.StatusOK, statuses)
		}
	})
	mux.HandleFunc("/diagnostics", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"status": "ok", "service": runtime.Service, "ready": runtime.Ready(),
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
