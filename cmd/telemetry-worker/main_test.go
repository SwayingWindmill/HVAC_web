package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/modules/telemetry/pkg/telemetry"
)

type stubPresenceSweeper struct {
	calls atomic.Int32
	fn    func(call int32) (int, error)
}

func (stub *stubPresenceSweeper) SweepDuePresence(_ context.Context, _ time.Time, _ int) (int, error) {
	call := stub.calls.Add(1)
	return stub.fn(call)
}

func TestRunPresenceSweepTicksAndStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var logs bytes.Buffer
	logger := observability.NewJSONLogger(&logs, slog.LevelInfo)
	sweeper := &stubPresenceSweeper{
		fn: func(call int32) (int, error) {
			switch call {
			case 1:
				return 2, nil
			case 2:
				return 0, errors.New("temporary db hiccup")
			default:
				cancel()
				return 0, nil
			}
		},
	}

	runPresenceSweep(ctx, sweeper, 5*time.Millisecond, logger)

	if got := sweeper.calls.Load(); got < 3 {
		t.Fatalf("calls=%d, want >= 3", got)
	}
	output := logs.String()
	if !strings.Contains(output, `"telemetry_presence_swept"`) || !strings.Contains(output, `"device_count":2`) {
		t.Fatalf("missing swept log in %s", output)
	}
	if !strings.Contains(output, `"telemetry_presence_sweep_failed"`) {
		t.Fatalf("missing failure log in %s", output)
	}
}

type telemetryRecordingExporter struct {
	record func(string)
}

func (e *telemetryRecordingExporter) Export(context.Context, []observability.SpanData) error {
	return nil
}

func (e *telemetryRecordingExporter) Shutdown(context.Context) error {
	if e.record != nil {
		e.record("telemetry_shutdown")
	}
	return nil
}

func TestShutdownTelemetryWorkerOrder(t *testing.T) {
	var eventsMu sync.Mutex
	var events []string
	recordEvent := func(ev string) {
		eventsMu.Lock()
		events = append(events, ev)
		eventsMu.Unlock()
	}

	obsRuntime := observability.NewRuntime(observability.RuntimeConfig{
		Service: "telemetry-runtime-test", QueueSize: 8, ExportTimeout: 100 * time.Millisecond,
		Exporter: &telemetryRecordingExporter{record: recordEvent},
	})
	obsRuntime.MarkReady()
	logger := observability.NewJSONLogger(io.Discard, slog.LevelInfo)

	var storesClosed atomic.Bool
	httpEntered := make(chan struct{})
	releaseHTTP := make(chan struct{})
	apiSrv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(httpEntered)
			<-releaseHTTP
			if obsRuntime.Ready() {
				t.Error("expected observabilityRuntime.Ready() == false during shutdown")
			}
			if storesClosed.Load() {
				t.Error("stores closed before HTTP handler completed")
			}
			recordEvent("http_handler_done")
			w.WriteHeader(http.StatusOK)
		}),
	}
	apiLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var httpWG sync.WaitGroup
	httpWG.Add(1)
	go func() {
		defer httpWG.Done()
		_ = apiSrv.Serve(apiLn)
		recordEvent("api_http_goroutine_done")
	}()

	diagSrv := &http.Server{Handler: obsRuntime.DiagnosticsHandler()}
	diagLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpWG.Add(1)
	go func() {
		defer httpWG.Done()
		_ = diagSrv.Serve(diagLn)
		recordEvent("diag_http_goroutine_done")
	}()

	var workersWG sync.WaitGroup
	workerCtx, workerCancel := context.WithCancel(context.Background())
	workerStarted := make(chan struct{})
	runSupervised(workerCtx, &workersWG, obsRuntime, logger, "telemetry.test_worker", func() {
		close(workerStarted)
		<-workerCtx.Done()
		time.Sleep(25 * time.Millisecond)
		if storesClosed.Load() {
			t.Error("stores closed before supervised worker exited")
		}
		recordEvent("worker_done")
	})
	<-workerStarted

	httpResp := make(chan error, 1)
	go func() {
		resp, getErr := http.Get("http://" + apiLn.Addr().String() + "/work")
		if resp != nil {
			_ = resp.Body.Close()
		}
		httpResp <- getErr
	}()
	<-httpEntered

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		shutdownTelemetryWorker(telemetryWorkerShutdown{
			Observability:    obsRuntime,
			Logger:           logger,
			Diagnostics:      diagSrv,
			Server:           apiSrv,
			HTTPWG:           &httpWG,
			CancelWorkers:    workerCancel,
			WorkersWG:        &workersWG,
			HTTPTimeout:      2 * time.Second,
			WorkerTimeout:    2 * time.Second,
			TelemetryTimeout: time.Second,
			CloseHistory: func() {
				storesClosed.Store(true)
				recordEvent("close_history")
			},
			CloseLatestCache: func() {
				recordEvent("close_latest_cache")
			},
			CloseRateLimiter: func() {
				recordEvent("close_rate_limiter")
			},
			CloseStore: func() {
				recordEvent("close_store")
			},
		})
	}()

	deadline := time.Now().Add(time.Second)
	for obsRuntime.Ready() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if obsRuntime.Ready() {
		t.Fatal("observabilityRuntime stayed ready after shutdown started")
	}
	if workerCtx.Err() != nil {
		t.Fatal("worker context cancelled before HTTP server finished shutting down")
	}

	close(releaseHTTP)
	if err := <-httpResp; err != nil {
		t.Fatalf("in-flight HTTP request failed during graceful shutdown: %v", err)
	}
	select {
	case <-shutdownDone:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdownTelemetryWorker did not complete")
	}

	eventsMu.Lock()
	got := slices.Clone(events)
	eventsMu.Unlock()
	httpHandlerIdx := slices.Index(got, "http_handler_done")
	apiGoroutineIdx := slices.Index(got, "api_http_goroutine_done")
	diagGoroutineIdx := slices.Index(got, "diag_http_goroutine_done")
	workerIdx := slices.Index(got, "worker_done")
	telemetryIdx := slices.Index(got, "telemetry_shutdown")
	closeHistoryIdx := slices.Index(got, "close_history")
	if httpHandlerIdx < 0 || apiGoroutineIdx < 0 || diagGoroutineIdx < 0 || workerIdx < 0 || telemetryIdx < 0 || closeHistoryIdx < 0 {
		t.Fatalf("missing expected shutdown events: %v", got)
	}
	if httpHandlerIdx >= workerIdx || apiGoroutineIdx >= workerIdx || diagGoroutineIdx >= workerIdx {
		t.Fatalf("expected HTTP drain and server goroutines to finish before worker_done, got %v", got)
	}
	if telemetryIdx < workerIdx || telemetryIdx > closeHistoryIdx {
		t.Fatalf("expected telemetry_shutdown after worker_done and before store closes, got %v", got)
	}
	tail := got[closeHistoryIdx:]
	wantTail := []string{"close_history", "close_latest_cache", "close_rate_limiter", "close_store"}
	if !slices.Equal(tail, wantTail) {
		t.Fatalf("store close tail=%v, want %v (full=%v)", tail, wantTail, got)
	}
}

