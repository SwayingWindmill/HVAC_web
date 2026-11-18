package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

type schedulerRecordingExporter struct {
	record func(string)
}

func (e *schedulerRecordingExporter) Export(context.Context, []observability.SpanData) error {
	return nil
}

func (e *schedulerRecordingExporter) Shutdown(context.Context) error {
	if e.record != nil {
		e.record("telemetry_shutdown")
	}
	return nil
}

func TestShutdownSchedulerMarksNotReadyImmediatelyAndOrdersTeardown(t *testing.T) {
	var mu sync.Mutex
	var events []string
	record := func(ev string) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}

	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service:  "scheduler-test",
		Exporter: &schedulerRecordingExporter{record: record},
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
	workerDone := make(chan struct{})
	workerStarted := make(chan struct{})
	go func() {
		defer close(workerDone)
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
		shutdownScheduler(
			nil,
			telemetry,
			diagSrv,
			diagDone,
			cancelWorker,
			workerDone,
			func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = telemetry.Shutdown(ctx)
			},
			func() { record("store_closed") },
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
		t.Fatal("shutdownScheduler timed out")
	}

	mu.Lock()
	got := slices.Clone(events)
	mu.Unlock()
	diagHandlerIdx := slices.Index(got, "diag_handler_done")
	diagGoroutineIdx := slices.Index(got, "diag_goroutine_done")
	workerIdx := slices.Index(got, "worker_done")
	telemetryIdx := slices.Index(got, "telemetry_shutdown")
	storeIdx := slices.Index(got, "store_closed")
	if diagHandlerIdx < 0 || diagGoroutineIdx < 0 || workerIdx < 0 || telemetryIdx < 0 || storeIdx < 0 {
		t.Fatalf("missing expected shutdown events: %v", got)
	}
	if diagHandlerIdx >= workerIdx || diagGoroutineIdx >= workerIdx {
		t.Fatalf("expected diagnostics drain and exit before worker_done: %v", got)
	}
	if workerIdx >= telemetryIdx || telemetryIdx >= storeIdx {
		t.Fatalf("expected worker_done < telemetry_shutdown < store_closed: %v", got)
	}
}
