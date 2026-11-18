package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

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
	lines := strings.Split(strings.TrimSpace(logOutput), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected single-line JSON log, got %d lines: %s", len(lines), logOutput)
	}
	var entry struct {
		Stack []stackFrame `json:"stack"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("unmarshal single-line panic JSON: %v", err)
	}
	if len(entry.Stack) == 0 || len(entry.Stack) > maxPanicStackFrames {
		t.Fatalf("expected 1..%d stack frames, got %d", maxPanicStackFrames, len(entry.Stack))
	}
	foundFrame := false
	for _, frame := range entry.Stack {
		if frame.Function == "" || frame.File == "" || frame.Line <= 0 {
			t.Fatalf("incomplete stack frame: %+v", frame)
		}
		if strings.Contains(frame.Function, "TestRecoverCatchesDirectDeferPanicAndRedactsError") {
			foundFrame = true
		}
	}
	if !foundFrame {
		t.Fatalf("expected panicking test frame in structured stack: %+v", entry.Stack)
	}
	prom := httptest.NewRecorder()
	registry.Handler().ServeHTTP(prom, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(prom.Body.String(), "hvac_goroutine_panics_total 1") {
		t.Fatalf("metrics missing panic counter: %s", prom.Body.String())
	}
}

func TestGoSafeAndRuntimeGoRecoverPanics(t *testing.T) {
	buffer := &syncBuffer{}
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

	deadline := time.Now().Add(2 * time.Second)
	var metricsBody, logOutput string
	for {
		prom := httptest.NewRecorder()
		runtime.Metrics.Handler().ServeHTTP(prom, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		metricsBody = prom.Body.String()
		logOutput = buffer.String()
		if strings.Contains(metricsBody, "hvac_goroutine_panics_total 2") &&
			strings.Contains(logOutput, `"worker.safe"`) &&
			strings.Contains(logOutput, `"worker.runtime"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected 2 recovered panics in metrics and logs, got metrics:\n%s\nlogs:\n%s", metricsBody, logOutput)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if strings.Contains(logOutput, "boom-") {
		t.Fatalf("panic payload leaked into log output: %s", logOutput)
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
