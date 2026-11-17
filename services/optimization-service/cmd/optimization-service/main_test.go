package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/services/optimization-service/internal/optimization"
)

type stubMonitorRunner struct {
	calls atomic.Int32
	fn    func(ctx context.Context, call int32) error
}

func (s *stubMonitorRunner) RunMonitorPass(ctx context.Context, _ time.Time) (optimization.MonitorPassSummary, error) {
	call := s.calls.Add(1)
	if s.fn != nil {
		return optimization.MonitorPassSummary{}, s.fn(ctx, call)
	}
	return optimization.MonitorPassSummary{}, nil
}

type stubReconciler struct {
	calls atomic.Int32
	fn    func(ctx context.Context, call int32) (int, error)
}

func (s *stubReconciler) Reconcile(ctx context.Context, _ time.Time, _ int) (int, error) {
	call := s.calls.Add(1)
	if s.fn != nil {
		return s.fn(ctx, call)
	}
	return 0, nil
}

func TestRunMonitorLoopAndReconcileLoopRespectContextCancellation(t *testing.T) {
	monitorCtx, monitorCancel := context.WithCancel(t.Context())
	defer monitorCancel()

	monitor := &stubMonitorRunner{
		fn: func(ctx context.Context, call int32) error {
			if _, ok := ctx.Deadline(); !ok {
				t.Errorf("monitor pass context missing timeout deadline")
			}
			if call >= 2 {
				monitorCancel()
			}
			return nil
		},
	}
	doneMonitor := make(chan struct{})
	go func() {
		defer close(doneMonitor)
		runMonitorLoop(monitorCtx, 5*time.Millisecond, monitor)
	}()
	select {
	case <-doneMonitor:
	case <-time.After(2 * time.Second):
		t.Fatal("runMonitorLoop did not stop after context cancellation")
	}
	if monitor.calls.Load() < 2 {
		t.Fatalf("monitor calls=%d, want >= 2", monitor.calls.Load())
	}

	reconcileCtx, reconcileCancel := context.WithCancel(t.Context())
	defer reconcileCancel()
	observedParentCancel := make(chan bool, 1)
	reconciler := &stubReconciler{
		fn: func(ctx context.Context, call int32) (int, error) {
			if _, ok := ctx.Deadline(); !ok {
				t.Errorf("reconcile context missing timeout deadline")
			}
			if call == 1 {
				reconcileCancel()
				select {
				case <-ctx.Done():
					observedParentCancel <- true
				case <-time.After(time.Second):
					observedParentCancel <- false
				}
			}
			return 0, nil
		},
	}
	doneReconcile := make(chan struct{})
	go func() {
		defer close(doneReconcile)
		runReconcileLoop(reconcileCtx, 5*time.Millisecond, reconciler)
	}()
	select {
	case <-doneReconcile:
	case <-time.After(2 * time.Second):
		t.Fatal("runReconcileLoop did not stop after context cancellation")
	}
	if !<-observedParentCancel {
		t.Fatal("reconcile context did not derive from parent ctx")
	}
}

func TestRunSupervisedRecoversPanicAndStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var logs bytes.Buffer
	logger := observability.NewJSONLogger(&logs, slog.LevelInfo)
	metrics := observability.NewRegistry()
	var wg sync.WaitGroup
	var attempts atomic.Int32

	runSupervised(ctx, &wg, logger, metrics, "optimization.test_worker", 5*time.Millisecond, func() {
		if attempts.Add(1) == 1 {
			panic("boom")
		}
		cancel()
	})

	wg.Wait()
	if attempts.Load() < 2 {
		t.Fatalf("attempts=%d, want >= 2 after panic restart", attempts.Load())
	}
	output := logs.String()
	if !strings.Contains(output, `"background_goroutine_panicked"`) || !strings.Contains(output, `"[REDACTED]"`) {
		t.Fatalf("expected redacted panic recovery log, got %s", output)
	}
}

func TestShutdownServersAndWaitBackground(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: optimization.HealthHandler(), ReadHeaderTimeout: time.Second}
	var wg sync.WaitGroup
	wg.Add(1)
	serveErr := make(chan error, 1)
	go func() {
		defer wg.Done()
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	if err := shutdownServers(2*time.Second, srv, nil); err != nil {
		t.Fatalf("shutdownServers err=%v", err)
	}
	waitBackground(&wg, 2*time.Second)
	select {
	case err := <-serveErr:
		t.Fatalf("unexpected server error: %v", err)
	default:
	}
}
