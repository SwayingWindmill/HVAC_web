package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
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

type fakeBackfillDetector struct {
	initCalls   atomic.Int32
	detectCalls atomic.Int32
	initErr     error
	detectErr   error
	enqueued    int
}

func (f *fakeBackfillDetector) InitializeWatermark(_ context.Context) error {
	f.initCalls.Add(1)
	return f.initErr
}

func (f *fakeBackfillDetector) DetectAndEnqueue(_ context.Context, _ time.Time) (int, error) {
	f.detectCalls.Add(1)
	if f.detectErr != nil {
		return 0, f.detectErr
	}
	return f.enqueued, nil
}

func TestParseDurationValueBackfillScanInterval(t *testing.T) {
	if got, err := parseDurationValue("", 30*time.Second, 0, 24*time.Hour); err != nil || got != 30*time.Second {
		t.Fatalf("empty got=(%v, %v), want (30s, nil)", got, err)
	}
	if got, err := parseDurationValue("0", 30*time.Second, 0, 24*time.Hour); err != nil || got != 0 {
		t.Fatalf("zero got=(%v, %v), want (0, nil)", got, err)
	}
	if got, err := parseDurationValue("15s", 30*time.Second, 0, 24*time.Hour); err != nil || got != 15*time.Second {
		t.Fatalf("15s got=(%v, %v), want (15s, nil)", got, err)
	}
	if _, err := parseDurationValue("-1s", 30*time.Second, 0, 24*time.Hour); err == nil {
		t.Fatal("expected error for negative duration")
	}
	if _, err := parseDurationValue("invalid", 30*time.Second, 0, 24*time.Hour); err == nil {
		t.Fatal("expected error for invalid duration")
	}
}

func TestRunBackfillDetectorLoopZeroIntervalDisabled(t *testing.T) {
	detector := &fakeBackfillDetector{enqueued: 2}
	runBackfillDetectorLoop(context.Background(), detector, 0, nil, slog.New(slog.NewTextHandler(&syncBuffer{}, nil)))
	if detector.initCalls.Load() != 0 || detector.detectCalls.Load() != 0 {
		t.Fatalf("disabled loop called init=%d detect=%d", detector.initCalls.Load(), detector.detectCalls.Load())
	}
}

func TestRunBackfillDetectorLoopInitializesScansAndRedactsErrors(t *testing.T) {
	var logs syncBuffer
	logger := observability.NewJSONLogger(&logs, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{Service: "metric-worker"})

	detector := &fakeBackfillDetector{enqueued: 3}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runBackfillDetectorLoop(ctx, detector, 10*time.Millisecond, telemetry, logger)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if detector.detectCalls.Load() >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runBackfillDetectorLoop did not exit on cancel")
	}

	if detector.initCalls.Load() != 1 {
		t.Fatalf("initCalls=%d, want 1", detector.initCalls.Load())
	}
	if detector.detectCalls.Load() < 2 {
		t.Fatalf("detectCalls=%d, want >= 2", detector.detectCalls.Load())
	}

	rec := httptest.NewRecorder()
	telemetry.Metrics.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	metricsBody := rec.Body.String()
	if !strings.Contains(metricsBody, `hvac_metric_backfill_scans_total{outcome="success"}`) {
		t.Fatalf("missing success scan metric:\n%s", metricsBody)
	}
	if !strings.Contains(metricsBody, `hvac_metric_backfill_recalc_enqueued_total`) {
		t.Fatalf("missing recalc enqueued metric:\n%s", metricsBody)
	}
	if !strings.Contains(logs.String(), `"msg":"metric_backfill_recalc_enqueued"`) {
		t.Fatalf("missing enqueued log: %s", logs.String())
	}

	// Verify errors are logged strictly with redacted error_code without raw error detail.
	var errLogs syncBuffer
	errLogger := observability.NewJSONLogger(&errLogs, slog.LevelInfo)
	errDetector := &fakeBackfillDetector{
		initErr:   errors.New("secret-init-dsn-leak"),
		detectErr: errors.New("secret-scan-password-leak"),
	}
	errCtx, errCancel := context.WithCancel(context.Background())
	errDone := make(chan struct{})
	go func() {
		defer close(errDone)
		runBackfillDetectorLoop(errCtx, errDetector, 10*time.Millisecond, telemetry, errLogger)
	}()
	errDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(errDeadline) {
		if errDetector.detectCalls.Load() >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	errCancel()
	<-errDone

	out := errLogs.String()
	if strings.Contains(out, "secret-init-dsn-leak") || strings.Contains(out, "secret-scan-password-leak") {
		t.Fatalf("error log leaked raw error string: %s", out)
	}
	if !strings.Contains(out, `"error_code":"METRIC_BACKFILL_WATERMARK_INIT_FAILED"`) || !strings.Contains(out, `"error_code":"METRIC_BACKFILL_SCAN_FAILED"`) {
		t.Fatalf("missing expected error_code fields in logs: %s", out)
	}
}
