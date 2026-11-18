package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

func TestRunSupervisedRecoversPanicAndDrains(t *testing.T) {
	var logs bytes.Buffer
	logger := observability.NewJSONLogger(&logs, slog.LevelInfo)
	metrics := observability.NewRegistry()

	ctx, cancel := context.WithCancel(context.Background())
	var runs atomic.Int32
	var wg sync.WaitGroup
	runSupervised(ctx, &wg, logger, metrics, "mv.test_worker", 5*time.Millisecond, func() {
		if runs.Add(1) == 1 {
			panic("boom")
		}
		<-ctx.Done()
	})

	deadline := time.Now().Add(time.Second)
	for runs.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if runs.Load() < 2 {
		t.Fatalf("expected restart after panic, runs=%d", runs.Load())
	}
	cancel()
	waitBackground(&wg, time.Second, logger)
	if !bytes.Contains(logs.Bytes(), []byte("GOROUTINE_PANIC_RECOVERED")) {
		t.Fatalf("expected JSON panic log, got: %s", logs.String())
	}
}

func TestStartServerShutdownWaitsForInFlightHTTP(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(started)
			<-release
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}),
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = srv.Serve(ln)
	}()

	reqErrCh := make(chan error, 1)
	go func() {
		resp, reqErr := http.Get("http://" + ln.Addr().String() + "/slow")
		if reqErr == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		reqErrCh <- reqErr
	}()
	<-started

	shutdownDone, shutdownErr := startServerShutdown(2*time.Second, nil, nil, "mv.shutdown", srv)
	select {
	case <-shutdownDone:
		t.Fatal("shutdownDone closed while request was in flight")
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	<-shutdownDone
	if err := shutdownErr(); err != nil {
		t.Fatalf("shutdown returned error: %v", err)
	}
	if err := <-reqErrCh; err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	waitBackground(&wg, time.Second, nil)
}

type shutdownRecordingExporter struct {
	record func(string)
}

func (e *shutdownRecordingExporter) Export(context.Context, []observability.SpanData) error {
	return nil
}

func (e *shutdownRecordingExporter) Shutdown(context.Context) error {
	if e.record != nil {
		e.record("telemetry_shutdown")
	}
	return nil
}

func TestShutdownServiceRuntimeStrictOrder(t *testing.T) {
	var mu sync.Mutex
	var events []string
	record := func(event string) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	}

	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service:  "mv-service-test",
		Exporter: &shutdownRecordingExporter{record: record},
	})
	telemetry.MarkReady()

	httpEntered := make(chan struct{})
	releaseHTTP := make(chan struct{})
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(httpEntered)
			<-releaseHTTP
			record("http_handler_done")
			w.WriteHeader(http.StatusOK)
		}),
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var httpWG sync.WaitGroup
	httpWG.Add(1)
	go func() {
		defer httpWG.Done()
		_ = srv.Serve(ln)
		record("http_server_exited")
	}()

	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	var bgWG sync.WaitGroup
	workerStarted := make(chan struct{})
	runSupervised(workerCtx, &bgWG, nil, telemetry.Metrics, "mv.test_worker", 5*time.Millisecond, func() {
		close(workerStarted)
		<-workerCtx.Done()
		record("worker_exited")
	})
	<-workerStarted

	reqErrCh := make(chan error, 1)
	go func() {
		resp, reqErr := http.Get("http://" + ln.Addr().String() + "/slow")
		if reqErr == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		reqErrCh <- reqErr
	}()
	<-httpEntered

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- shutdownServiceRuntime(
			nil,
			telemetry,
			2*time.Second,
			2*time.Second,
			"mv.shutdown",
			&httpWG,
			cancelWorkers,
			&bgWG,
			func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = telemetry.Shutdown(ctx)
			},
			srv,
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
		t.Fatalf("workerCtx canceled before HTTP shutdown completed: %v", workerCtx.Err())
	}

	close(releaseHTTP)
	if err := <-reqErrCh; err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("shutdownServiceRuntime failed: %v", err)
	}

	mu.Lock()
	got := slices.Clone(events)
	mu.Unlock()
	httpHandlerIdx := slices.Index(got, "http_handler_done")
	httpServerIdx := slices.Index(got, "http_server_exited")
	workerIdx := slices.Index(got, "worker_exited")
	telemetryIdx := slices.Index(got, "telemetry_shutdown")
	if httpHandlerIdx < 0 || httpServerIdx < 0 || workerIdx < 0 || telemetryIdx < 0 {
		t.Fatalf("missing expected shutdown events: %v", got)
	}
	if httpHandlerIdx >= workerIdx || httpServerIdx >= workerIdx {
		t.Fatalf("expected HTTP drain and server exit before worker_exited: %v", got)
	}
	if workerIdx >= telemetryIdx {
		t.Fatalf("expected worker_exited before telemetry_shutdown: %v", got)
	}
}
