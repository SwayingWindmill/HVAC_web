package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
)

func TestDrainAndShutdownWaitsForHTTPBeforeReturn(t *testing.T) {
	logger := observability.NewJSONLogger(io.Discard, slog.LevelInfo)
	telemetry := observability.NewRuntime(observability.RuntimeConfig{
		Service:   "identity-service",
		QueueSize: 8,
		Logger:    logger,
	})
	telemetry.MarkReady()

	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})

	httpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(entered)
			<-release
			w.WriteHeader(http.StatusOK)
			close(finished)
		}),
	}
	diagLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := &http.Server{Handler: telemetry.DiagnosticsHandler()}

	serveErr := make(chan error, 2)
	var httpWG sync.WaitGroup
	httpWG.Add(2)
	go func() {
		defer httpWG.Done()
		if err := httpServer.Serve(httpLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()
	go func() {
		defer httpWG.Done()
		if err := diagnostics.Serve(diagLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	reqDone := make(chan int, 1)
	go func() {
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + httpLn.Addr().String() + "/slow")
		if err != nil {
			reqDone <- 0
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		reqDone <- resp.StatusCode
	}()

	<-entered
	sigCtx, cancelSig := context.WithCancel(context.Background())
	shutdownResult := make(chan int, 1)
	go func() {
		shutdownResult <- drainAndShutdown(sigCtx, serveErr, logger, telemetry, httpServer, diagnostics, &httpWG, 5*time.Second)
	}()

	cancelSig()

	deadline := time.Now().Add(time.Second)
	for telemetry.Ready() {
		if time.Now().After(deadline) {
			t.Fatal("expected telemetry to be marked not-ready immediately on shutdown")
		}
		time.Sleep(5 * time.Millisecond)
	}

	select {
	case code := <-shutdownResult:
		t.Fatalf("drainAndShutdown returned early with code %d while HTTP request was in flight", code)
	case <-time.After(60 * time.Millisecond):
	}

	close(release)
	<-finished
	if status := <-reqDone; status != http.StatusOK {
		t.Fatalf("in-flight request status = %d, want 200", status)
	}
	if code := <-shutdownResult; code != 0 {
		t.Fatalf("drainAndShutdown exit code = %d, want 0", code)
	}
}
