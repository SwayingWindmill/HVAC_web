package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
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
	runSupervised(ctx, &wg, logger, metrics, "report.test_worker", 5*time.Millisecond, func() {
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

	shutdownDone, shutdownErr := startServerShutdown(2*time.Second, nil, nil, "report.shutdown", srv)
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