func TestShutdownTelemetryWorkerBoundedWorkerWait(t *testing.T) {
	obsRuntime := observability.NewRuntime(observability.RuntimeConfig{
		Service: "telemetry-runtime-timeout-test", QueueSize: 4, ExportTimeout: 100 * time.Millisecond,
	})
	var logs bytes.Buffer
	logger := observability.NewJSONLogger(&logs, slog.LevelInfo)

	var workersWG sync.WaitGroup
	workerCtx, workerCancel := context.WithCancel(context.Background())
	releaseStuckWorker := make(chan struct{})
	t.Cleanup(func() { close(releaseStuckWorker) })
	started := make(chan struct{})
	runSupervised(workerCtx, &workersWG, obsRuntime, logger, "telemetry.stuck_worker", func() {
		close(started)
		<-releaseStuckWorker
	})
	<-started

	var closedStore atomic.Bool
	start := time.Now()
	shutdownTelemetryWorker(telemetryWorkerShutdown{
		Observability:    obsRuntime,
		Logger:           logger,
		CancelWorkers:    workerCancel,
		WorkersWG:        &workersWG,
		HTTPTimeout:      100 * time.Millisecond,
		WorkerTimeout:    40 * time.Millisecond,
		TelemetryTimeout: 100 * time.Millisecond,
		CloseStore:       func() { closedStore.Store(true) },
	})
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Fatalf("shutdownTelemetryWorker blocked past bounded worker wait: %s", elapsed)
	}
	if !closedStore.Load() {
		t.Fatal("expected CloseStore to run after bounded worker wait timeout")
	}
	if !strings.Contains(logs.String(), `"TELEMETRY_WORKER_SHUTDOWN_TIMEOUT"`) {
		t.Fatalf("expected timeout warning log, got: %s", logs.String())
	}
}

func TestShutdownTelemetryWorkerDrainRelaysAndAnalyticsOrder(t *testing.T) {
	var eventsMu sync.Mutex
	var events []string
	record := func(ev string) {
		eventsMu.Lock()
		events = append(events, ev)
		eventsMu.Unlock()
	}

	obsRuntime := observability.NewRuntime(observability.RuntimeConfig{
		Service: "telemetry-runtime-4phase-test", QueueSize: 4, ExportTimeout: 100 * time.Millisecond,
		Exporter: &telemetryRecordingExporter{record: record},
	})
	obsRuntime.MarkReady()
	logger := observability.NewJSONLogger(io.Discard, slog.LevelInfo)

	var workersWG, analyticsWG sync.WaitGroup
	workerCtx, workerCancel := context.WithCancel(context.Background())
	analyticsCtx, analyticsCancel := context.WithCancel(context.Background())

	workerStarted := make(chan struct{})
	runSupervised(workerCtx, &workersWG, obsRuntime, logger, "telemetry.poll", func() {
		close(workerStarted)
		<-workerCtx.Done()
		record("poll_stopped")
	})
	analyticsStarted := make(chan struct{})
	runSupervised(analyticsCtx, &analyticsWG, obsRuntime, logger, "telemetry.analytics", func() {
		close(analyticsStarted)
		<-analyticsCtx.Done()
		record("analytics_stopped")
	})
	<-workerStarted
	<-analyticsStarted

	shutdownTelemetryWorker(telemetryWorkerShutdown{
		Observability: obsRuntime,
		Logger:        logger,
		CancelWorkers: workerCancel,
		WorkersWG:     &workersWG,
		DrainRelays: func(drainCtx context.Context) {
			if drainCtx.Err() != nil {
				t.Error("expected active drainCtx")
			}
			if _, hasDeadline := drainCtx.Deadline(); !hasDeadline {
				t.Error("expected bounded deadline on drainCtx")
			}
			if analyticsCtx.Err() != nil {
				t.Error("analytics projector cancelled before relay drain completed")
			}
			record("drain_relays")
		},
		CancelAnalytics:  analyticsCancel,
		AnalyticsWG:      &analyticsWG,
		WorkerTimeout:    time.Second,
		DrainTimeout:     time.Second,
		TelemetryTimeout: time.Second,
		CloseHistory:     func() { record("close_history") },
		CloseStore:       func() { record("close_store") },
	})

	eventsMu.Lock()
	got := slices.Clone(events)
	eventsMu.Unlock()
	want := []string{"poll_stopped", "drain_relays", "analytics_stopped", "telemetry_shutdown", "close_history", "close_store"}
	if !slices.Equal(got, want) {
		t.Fatalf("shutdown event order=%v, want %v", got, want)
	}
}

