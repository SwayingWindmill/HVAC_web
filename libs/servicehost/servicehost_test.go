package servicehost

import (
	"bytes"
	"context"
	"errors"
	"io"
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

func TestEnvHelpersParseAndValidate(t *testing.T) {
	t.Setenv("TEST_HOST_STR", "  val ")
	if got := EnvOrDefault("TEST_HOST_STR", "fallback"); got != "val" {
		t.Fatalf("EnvOrDefault = %q, want val", got)
	}
	if got := EnvOrDefault("TEST_HOST_MISSING", "fallback"); got != "fallback" {
		t.Fatalf("EnvOrDefault missing = %q, want fallback", got)
	}
	if got, err := RequiredEnv("TEST_HOST_STR"); err != nil || got != "val" {
		t.Fatalf("RequiredEnv = (%q, %v)", got, err)
	}
	if _, err := RequiredEnv("TEST_HOST_MISSING"); err == nil {
		t.Fatal("RequiredEnv missing accepted")
	}

	t.Setenv("TEST_HOST_DUR", "30s")
	if got, err := DurationEnv("TEST_HOST_DUR", time.Second); err != nil || got != 30*time.Second {
		t.Fatalf("DurationEnv = (%v, %v)", got, err)
	}
	t.Setenv("TEST_HOST_DUR", "-1s")
	if _, err := DurationEnv("TEST_HOST_DUR", time.Second); err == nil {
		t.Fatal("DurationEnv negative accepted")
	}

	t.Setenv("TEST_HOST_INT", "10")
	if got, err := BoundedIntEnv("TEST_HOST_INT", 5, 1, 100); err != nil || got != 10 {
		t.Fatalf("BoundedIntEnv = (%d, %v)", got, err)
	}
	t.Setenv("TEST_HOST_INT", "101")
	if _, err := BoundedIntEnv("TEST_HOST_INT", 5, 1, 100); err == nil {
		t.Fatal("BoundedIntEnv out-of-range accepted")
	}

	id := DefaultWorkerID("service")
	if !strings.Contains(id, "-") {
		t.Fatalf("DefaultWorkerID = %q", id)
	}
}

func TestRunSupervisedRecoversPanicAndDrains(t *testing.T) {
	var logs bytes.Buffer
	logger := observability.NewJSONLogger(&logs, slog.LevelInfo)
	metrics := observability.NewRegistry()

	ctx, cancel := context.WithCancel(context.Background())
	var runs atomic.Int32
	restarted := make(chan struct{})
	var wg sync.WaitGroup
	RunSupervised(ctx, &wg, logger, metrics, "servicehost.test_worker", 5*time.Millisecond, func() {
		if runs.Add(1) == 1 {
			panic("boom")
		}
		close(restarted)
		<-ctx.Done()
	})

	select {
	case <-restarted:
	case <-time.After(time.Second):
		t.Fatalf("expected restart after panic, runs=%d", runs.Load())
	}
	cancel()
	WaitBackground(&wg, time.Second, logger)
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

	shutdownDone, shutdownErr := StartServerShutdown(2*time.Second, nil, nil, "servicehost.shutdown", srv)
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
	WaitBackground(&wg, time.Second, nil)
}

func TestShutdownServersIgnoresNilAndErrServerClosed(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.NewServeMux(), ReadHeaderTimeout: time.Second}
	var wg sync.WaitGroup
	wg.Add(1)
	serveErr := make(chan error, 1)
	go func() {
		defer wg.Done()
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	if err := ShutdownServers(2*time.Second, srv, nil); err != nil {
		t.Fatalf("ShutdownServers err=%v", err)
	}
	WaitBackground(&wg, 2*time.Second, nil)
	select {
	case err := <-serveErr:
		t.Fatalf("unexpected server error: %v", err)
	default:
	}
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

func TestShutdownRuntimeStrictOrder(t *testing.T) {
	var mu sync.Mutex
	var events []string
	record := func(event string) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	}

	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service:  "servicehost-test",
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
	RunSupervised(workerCtx, &bgWG, nil, telemetry.Metrics, "servicehost.test_worker", 5*time.Millisecond, func() {
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
		shutdownDone <- ShutdownRuntime(
			nil,
			telemetry,
			2*time.Second,
			2*time.Second,
			"servicehost.shutdown",
			&httpWG,
			cancelWorkers,
			&bgWG,
			NewTelemetryShutdown(telemetry, time.Second),
			srv,
		)
	}()

	deadlineTimer := time.NewTimer(time.Second)
	defer deadlineTimer.Stop()
	readyPoll := time.NewTicker(time.Millisecond)
	defer readyPoll.Stop()
	for telemetry.Ready() {
		select {
		case <-readyPoll.C:
		case <-deadlineTimer.C:
			close(releaseHTTP)
			t.Fatal("expected telemetry.MarkNotReady() immediately on shutdown")
		}
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
		t.Fatalf("ShutdownRuntime failed: %v", err)
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
