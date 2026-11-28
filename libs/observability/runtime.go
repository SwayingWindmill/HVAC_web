package observability

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"net/netip"
	"os"
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
	Logger        *slog.Logger
}

type Runtime struct {
	Service       string
	Tracer        *Tracer
	Metrics       *Registry
	async         *AsyncExporter
	logger        *slog.Logger
	depMu         sync.RWMutex
	dependencies  []Dependency
	ready         atomic.Bool
	started       time.Time
	mu            sync.Mutex
	childRuntimes []*Runtime
}

func NewRuntime(config RuntimeConfig) *Runtime {
	delegate := config.Exporter
	if delegate == nil && config.OTLPEndpoint != "" {
		delegate = NewOTLPHTTPExporter(config.OTLPEndpoint, config.ExportTimeout)
	}
	if delegate == nil {
		delegate = NopExporter{}
	}
	logger := config.Logger
	if logger == nil {
		logger = NewJSONLogger(os.Stdout, slog.LevelInfo)
	}
	metrics := NewRegistry()
	if strings.TrimSpace(config.Service) != "" {
		_ = metrics.SetGauge(
			"hvac_service_up",
			"1 when this diagnostics server is serving.",
			map[string]string{"service": config.Service},
			1,
		)
	}
	async := newAsyncExporter(delegate, config.QueueSize, logger, metrics)
	return &Runtime{
		Service: config.Service,
		Tracer:  NewTracer(config.Service, async),
		Metrics: metrics,
		async:   async,
		logger:  logger,
		started: time.Now().UTC(),
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
	runtime.depMu.Lock()
	runtime.dependencies = kept
	runtime.depMu.Unlock()
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
	if parent == nil {
		parent = context.Background()
	}
	runtime.depMu.RLock()
	dependencies := append([]Dependency(nil), runtime.dependencies...)
	runtime.depMu.RUnlock()
	statuses := make([]dependencyStatus, len(dependencies))
	var group sync.WaitGroup
	for index, dependency := range dependencies {
		group.Add(1)
		go func(idx int, dep Dependency) {
			defer group.Done()
			statuses[idx] = dependencyStatus{Name: dep.Name, Required: dep.Required, Status: "up"}
			defer Recover(runtime.logger, runtime.Metrics, "observability.probe_dependency", func(any) {
				statuses[idx].Status = "down"
				statuses[idx].Error = "[REDACTED]"
			})
			ctx, cancel := context.WithTimeout(parent, time.Second)
			defer cancel()
			if err := dep.Check(ctx); err != nil {
				statuses[idx].Status = "down"
				statuses[idx].Error = "[REDACTED]"
				if runtime.logger != nil {
					runtime.logger.Warn(
						"readiness_dependency_failed",
						"service", runtime.Service,
						"dependency", dep.Name,
						"required", dep.Required,
						"cause", err.Error(),
					)
				}
			}
		}(index, dependency)
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

// AttachChildMetrics exposes a child Registry on this Runtime's /metrics endpoint.
func (runtime *Runtime) AttachChildMetrics(_ string, child *Registry) {
	if runtime == nil || runtime.Metrics == nil {
		return
	}
	runtime.Metrics.AttachChild(child)
}

// AttachChildRuntime exposes a child Runtime's metrics and telemetry gauges on
// this Runtime's /metrics endpoint.
func (runtime *Runtime) AttachChildRuntime(child *Runtime) {
	if runtime == nil || child == nil || child == runtime {
		return
	}
	runtime.mu.Lock()
	for _, existing := range runtime.childRuntimes {
		if existing == child {
			runtime.mu.Unlock()
			return
		}
	}
	runtime.childRuntimes = append(runtime.childRuntimes, child)
	runtime.mu.Unlock()
	if child.Metrics != nil {
		runtime.AttachChildMetrics(child.Service, child.Metrics)
	}
}

func (runtime *Runtime) refreshTelemetryGauges() {
	if runtime == nil || runtime.Metrics == nil {
		return
	}
	_ = runtime.Metrics.SetGauge("s0_telemetry_dropped_spans", "Spans dropped by the bounded telemetry queue.", map[string]string{"service": runtime.Service}, float64(runtime.DroppedSpans()))
	_ = runtime.Metrics.SetGauge("s0_telemetry_failed_exports", "Spans rejected by the telemetry exporter.", map[string]string{"service": runtime.Service}, float64(runtime.FailedExports()))
}

func (runtime *Runtime) DiagnosticsHandler() http.Handler {
	mux := http.NewServeMux()
	metricsHandler := runtime.Metrics.Handler()
	mux.HandleFunc("/metrics", func(writer http.ResponseWriter, request *http.Request) {
		runtime.refreshTelemetryGauges()
		runtime.mu.Lock()
		children := append([]*Runtime(nil), runtime.childRuntimes...)
		runtime.mu.Unlock()
		for _, child := range children {
			child.refreshTelemetryGauges()
		}
		metricsHandler.ServeHTTP(writer, request)
	})
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok\n"))
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
	if os.Getenv("ENABLE_DIAGNOSTICS_PPROF") == "true" {
		mux.Handle("/debug/pprof/", loopbackOnly(http.HandlerFunc(pprof.Index)))
		mux.Handle("/debug/pprof/cmdline", loopbackOnly(http.HandlerFunc(pprof.Cmdline)))
		mux.Handle("/debug/pprof/profile", loopbackOnly(http.HandlerFunc(pprof.Profile)))
		mux.Handle("/debug/pprof/symbol", loopbackOnly(http.HandlerFunc(pprof.Symbol)))
		mux.Handle("/debug/pprof/trace", loopbackOnly(http.HandlerFunc(pprof.Trace)))
	}
	return mux
}

func loopbackOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !isLoopbackRemoteAddr(request.RemoteAddr) {
			http.Error(writer, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func isLoopbackRemoteAddr(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		return false
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return addr.Unmap().IsLoopback()
}

func NewJSONLogger(writer io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: level, ReplaceAttr: redactLogAttribute}))
}

func redactLogAttribute(_ []string, attribute slog.Attr) slog.Attr {
	key := strings.ReplaceAll(strings.ToLower(attribute.Key), "-", "_")
	for _, fragment := range []string{
		"authorization",
		"cookie",
		"token",
		"grant",
		"secret",
		"password",
		"body",
		"connection_string",
		"database_url",
		"dsn",
		"api_key",
		"apikey",
		"private_key",
		"privatekey",
		"credential",
	} {
		if strings.Contains(key, fragment) {
			return slog.String(attribute.Key, "[REDACTED]")
		}
	}
	if key == "error" {
		return slog.String(attribute.Key, "[REDACTED]")
	}
	return attribute
}