type stubBatchDrainer struct {
	name   string
	record func(string)
	err    error
}

func (s *stubBatchDrainer) RelayOnce(ctx context.Context, limit int) (int, error) {
	if ctx.Err() != nil || limit != 64 {
		return 0, errors.New("unexpected drain arguments")
	}
	s.record(s.name)
	return 1, s.err
}

type stubHistoryDrainer struct {
	record    func(string)
	remaining int
	err       error
}

func (s *stubHistoryDrainer) RelayOnce(ctx context.Context) (int, error) {
	if ctx.Err() != nil {
		return 0, errors.New("unexpected drain context")
	}
	s.record("history")
	if s.err != nil {
		return 0, s.err
	}
	if s.remaining > 0 {
		s.remaining--
		return 1, nil
	}
	return 0, nil
}

func TestDrainTelemetryRelaysResilientAndOrdered(t *testing.T) {
	var called []string
	var logs bytes.Buffer
	logger := observability.NewJSONLogger(&logs, slog.LevelInfo)
	record := func(name string) { called = append(called, name) }

	drainTelemetryRelays(t.Context(), telemetryRelayDrainTargets{
		LatestCache: &stubBatchDrainer{name: "latest_cache", record: record, err: errors.New("redis glitch")},
		Realtime:    &stubBatchDrainer{name: "realtime", record: record},
		Alarm:       &stubBatchDrainer{name: "alarm", record: record},
		History:     &stubHistoryDrainer{record: record, remaining: 1},
	}, logger)

	want := []string{"latest_cache", "realtime", "alarm", "history", "history"}
	if !slices.Equal(called, want) {
		t.Fatalf("drain called=%v, want %v", called, want)
	}
	if !strings.Contains(logs.String(), `"TELEMETRY_LATEST_CACHE_DRAIN_FAILED"`) {
		t.Fatalf("expected drain failure warning in logs: %s", logs.String())
	}
}

type stubClickHouseRetentionExecutor struct {
	calls atomic.Int32
	onRun func()
}

func (s *stubClickHouseRetentionExecutor) Prune(ctx context.Context, _ time.Time) (telemetry.ClickHouseRetentionResult, error) {
	if _, ok := ctx.Deadline(); !ok {
		return telemetry.ClickHouseRetentionResult{}, errors.New("missing prune timeout")
	}
	s.calls.Add(1)
	if s.onRun != nil {
		s.onRun()
	}
	return telemetry.ClickHouseRetentionResult{SubmittedTables: []string{"telemetry_history.observations"}}, nil
}

