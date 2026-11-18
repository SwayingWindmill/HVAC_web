package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/ownershipregistry"
	"github.com/redis/go-redis/v9"
)

type panicAuditSink struct {
	panicValue any
}

func (s panicAuditSink) Record(_ context.Context, _ ownershipregistry.AuditRecord) error {
	if s.panicValue != nil {
		panic(s.panicValue)
	}
	return nil
}

type trackedReadCloser struct {
	data   []byte
	offset int
	readN  int
	closed bool
}

func (r *trackedReadCloser) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	r.readN += n
	return n, nil
}

func (r *trackedReadCloser) Close() error {
	r.closed = true
	return nil
}

func TestHandlerServeHTTPRecoversPanicAndRethrowsErrAbortHandler(t *testing.T) {
	fix := newConcentratorGatewayFixture(t)
	var logs bytes.Buffer
	logger := observability.NewJSONLogger(&logs, slog.LevelInfo)
	exporter := &observability.MemoryExporter{}
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service:       serviceName,
		Exporter:      exporter,
		QueueSize:     16,
		ExportTimeout: 100 * time.Millisecond,
		Logger:        logger,
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = telemetry.Shutdown(ctx)
	})
	fix.handler.logger = logger
	fix.handler.observability = telemetry

	fix.handler.routeAudit = panicAuditSink{panicValue: "seeded-panic-secret"}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/"+fix.gatewayID+"/concentrators/1/status", nil)
	fix.authenticate(req, false)
	rec := httptest.NewRecorder()
	fix.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic recovery status = %d, want 500 (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "INTERNAL_ERROR") {
		t.Fatalf("expected INTERNAL_ERROR problem body, got %s", rec.Body.String())
	}

	var foundPanicLog bool
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("expected single-line valid JSON log, got error %v on line %q", err, line)
		}
		if entry["msg"] == "http_handler_panicked" {
			foundPanicLog = true
			stack, ok := entry["stack"].([]any)
			if !ok || len(stack) == 0 {
				t.Fatalf("expected structured stack frames in panic log, got %#v", entry["stack"])
			}
		}
	}
	if !foundPanicLog {
		t.Fatalf("missing http_handler_panicked log entry: %s", logs.String())
	}
	if strings.Contains(logs.String(), "seeded-panic-secret") {
		t.Fatalf("panic payload leaked into log: %s", logs.String())
	}

	prom := httptest.NewRecorder()
	telemetry.Metrics.Handler().ServeHTTP(prom, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(prom.Body.String(), "hvac_goroutine_panics_total 1") {
		t.Fatalf("expected hvac_goroutine_panics_total 1, got: %s", prom.Body.String())
	}

	// Verify http.ErrAbortHandler passes through without writing 500 or incrementing panic metric.
	fix.handler.routeAudit = panicAuditSink{panicValue: http.ErrAbortHandler}
	abortReq := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/"+fix.gatewayID+"/concentrators/1/status", nil)
	fix.authenticate(abortReq, false)
	abortRec := httptest.NewRecorder()

	var rethrown any
	func() {
		defer func() {
			rethrown = recover()
		}()
		fix.handler.ServeHTTP(abortRec, abortReq)
	}()
	if rethrown != http.ErrAbortHandler {
		t.Fatalf("expected http.ErrAbortHandler to be re-panicked, got %#v", rethrown)
	}
	if abortRec.Body.Len() != 0 {
		t.Fatalf("expected no body written on ErrAbortHandler, got %s", abortRec.Body.String())
	}

	promAfter := httptest.NewRecorder()
	telemetry.Metrics.Handler().ServeHTTP(promAfter, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(promAfter.Body.String(), "hvac_goroutine_panics_total 1") {
		t.Fatalf("ErrAbortHandler unexpectedly incremented panic counter: %s", promAfter.Body.String())
	}
	if !strings.Contains(promAfter.Body.String(), `result="error"`) {
		t.Fatalf("expected s0_http_requests_total error increment on abort: %s", promAfter.Body.String())
	}
}

func TestGatewayDrainAndCloseBodyCapsAndCloses(t *testing.T) {
	drainAndCloseBody(nil)

	small := &trackedReadCloser{data: bytes.Repeat([]byte("x"), 512)}
	drainAndCloseBody(small)
	if small.readN != 512 || !small.closed {
		t.Fatalf("small readN=%d closed=%v, want readN=512 closed=true", small.readN, small.closed)
	}

	large := &trackedReadCloser{data: bytes.Repeat([]byte("y"), 128<<10)}
	drainAndCloseBody(large)
	if large.readN != maxDrainBodyBytes || !large.closed {
		t.Fatalf("large readN=%d closed=%v, want readN=%d closed=true", large.readN, large.closed, maxDrainBodyBytes)
	}
}

func TestApplyLoginStateRedisBounds(t *testing.T) {
	defaults := &redis.Options{}
	applyLoginStateRedisBounds(defaults)
	if defaults.DialTimeout != 3*time.Second || defaults.ReadTimeout != 2*time.Second ||
		defaults.WriteTimeout != 2*time.Second || defaults.PoolTimeout != 3*time.Second ||
		defaults.PoolSize != 16 || defaults.MinIdleConns != 2 {
		t.Fatalf("unexpected default login state redis options: %+v", defaults)
	}

	oversized := &redis.Options{
		DialTimeout:  20 * time.Second,
		ReadTimeout:  20 * time.Second,
		WriteTimeout: 20 * time.Second,
		PoolTimeout:  20 * time.Second,
		PoolSize:     500,
		MinIdleConns: 0,
	}
	applyLoginStateRedisBounds(oversized)
	if oversized.DialTimeout > 5*time.Second || oversized.ReadTimeout > 5*time.Second ||
		oversized.WriteTimeout > 5*time.Second || oversized.PoolTimeout > 5*time.Second ||
		oversized.PoolSize != 32 || oversized.MinIdleConns != 2 {
		t.Fatalf("oversized login state redis options were not clamped: %+v", oversized)
	}
}
