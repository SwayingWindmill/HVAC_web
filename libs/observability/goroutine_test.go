package observability

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestRecoverCatchesDirectDeferPanicAndRedactsError(t *testing.T) {
	buffer := &bytes.Buffer{}
	logger := NewJSONLogger(buffer, slog.LevelInfo)
	registry := NewRegistry()

	var hookValue any
	func() {
		defer Recover(logger, registry, "test.worker", func(recovered any) {
			hookValue = recovered
		})
		panic("secret-connection-string")
	}()

	if hookValue != "secret-connection-string" {
		t.Fatalf("hookValue = %v, want panic payload passed to hook", hookValue)
	}
	logOutput := buffer.String()
	if strings.Contains(logOutput, "secret-connection-string") || !strings.Contains(logOutput, `"error":"[REDACTED]"`) || !strings.Contains(logOutput, `"component":"test.worker"`) {
		t.Fatalf("unexpected panic log output: %s", logOutput)
	}
	prom := httptest.NewRecorder()
	registry.Handler().ServeHTTP(prom, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(prom.Body.String(), "hvac_goroutine_panics_total 1") {
		t.Fatalf("metrics missing panic counter: %s", prom.Body.String())
	}
}

func TestGoSafeAndRuntimeGoRecoverPanics(t *testing.T) {
	buffer := &bytes.Buffer{}
	logger := NewJSONLogger(buffer, slog.LevelInfo)
	runtime := NewRuntime(RuntimeConfig{Service: "test", QueueSize: 1})

	var wg sync.WaitGroup
	wg.Add(2)
	GoSafe(logger, runtime.Metrics, "worker.safe", func() {
		defer wg.Done()
		panic("boom-safe")
	})
	runtime.Go(logger, "worker.runtime", func() {
		defer wg.Done()
		panic("boom-runtime")
	})
	wg.Wait()

	prom := httptest.NewRecorder()
	runtime.Metrics.Handler().ServeHTTP(prom, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(prom.Body.String(), "hvac_goroutine_panics_total 2") {
		t.Fatalf("expected 2 recovered panics in metrics, got:\n%s", prom.Body.String())
	}
	if strings.Contains(buffer.String(), "boom-") {
		t.Fatalf("panic payload leaked into log output: %s", buffer.String())
	}
}

func TestProbeDependenciesRecoversFromPanic(t *testing.T) {
	runtime := NewRuntime(RuntimeConfig{Service: "test", QueueSize: 1})
	runtime.SetDependencies(
		Dependency{Name: "healthy", Required: true, Check: func(context.Context) error { return nil }},
		Dependency{Name: "panicking", Required: true, Check: func(context.Context) error { panic("probe-secret-boom") }},
	)
	runtime.MarkReady()

	rec := httptest.NewRecorder()
	runtime.DiagnosticsHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d, want 503", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "probe-secret-boom") || !strings.Contains(body, `"name":"panicking"`) || !strings.Contains(body, `"error":"[REDACTED]"`) {
		t.Fatalf("unexpected readiness response: %s", body)
	}
}
