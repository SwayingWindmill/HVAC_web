package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/metric/pkg/metric"
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
		if detector.detectCalls.Load() >= 2 && strings.Contains(logs.String(), `"msg":"metric_backfill_recalc_enqueued"`) {
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
		out := errLogs.String()
		if errDetector.detectCalls.Load() >= 1 &&
			strings.Contains(out, `"error_code":"METRIC_BACKFILL_WATERMARK_INIT_FAILED"`) &&
			strings.Contains(out, `"error_code":"METRIC_BACKFILL_SCAN_FAILED"`) {
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

type metricRecordingExporter struct {
	record func(string)
}

func (e *metricRecordingExporter) Export(context.Context, []observability.SpanData) error {
	return nil
}

func (e *metricRecordingExporter) Shutdown(context.Context) error {
	if e.record != nil {
		e.record("telemetry_shutdown")
	}
	return nil
}

func TestShutdownMetricWorkerMarksNotReadyImmediatelyAndOrdersTeardown(t *testing.T) {
	var mu sync.Mutex
	var events []string
	record := func(ev string) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}

	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service:  "metric-worker-test",
		Exporter: &metricRecordingExporter{record: record},
	})
	telemetry.MarkReady()

	httpEntered := make(chan struct{})
	releaseHTTP := make(chan struct{})
	diagSrv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(httpEntered)
			<-releaseHTTP
			record("diag_handler_done")
			w.WriteHeader(http.StatusOK)
		}),
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	diagDone := make(chan struct{})
	go func() {
		defer close(diagDone)
		_ = diagSrv.Serve(ln)
		record("diag_goroutine_done")
	}()

	workerCtx, cancelWorker := context.WithCancel(context.Background())
	var bgWG sync.WaitGroup
	workerStarted := make(chan struct{})
	bgWG.Add(1)
	go func() {
		defer bgWG.Done()
		close(workerStarted)
		<-workerCtx.Done()
		record("worker_done")
	}()
	<-workerStarted

	reqErrCh := make(chan error, 1)
	go func() {
		resp, reqErr := http.Get("http://" + ln.Addr().String() + "/metrics")
		if reqErr == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		reqErrCh <- reqErr
	}()
	<-httpEntered

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		shutdownMetricWorker(
			nil,
			telemetry,
			diagSrv,
			diagDone,
			cancelWorker,
			&bgWG,
			func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = telemetry.Shutdown(ctx)
			},
			func() { record("runtime_closed") },
			2*time.Second,
		)
	}()

	deadline := time.Now().Add(time.Second)
	for telemetry.Ready() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if telemetry.Ready() {
		close(releaseHTTP)
		t.Fatal("expected telemetry.MarkNotReady() immediately on shutdown")
	}
	if workerCtx.Err() != nil {
		close(releaseHTTP)
		t.Fatalf("workerCtx canceled before diagnostics HTTP shutdown completed")
	}

	close(releaseHTTP)
	if err := <-reqErrCh; err != nil {
		t.Fatalf("diagnostics request failed: %v", err)
	}
	select {
	case <-shutdownDone:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdownMetricWorker timed out")
	}

	mu.Lock()
	got := slices.Clone(events)
	mu.Unlock()
	diagHandlerIdx := slices.Index(got, "diag_handler_done")
	diagGoroutineIdx := slices.Index(got, "diag_goroutine_done")
	workerIdx := slices.Index(got, "worker_done")
	telemetryIdx := slices.Index(got, "telemetry_shutdown")
	runtimeIdx := slices.Index(got, "runtime_closed")
	if diagHandlerIdx < 0 || diagGoroutineIdx < 0 || workerIdx < 0 || telemetryIdx < 0 || runtimeIdx < 0 {
		t.Fatalf("missing expected shutdown events: %v", got)
	}
	if diagHandlerIdx >= workerIdx || diagGoroutineIdx >= workerIdx {
		t.Fatalf("expected diagnostics drain and exit before worker_done: %v", got)
	}
	if workerIdx >= telemetryIdx || telemetryIdx >= runtimeIdx {
		t.Fatalf("expected worker_done < telemetry_shutdown < runtime_closed: %v", got)
	}
}

func TestMetricWorkerEnvHelpersReturnErrors(t *testing.T) {
	t.Setenv("TEST_METRIC_REQUIRED_EMPTY", "")
	if _, err := requiredEnv("TEST_METRIC_REQUIRED_EMPTY"); err == nil {
		t.Fatal("expected error for empty requiredEnv")
	}
	t.Setenv("TEST_METRIC_DURATION_BAD", "bogus")
	if _, err := durationEnv("TEST_METRIC_DURATION_BAD", time.Second, time.Second, time.Minute); err == nil {
		t.Fatal("expected error for invalid durationEnv")
	}
	t.Setenv("TEST_METRIC_INT_BAD", "0")
	if _, err := integerEnv("TEST_METRIC_INT_BAD", 5, 1, 100); err == nil {
		t.Fatal("expected error for out-of-range integerEnv")
	}
	t.Setenv("RECONCILE_INTERVAL", "2m")
	if got, err := reconcileIntervalEnv(); err != nil || got != 2*time.Minute {
		t.Fatalf("reconcileIntervalEnv() = %v, %v; want 2m", got, err)
	}
}

type fakeDirtyRunner struct {
	calls atomic.Int32
	stats metric.RecalcBatchStats
	err   error
}

func (f *fakeDirtyRunner) ProcessOnce(context.Context) (metric.RecalcBatchStats, error) {
	f.calls.Add(1)
	return f.stats, f.err
}

type fakeScopeListerAndReconciler struct {
	scopes []metric.TenantSiteScope
	calls  atomic.Int32
}

func (f *fakeScopeListerAndReconciler) ListStalePublicationScopes(context.Context, time.Time, int) ([]metric.TenantSiteScope, error) {
	return f.scopes, nil
}

func (f *fakeScopeListerAndReconciler) ReconcileScope(context.Context, string, string, time.Time, int) (int, error) {
	f.calls.Add(1)
	return 1, nil
}

func TestDirtyRecalcAndReconcileLoopsCancelCleanly(t *testing.T) {
	telemetry := observability.NewRuntime(observability.RuntimeConfig{Service: "metric-worker-test"})
	var logs syncBuffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	runner := &fakeDirtyRunner{stats: metric.RecalcBatchStats{Claimed: 1, CoalescedSpan: 1, ExecutedRuns: 2, Succeeded: 1}}
	reconciler := &fakeScopeListerAndReconciler{scopes: []metric.TenantSiteScope{{TenantID: "t1", SiteID: "s1"}}}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		runDirtyRecalcLoop(ctx, runner, 5*time.Millisecond, telemetry, logger)
	}()
	go func() {
		defer wg.Done()
		runPublicationReconcileLoop(ctx, reconciler, reconciler, 5*time.Millisecond, time.Minute, telemetry, logger)
	}()

	deadline := time.Now().Add(time.Second)
	for (runner.calls.Load() == 0 || reconciler.calls.Load() == 0) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("background recalc/reconcile loops did not exit on cancel")
	}
	if runner.calls.Load() == 0 || reconciler.calls.Load() == 0 {
		t.Fatalf("expected both loops to run at least once: dirty=%d reconcile=%d", runner.calls.Load(), reconciler.calls.Load())
	}
}
