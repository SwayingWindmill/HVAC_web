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

func TestShutdownTelemetryWorkerOrder(t *testing.T) {
	obsRuntime := observability.NewRuntime(observability.RuntimeConfig{
		Service: "telemetry-runtime-test", QueueSize: 8, ExportTimeout: 100 * time.Millisecond,
	})
	obsRuntime.MarkReady()
	logger := observability.NewJSONLogger(&bytes.Buffer{}, slog.LevelInfo)

	var eventsMu sync.Mutex
	var events []string
	recordEvent := func(ev string) {
		eventsMu.Lock()
		events = append(events, ev)
		eventsMu.Unlock()
	}

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
	go func() { _ = apiSrv.Serve(apiLn) }()

	diagSrv := &http.Server{Handler: obsRuntime.DiagnosticsHandler()}
	diagLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = diagSrv.Serve(diagLn) }()

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
	want := []string{
		"http_handler_done",
		"worker_done",
		"close_history",
		"close_latest_cache",
		"close_rate_limiter",
		"close_store",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("shutdown event order=%v, want %v", got, want)
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
	obsRuntime := observability.NewRuntime(observability.RuntimeConfig{
		Service: "telemetry-runtime-4phase-test", QueueSize: 4, ExportTimeout: 100 * time.Millisecond,
	})
	obsRuntime.MarkReady()
	logger := observability.NewJSONLogger(&bytes.Buffer{}, slog.LevelInfo)

	var eventsMu sync.Mutex
	var events []string
	record := func(ev string) {
		eventsMu.Lock()
		events = append(events, ev)
		eventsMu.Unlock()
	}

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
	want := []string{"poll_stopped", "drain_relays", "analytics_stopped", "close_history", "close_store"}
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
		if len(res.ArchivedTables) != 2 || len(res.SubmittedTables) != 5 {
			t.Fatalf("unexpected result: %+v", res)
		}
		entries, err := os.ReadDir(archiveDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 4 {
			t.Fatalf("expected 4 archive files, got %d", len(entries))
		}
		mu.Lock()
		defer mu.Unlock()
		if len(ops) != 7 || ops[0] != "SELECT" || ops[1] != "SELECT" {
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
