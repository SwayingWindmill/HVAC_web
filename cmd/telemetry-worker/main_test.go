package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
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