func TestRunClickHouseRetentionAndLegalHolds(t *testing.T) {
	holds, err := parseClickHouseLegalHolds(" 018f2d00-0000-7000-8000-000000000001 , 018f2d00-0000-7000-8000-000000000002:018f2e00-1000-7000-8000-000000000009 ")
	if err != nil {
		t.Fatal(err)
	}
	if len(holds) != 2 || holds[0].SiteID != "" || holds[1].SiteID != "018f2e00-1000-7000-8000-000000000009" {
		t.Fatalf("unexpected parsed legal holds: %#v", holds)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var logs bytes.Buffer
	logger := observability.NewJSONLogger(&logs, slog.LevelInfo)
	exec := &stubClickHouseRetentionExecutor{onRun: cancel}
	runClickHouseRetention(ctx, exec, time.Minute, logger)

	if exec.calls.Load() != 1 {
		t.Fatalf("calls=%d, want 1", exec.calls.Load())
	}
	if !strings.Contains(logs.String(), `"telemetry_clickhouse_retention_submitted"`) {
		t.Fatalf("expected retention submission log, got %s", logs.String())
	}
}

func TestParseTelemetryPostgresPoolConfig(t *testing.T) {
	lookupFrom := func(values map[string]string) func(string) string {
		return func(key string) string {
			return values[key]
		}
	}

	t.Run("defaults when env vars unset", func(t *testing.T) {
		cfg, err := parseTelemetryPostgresPoolConfig(lookupFrom(map[string]string{}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg != telemetry.DefaultPostgresPoolConfig() {
			t.Fatalf("got=%+v, want=%+v", cfg, telemetry.DefaultPostgresPoolConfig())
		}
	})

	t.Run("overrides all five env vars", func(t *testing.T) {
		cfg, err := parseTelemetryPostgresPoolConfig(lookupFrom(map[string]string{
			"TELEMETRY_POSTGRES_MAX_CONNS":           "40",
			"TELEMETRY_POSTGRES_MIN_CONNS":           "8",
			"TELEMETRY_POSTGRES_MAX_CONN_LIFETIME":   "45m",
			"TELEMETRY_POSTGRES_MAX_CONN_IDLE_TIME":  "10m",
			"TELEMETRY_POSTGRES_HEALTH_CHECK_PERIOD": "15s",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := telemetry.PostgresPoolConfig{
			MaxConns:          40,
			MinConns:          8,
			MaxConnLifetime:   45 * time.Minute,
			MaxConnIdleTime:   10 * time.Minute,
			HealthCheckPeriod: 15 * time.Second,
		}
		if cfg != want {
			t.Fatalf("got=%+v, want=%+v", cfg, want)
		}
	})

	t.Run("small max conns clamps default min conns", func(t *testing.T) {
		cfg, err := parseTelemetryPostgresPoolConfig(lookupFrom(map[string]string{
			"TELEMETRY_POSTGRES_MAX_CONNS": "2",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.MaxConns != 2 || cfg.MinConns != 2 {
			t.Fatalf("MaxConns=%d MinConns=%d, want 2/2", cfg.MaxConns, cfg.MinConns)
		}
	})

	t.Run("rejects invalid values", func(t *testing.T) {
		invalidCases := []struct {
			name   string
			values map[string]string
		}{
			{name: "zero max conns", values: map[string]string{"TELEMETRY_POSTGRES_MAX_CONNS": "0"}},
			{name: "negative max conns", values: map[string]string{"TELEMETRY_POSTGRES_MAX_CONNS": "-1"}},
			{name: "non-numeric max conns", values: map[string]string{"TELEMETRY_POSTGRES_MAX_CONNS": "abc"}},
			{name: "overflow max conns", values: map[string]string{"TELEMETRY_POSTGRES_MAX_CONNS": "999999999999"}},
			{name: "zero min conns", values: map[string]string{"TELEMETRY_POSTGRES_MIN_CONNS": "0"}},
			{name: "min exceeds max", values: map[string]string{"TELEMETRY_POSTGRES_MAX_CONNS": "4", "TELEMETRY_POSTGRES_MIN_CONNS": "6"}},
			{name: "zero max conn lifetime", values: map[string]string{"TELEMETRY_POSTGRES_MAX_CONN_LIFETIME": "0s"}},
			{name: "negative max conn idle time", values: map[string]string{"TELEMETRY_POSTGRES_MAX_CONN_IDLE_TIME": "-5s"}},
			{name: "invalid health check period", values: map[string]string{"TELEMETRY_POSTGRES_HEALTH_CHECK_PERIOD": "not-a-duration"}},
		}
		for _, tc := range invalidCases {
			t.Run(tc.name, func(t *testing.T) {
				if _, err := parseTelemetryPostgresPoolConfig(lookupFrom(tc.values)); err == nil {
					t.Fatalf("expected error for %v", tc.values)
				}
			})
		}
	})
}

func TestLoadClickHouseRetentionPrunerArchiveEnvWiring(t *testing.T) {
	lookupFrom := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}

	t.Run("disabled when retention user unset", func(t *testing.T) {
		pruner, _, cancel, err := loadClickHouseRetentionPrunerFromEnv(lookupFrom(map[string]string{}))
		if err != nil || pruner != nil || cancel != nil {
			t.Fatalf("expected disabled pruner, got pruner=%v cancel=%v err=%v", pruner, cancel, err)
		}
	})

	t.Run("rejects archive required without archive dir", func(t *testing.T) {
		_, _, _, err := loadClickHouseRetentionPrunerFromEnv(lookupFrom(map[string]string{
			"TELEMETRY_RETENTION_CLICKHOUSE_USER":  "telemetry_retention_writer",
			"TELEMETRY_CLICKHOUSE_HTTP_URL":        "http://127.0.0.1:8123",
			"TELEMETRY_RETENTION_ARCHIVE_REQUIRED": "true",
		}))
		if err == nil || !strings.Contains(err.Error(), "TELEMETRY_COLD_ARCHIVE_DIR") {
			t.Fatalf("expected missing archive dir error, got %v", err)
		}
	})

	t.Run("rejects invalid archive required bool", func(t *testing.T) {
		_, _, _, err := loadClickHouseRetentionPrunerFromEnv(lookupFrom(map[string]string{
			"TELEMETRY_RETENTION_CLICKHOUSE_USER":  "telemetry_retention_writer",
			"TELEMETRY_CLICKHOUSE_HTTP_URL":        "http://127.0.0.1:8123",
			"TELEMETRY_RETENTION_ARCHIVE_REQUIRED": "maybe",
		}))
		if err == nil || !strings.Contains(err.Error(), "TELEMETRY_RETENTION_ARCHIVE_REQUIRED") {
			t.Fatalf("expected bool parse error, got %v", err)
		}
	})

	t.Run("rejects unsafe archive dir", func(t *testing.T) {
		_, _, _, err := loadClickHouseRetentionPrunerFromEnv(lookupFrom(map[string]string{
			"TELEMETRY_RETENTION_CLICKHOUSE_USER":  "telemetry_retention_writer",
			"TELEMETRY_CLICKHOUSE_HTTP_URL":        "http://127.0.0.1:8123",
			"TELEMETRY_COLD_ARCHIVE_DIR":           "../escape",
			"TELEMETRY_RETENTION_ARCHIVE_REQUIRED": "true",
		}))
		if err == nil {
			t.Fatal("expected unsafe archive dir error")
		}
	})

	t.Run("wires cold archive exporter and runs pre-prune export", func(t *testing.T) {
		var ops []string
		var mu sync.Mutex
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			body, _ := io.ReadAll(request.Body)
			sql := string(body)
			switch {
			case strings.Contains(sql, "FROM system.parts"):
				writer.WriteHeader(http.StatusOK)
				return
			case strings.Contains(sql, "FROM system.mutations"):
				_, _ = io.WriteString(writer, "0\n")
				return
			case strings.HasPrefix(sql, "SELECT 1 FROM "):
				_, _ = io.WriteString(writer, "1\n")
				return
			}
			mu.Lock()
			if strings.HasPrefix(sql, "SELECT ") {
				ops = append(ops, "SELECT")
			} else if strings.HasPrefix(sql, "ALTER TABLE ") {
				ops = append(ops, "DELETE")
			}
			mu.Unlock()
			writer.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		archiveDir := t.TempDir()
		pruner, _, cancel, err := loadClickHouseRetentionPrunerFromEnv(lookupFrom(map[string]string{
			"TELEMETRY_RETENTION_CLICKHOUSE_USER":  "telemetry_retention_writer",
			"TELEMETRY_CLICKHOUSE_HTTP_URL":        server.URL,
			"TELEMETRY_COLD_ARCHIVE_DIR":           archiveDir,
			"TELEMETRY_RETENTION_ARCHIVE_REQUIRED": "true",
		}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer cancel()

		res, err := pruner.Prune(t.Context(), time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC))
		if err != nil {
			t.Fatalf("Prune failed: %v", err)
		}
		if len(res.ArchivedTables) != 3 || len(res.SubmittedTables) != 9 {
			t.Fatalf("unexpected result: %+v", res)
		}
		entries, err := os.ReadDir(archiveDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 6 {
			t.Fatalf("expected 6 archive files, got %d", len(entries))
		}
		mu.Lock()
		defer mu.Unlock()
		if len(ops) != 12 || ops[0] != "SELECT" || ops[1] != "SELECT" || ops[2] != "SELECT" {
			t.Fatalf("unexpected query order: %v", ops)
		}
	})
}

func TestParseHistoryProjectionSettingsPipelineDepth(t *testing.T) {
	lookupFrom := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}

	defaults, err := parseHistoryProjectionSettings(lookupFrom(map[string]string{}))
	if err != nil {
		t.Fatalf("defaults error: %v", err)
	}
	if defaults.PipelineDepth != 2 {
		t.Fatalf("default PipelineDepth=%d want 2", defaults.PipelineDepth)
	}

	for _, valid := range []string{"1", "2", "3", "4"} {
		cfg, err := parseHistoryProjectionSettings(lookupFrom(map[string]string{
			"TELEMETRY_HISTORY_PIPELINE_DEPTH": valid,
		}))
		if err != nil {
			t.Fatalf("valid depth %s error: %v", valid, err)
		}
		if cfg.PipelineDepth < 1 || cfg.PipelineDepth > 4 {
			t.Fatalf("unexpected depth %d", cfg.PipelineDepth)
		}
	}

	for _, invalid := range []string{"0", "-1", "5", "abc"} {
		if _, err := parseHistoryProjectionSettings(lookupFrom(map[string]string{
			"TELEMETRY_HISTORY_PIPELINE_DEPTH": invalid,
		})); err == nil {
			t.Fatalf("expected error for TELEMETRY_HISTORY_PIPELINE_DEPTH=%q", invalid)
		}
	}

	batchCfg, err := parseHistoryProjectionSettings(lookupFrom(map[string]string{
		"TELEMETRY_HISTORY_BATCH_SIZE": "16384",
	}))
	if err != nil || batchCfg.BatchSize != 16384 {
		t.Fatalf("BatchSize=%d err=%v, want 16384", batchCfg.BatchSize, err)
	}
	maxBatchCfg, err := parseHistoryProjectionSettings(lookupFrom(map[string]string{
		"TELEMETRY_HISTORY_BATCH_SIZE": "32768",
	}))
	if err != nil || maxBatchCfg.BatchSize != 32768 {
		t.Fatalf("max BatchSize=%d err=%v, want 32768", maxBatchCfg.BatchSize, err)
	}
	if _, err := parseHistoryProjectionSettings(lookupFrom(map[string]string{
		"TELEMETRY_HISTORY_BATCH_SIZE": "32769",
	})); err == nil {
		t.Fatal("expected error for TELEMETRY_HISTORY_BATCH_SIZE=32769")
	}
}

type stubProbeOffsetSweeper struct {
	calls atomic.Int32
	done  chan struct{}
}

func (s *stubProbeOffsetSweeper) SweepProbeOffsets(context.Context, time.Time) (int, error) {
	if s.calls.Add(1) == 1 && s.done != nil {
		close(s.done)
	}
	return 3, nil
}

func TestClonedHTTPTransportAndProbeSweepDebugLogging(t *testing.T) {
	transport := newClonedTelemetryHTTPTransport(&tls.Config{MinVersion: tls.VersionTLS13}, true, false, 12*time.Second)
	if transport.MaxConnsPerHost != 16 || transport.MaxIdleConns != 64 || transport.MaxIdleConnsPerHost != 16 ||
		transport.IdleConnTimeout != 90*time.Second || transport.ResponseHeaderTimeout != 12*time.Second ||
		transport.DialContext == nil || !transport.DisableCompression || transport.TLSClientConfig == nil {
		t.Fatalf("unexpected cloned transport config: %#v", transport)
	}

	var infoBuf syncLogBuffer
	infoCtx, cancelInfo := context.WithCancel(t.Context())
	doneInfo := make(chan struct{})
	exitedInfo := make(chan struct{})
	go func() {
		defer close(exitedInfo)
		runProbeOffsetSweepCoordinated(infoCtx, &stubProbeOffsetSweeper{done: doneInfo}, 10*time.Millisecond, nil, observability.NewJSONLogger(&infoBuf, slog.LevelInfo))
	}()
	<-doneInfo
	cancelInfo()
	<-exitedInfo
	if strings.Contains(infoBuf.String(), "telemetry_probe_offsets_estimated") {
		t.Fatalf("expected telemetry_probe_offsets_estimated suppressed at Info level, got: %s", infoBuf.String())
	}

	var debugBuf syncLogBuffer
	debugCtx, cancelDebug := context.WithCancel(t.Context())
	doneDebug := make(chan struct{})
	exitedDebug := make(chan struct{})
	go func() {
		defer close(exitedDebug)
		runProbeOffsetSweepCoordinated(debugCtx, &stubProbeOffsetSweeper{done: doneDebug}, 10*time.Millisecond, nil, observability.NewJSONLogger(&debugBuf, slog.LevelDebug))
	}()
	<-doneDebug
	cancelDebug()
	<-exitedDebug
	if !strings.Contains(debugBuf.String(), "telemetry_probe_offsets_estimated") {
		t.Fatalf("expected telemetry_probe_offsets_estimated at Debug level, got: %s", debugBuf.String())
	}
}

type syncLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncLogBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncLogBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestEnvHelpersReturnErrorsInsteadOfExiting(t *testing.T) {
	t.Setenv("TEST_TELEMETRY_REQUIRED_EMPTY", "   ")
	if _, err := requiredEnv("TEST_TELEMETRY_REQUIRED_EMPTY"); err == nil {
		t.Fatal("expected error for blank requiredEnv")
	}

	t.Setenv("TEST_TELEMETRY_DURATION_BAD", "invalid")
	if _, err := durationEnv("TEST_TELEMETRY_DURATION_BAD", time.Second, time.Second, time.Minute); err == nil {
		t.Fatal("expected error for invalid durationEnv")
	}

	t.Setenv("TEST_TELEMETRY_INT_BAD", "999")
	if _, err := integerEnv("TEST_TELEMETRY_INT_BAD", 5, 1, 100); err == nil {
		t.Fatal("expected error for out-of-range integerEnv")
	}
}

func TestOpenTelemetryHistorySink(t *testing.T) {
	lookupFrom := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}

	t.Run("selects Native TCP PrepareBatch sink when TELEMETRY_CLICKHOUSE_NATIVE_ADDR is set", func(t *testing.T) {
		sink, err := openTelemetryHistorySink(lookupFrom(map[string]string{
			"TELEMETRY_CLICKHOUSE_NATIVE_ADDR": "127.0.0.1:9000",
			"TELEMETRY_CLICKHOUSE_HTTP_URL":    "http://127.0.0.1:8123",
		}))
		if err != nil {
			t.Fatalf("unexpected error opening native sink: %v", err)
		}
		defer func() { _ = sink.Close() }()
		if _, ok := sink.(*telemetry.ClickHouseNativeHistorySink); !ok {
			t.Fatalf("expected *telemetry.ClickHouseNativeHistorySink, got %T", sink)
		}
	})

	t.Run("falls back to HTTP JSONEachRow sink when native addr is unset", func(t *testing.T) {
		sink, err := openTelemetryHistorySink(lookupFrom(map[string]string{
			"TELEMETRY_CLICKHOUSE_HTTP_URL": "http://127.0.0.1:8123",
		}))
		if err != nil {
			t.Fatalf("unexpected error opening HTTP sink: %v", err)
		}
		defer func() { _ = sink.Close() }()
		if _, ok := sink.(*telemetry.ClickHouseHistorySink); !ok {
			t.Fatalf("expected *telemetry.ClickHouseHistorySink, got %T", sink)
		}
	})

	t.Run("rejects invalid native addr, invalid CA file, and missing endpoints", func(t *testing.T) {
		if _, err := openTelemetryHistorySink(lookupFrom(map[string]string{
			"TELEMETRY_CLICKHOUSE_NATIVE_ADDR": "clickhouse-without-port",
		})); err == nil {
			t.Fatal("expected error for invalid TELEMETRY_CLICKHOUSE_NATIVE_ADDR")
		}

		if _, err := openTelemetryHistorySink(lookupFrom(map[string]string{
			"TELEMETRY_CLICKHOUSE_NATIVE_ADDR": "127.0.0.1:9000",
			"TELEMETRY_CLICKHOUSE_CA":          t.TempDir() + "/missing-ca.pem",
		})); err == nil {
			t.Fatal("expected error for missing TELEMETRY_CLICKHOUSE_CA file")
		}

		badCAPath := t.TempDir() + "/bad-ca.pem"
		if err := os.WriteFile(badCAPath, []byte("not-a-pem"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := openTelemetryHistorySink(lookupFrom(map[string]string{
			"TELEMETRY_CLICKHOUSE_NATIVE_ADDR": "127.0.0.1:9000",
			"TELEMETRY_CLICKHOUSE_CA":          badCAPath,
		})); err == nil {
			t.Fatal("expected error for invalid PEM content in TELEMETRY_CLICKHOUSE_CA")
		}

		if _, err := openTelemetryHistorySink(lookupFrom(map[string]string{})); err == nil {
			t.Fatal("expected error when both TELEMETRY_CLICKHOUSE_NATIVE_ADDR and TELEMETRY_CLICKHOUSE_HTTP_URL are empty")
		}
	})
}

func TestDeriveEdgeAlarmEventsURLAndShutdownDrainTarget(t *testing.T) {
	got, err := telemetry.DeriveEdgeAlarmEventsURL("https://alarm-service:8448/internal/v1/alarm/evaluations/telemetry", "")
	if err != nil || got != "https://alarm-service:8448/internal/v1/alarms/edge-events" {
		t.Fatalf("DeriveEdgeAlarmEventsURL() = (%q, %v)", got, err)
	}
	got, err = telemetry.DeriveEdgeAlarmEventsURL("https://alarm-service:8448/internal/v1/alarm/evaluations/telemetry", "https://alarm-owner:8448/internal/v1/alarms/edge-events")
	if err != nil || got != "https://alarm-owner:8448/internal/v1/alarms/edge-events" {
		t.Fatalf("DeriveEdgeAlarmEventsURL(override) = (%q, %v)", got, err)
	}
	if _, err := telemetry.DeriveEdgeAlarmEventsURL("http://alarm-service:8448/x", ""); err == nil {
		t.Fatal("expected non-https alarmEvalURL to fail")
	}
	var called []string
	drainTelemetryRelays(t.Context(), telemetryRelayDrainTargets{
		EdgeAlarm: &stubBatchDrainer{name: "edge_alarm", record: func(n string) { called = append(called, n) }},
	}, nil)
	if len(called) != 1 || called[0] != "edge_alarm" {
		t.Fatalf("expected EdgeAlarm relay drain on shutdown, got %v", called)
	}
}

func TestDrainAnalyticsProjectionOnceStopsAtEmptyAndBoundsBatches(t *testing.T) {
	if defaultAnalyticsProjectorPollInterval != 3*time.Second {
		t.Fatalf("defaultAnalyticsProjectorPollInterval=%s, want 3s", defaultAnalyticsProjectorPollInterval)
	}
	calls := 0
	total, err := drainAnalyticsProjectionOnce(t.Context(), func(context.Context) (int, error) {
		calls++
		if calls <= 2 {
			return 5, nil
		}
		return 0, nil
	})
	if err != nil || total != 10 || calls != 3 {
		t.Fatalf("drain empty: total=%d calls=%d err=%v, want 10/3/nil", total, calls, err)
	}

	boundedCalls := 0
	boundedTotal, err := drainAnalyticsProjectionOnce(t.Context(), func(context.Context) (int, error) {
		boundedCalls++
		return 2, nil
	})
	if err != nil || boundedCalls != maxAnalyticsProjectionDrainBatches || boundedTotal != 2*maxAnalyticsProjectionDrainBatches {
		t.Fatalf("drain bounded: total=%d calls=%d err=%v", boundedTotal, boundedCalls, err)
	}
}

func TestParseTelemetryWorkerCLIAndRunHourlyV2Backfill(t *testing.T) {
	if cli, err := parseTelemetryWorkerCLI(nil, io.Discard); err != nil || cli.enabled {
		t.Fatalf("empty args = (%+v, %v), want disabled", cli, err)
	}
	if _, err := parseTelemetryWorkerCLI([]string{"--tenant-id=018f4d00-0000-7000-8000-000000000001"}, io.Discard); err == nil {
		t.Fatal("expected error when --tenant-id passed without --backfill-hourly-v2-since")
	}
	if _, err := parseTelemetryWorkerCLI([]string{
		"--backfill-hourly-v2-since=2026-11-22T00:00:00Z",
		"--backfill-hourly-v2-until=2026-11-22T00:00:00Z",
	}, io.Discard); err == nil {
		t.Fatal("expected error when until <= since")
	}
	subCLI, err := parseTelemetryWorkerCLI([]string{
		"backfill-hourly-v2",
		"--since=2026-11-21T00:00:00Z",
		"--until=2026-11-21T02:00:00Z",
		"--tenant-id=018f4d00-0000-7000-8000-000000000001",
		"--site-id=018f4e00-1000-7000-8000-000000000001",
	}, io.Discard)
	if err != nil || !subCLI.enabled || subCLI.tenantID == "" || subCLI.siteID == "" {
		t.Fatalf("subcommand CLI = (%+v, %v)", subCLI, err)
	}

	const (
		tenantID = "018f4d00-0000-7000-8000-000000000001"
		siteID   = "018f4e00-1000-7000-8000-000000000001"
		pointID  = "018f4e00-2100-7000-8000-000000000001"
	)
	var insertedPayloads []string
	chServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		body, _ := io.ReadAll(r.Body)
		sql := string(body)
		switch {
		case strings.Contains(query, "INSERT INTO analytics.energy_hourly_facts_v2 FORMAT JSONEachRow"):
			insertedPayloads = append(insertedPayloads, sql)
			w.WriteHeader(http.StatusNoContent)
		case strings.Contains(sql, "FROM analytics.energy_hourly_facts_v2 AS f"):
			// Return nothing (clean backfill target) for both bucket discovery and existing state lookup.
		case strings.Contains(sql, "AS fact_version") && strings.Contains(sql, "has({point_hours:Array(String)}"):
			_, _ = io.WriteString(w,
				`{"fact_id":"f1","tenant_id":"`+tenantID+`","site_id":"`+siteID+`","meter_id":"m1","meter_binding_id":"b1","topology_version_id":"tv1","binding_version":"1","energy_type_id":"et1","energy_type":"electricity","meter_role":"PRIMARY","direction":"IMPORT","accounting_scope":"SITE","load_device_id":null,"device_id":"d1","point_id":"`+pointID+`","sensor_id":null,"telemetry_key":"site.energy","point_revision":"1","unit":"kWh","counter_decrease_mode":"RESET_TO_ZERO","counter_rollover_modulus":null,"period_start":"2026-11-21T01:00:00.000Z","period_end":"2026-11-21T01:15:00.000Z","energy_kwh":10,"transition_type":"INCREASE","quality":"VALID","quality_reasons":[],"source_previous_observation_id":"o0","source_current_observation_id":"o1","source_event_id":"e1","source_partition":"p0","source_offset":"1","dataset_revision":"1","data_watermark":"2026-11-21T01:15:00.000Z","projected_at":"2026-11-21T01:15:01.000Z","fact_revision":"0"}`+"\n"+
					`{"fact_id":"f2","tenant_id":"`+tenantID+`","site_id":"`+siteID+`","meter_id":"m1","meter_binding_id":"b1","topology_version_id":"tv1","binding_version":"1","energy_type_id":"et1","energy_type":"electricity","meter_role":"PRIMARY","direction":"IMPORT","accounting_scope":"SITE","load_device_id":null,"device_id":"d1","point_id":"`+pointID+`","sensor_id":null,"telemetry_key":"site.energy","point_revision":"1","unit":"kWh","counter_decrease_mode":"RESET_TO_ZERO","counter_rollover_modulus":null,"period_start":"2026-11-21T01:15:00.000Z","period_end":"2026-11-21T01:30:00.000Z","energy_kwh":12,"transition_type":"INCREASE","quality":"VALID","quality_reasons":[],"source_previous_observation_id":"o1","source_current_observation_id":"o2","source_event_id":"e2","source_partition":"p0","source_offset":"2","dataset_revision":"2","data_watermark":"2026-11-21T01:30:00.000Z","projected_at":"2026-11-21T01:30:01.000Z","fact_revision":"0"}`+"\n")
		case strings.Contains(sql, "toStartOfHour(addMilliseconds(v.period_end, -1))"):
			_, _ = io.WriteString(w, `{"tenant_id":"`+tenantID+`","site_id":"`+siteID+`","point_id":"`+pointID+`","hour_utc":"2026-11-21T01:00:00Z"}`+"\n")
		case strings.Contains(sql, "FROM analytics.tariff_price_windows AS w FINAL"):
			_, _ = io.WriteString(w, `{"window_start":"2026-11-21T01:00:00.000Z","window_end":"2026-11-21T02:00:00.000Z","period_code":"PEAK","energy_rate_per_kwh":1.25,"tariff_version_id":"tv-1"}`+"\n")
		case strings.Contains(sql, "dateDiff('millisecond', v.period_start, v.period_end) > 3600000"):
			// No cross-gap intervals
		default:
			t.Errorf("unexpected ClickHouse request: query=%q body=%s", query, sql)
		}
	}))
	defer chServer.Close()

	var stdout, stderr bytes.Buffer
	code := runTelemetryWorkerArgs(
		[]string{
			"--backfill-hourly-v2-since=2026-11-21T00:00:00Z",
			"--backfill-hourly-v2-until=2026-11-21T02:00:00Z",
			"--tenant-id=" + tenantID,
			"--site-id=" + siteID,
		},
		func(key string) string {
			switch key {
			case "TELEMETRY_CLICKHOUSE_HTTP_URL":
				return chServer.URL
			case "ANALYTICS_CLICKHOUSE_READER_USERNAME":
				return "analytics_projector_reader"
			case "ANALYTICS_CLICKHOUSE_WRITER_USERNAME":
				return "analytics_projector_writer"
			default:
				return ""
			}
		},
		&stdout,
		&stderr,
	)
	if code != 0 {
		t.Fatalf("backfill exited %d, stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	if len(insertedPayloads) != 1 {
		t.Fatalf("expected 1 v2 insert payload, got %d (%v)", len(insertedPayloads), insertedPayloads)
	}
	payload := insertedPayloads[0]
	if !strings.Contains(payload, `"energy_kwh":"22.000000"`) ||
		!strings.Contains(payload, `"cost_cny":"27.500000"`) ||
		!strings.Contains(payload, `"interval_count":2`) ||
		!strings.Contains(payload, `"tariff_period":"PEAK"`) {
		t.Fatalf("unexpected backfill insert payload: %s", payload)
	}
	if !strings.Contains(stdout.String(), `"analytics_hourly_v2_backfill_completed"`) {
		t.Fatalf("expected completion log, got: %s", stdout.String())
	}
}
