package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/libs/observability"
	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
	"github.com/quanlaihe/hvac-web/services/forecast-service/internal/forecast"
)

type fakeForecastQueue struct {
	mu              sync.Mutex
	renewFn         func(context.Context, schedulerjob.Job, time.Duration, time.Time) (bool, error)
	failCalls       int
	completeCalls   int
	lastFailCode    string
	lastFailRetry   bool
	lastFinalizeErr error
	lastCompleteErr error
}

func (f *fakeForecastQueue) Claim(context.Context, string, int, time.Duration, time.Time) ([]schedulerjob.Job, error) {
	return nil, nil
}

func (f *fakeForecastQueue) Start(context.Context, schedulerjob.Job, time.Duration, time.Time) (bool, error) {
	return true, nil
}

func (f *fakeForecastQueue) Renew(ctx context.Context, job schedulerjob.Job, leaseDuration time.Duration, now time.Time) (bool, error) {
	if f.renewFn != nil {
		return f.renewFn(ctx, job, leaseDuration, now)
	}
	return false, nil
}

func (f *fakeForecastQueue) Complete(ctx context.Context, _ schedulerjob.Job, _ any, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completeCalls++
	f.lastCompleteErr = ctx.Err()
	return nil
}

func (f *fakeForecastQueue) Fail(ctx context.Context, _ schedulerjob.Job, errorCode string, _ error, retryable bool, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failCalls++
	f.lastFailCode = errorCode
	f.lastFailRetry = retryable
	f.lastFinalizeErr = ctx.Err()
	return nil
}

type fakeForecastLoader struct{}

func (fakeForecastLoader) LoadForecastRequest(context.Context, string, string, forecast.SchedulerForecastReference) (forecast.Request, error) {
	return forecast.Request{ForecastJobID: "fj-1", ForecastSnapshotID: "fs-1"}, nil
}

type fakeForecastRunner struct {
	runFn func(context.Context, forecast.Request) ([]forecast.Point, error)
}

func (f fakeForecastRunner) Forecast(ctx context.Context, req forecast.Request) ([]forecast.Point, error) {
	return f.runFn(ctx, req)
}

func validForecastJob(timeoutSeconds int) schedulerjob.Job {
	const (
		jobID      = "01990000-1880-7000-8000-000000000001"
		tenantID   = "01990000-3000-7000-8000-000000000001"
		siteID     = "01990000-5000-7000-8000-000000000001"
		snapshotID = "01990000-1890-7000-8000-000000000001"
	)
	payload, _ := json.Marshal(forecast.SchedulerForecastReference{
		ForecastJobID:      jobID,
		ForecastSnapshotID: snapshotID,
	})
	return schedulerjob.Job{
		JobID:          jobID,
		JobType:        forecast.RunJobType,
		TenantID:       tenantID,
		SiteID:         siteID,
		Payload:        payload,
		TimeoutSeconds: timeoutSeconds,
		AttemptNo:      1,
		MaxAttempts:    3,
	}
}

func TestExecuteForecastJobDecouplesSIGTERMFromOperatorCancel(t *testing.T) {
	t.Run("SIGTERM marks retryable=true and never marks operator canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		queue := &fakeForecastQueue{}
		runner := fakeForecastRunner{runFn: func(jobCtx context.Context, _ forecast.Request) ([]forecast.Point, error) {
			cancel()
			<-jobCtx.Done()
			return nil, jobCtx.Err()
		}}

		executeForecastJob(ctx, queue, fakeForecastLoader{}, runner, validForecastJob(30), time.Minute, 10*time.Millisecond, nil, nil)

		if queue.failCalls != 1 {
			t.Fatalf("failCalls = %d, want 1", queue.failCalls)
		}
		if queue.lastFailCode == "FORECAST_CANCELED_BY_OPERATOR" {
			t.Fatalf("SIGTERM must not record FORECAST_CANCELED_BY_OPERATOR")
		}
		if !queue.lastFailRetry {
			t.Fatalf("SIGTERM must record retryable=true")
		}
		if queue.lastFinalizeErr != nil {
			t.Fatalf("finalization context must survive SIGTERM cancel, got err=%v", queue.lastFinalizeErr)
		}
	})

	t.Run("operator cancelRequested marks FORECAST_CANCELED_BY_OPERATOR and retryable=false", func(t *testing.T) {
		queue := &fakeForecastQueue{
			renewFn: func(context.Context, schedulerjob.Job, time.Duration, time.Time) (bool, error) {
				return true, nil
			},
		}
		runner := fakeForecastRunner{runFn: func(jobCtx context.Context, _ forecast.Request) ([]forecast.Point, error) {
			<-jobCtx.Done()
			return nil, jobCtx.Err()
		}}

		executeForecastJob(context.Background(), queue, fakeForecastLoader{}, runner, validForecastJob(30), time.Minute, 5*time.Millisecond, nil, nil)

		if queue.failCalls != 1 {
			t.Fatalf("failCalls = %d, want 1", queue.failCalls)
		}
		if queue.lastFailCode != "FORECAST_CANCELED_BY_OPERATOR" {
			t.Fatalf("errorCode = %q, want FORECAST_CANCELED_BY_OPERATOR", queue.lastFailCode)
		}
		if queue.lastFailRetry {
			t.Fatalf("operator cancelRequested must record retryable=false")
		}
	})

	t.Run("transient lease renew failure keeps retryable=true", func(t *testing.T) {
		queue := &fakeForecastQueue{
			renewFn: func(context.Context, schedulerjob.Job, time.Duration, time.Time) (bool, error) {
				return false, errors.New("temporary postgres timeout")
			},
		}
		runner := fakeForecastRunner{runFn: func(jobCtx context.Context, _ forecast.Request) ([]forecast.Point, error) {
			<-jobCtx.Done()
			return nil, jobCtx.Err()
		}}

		executeForecastJob(context.Background(), queue, fakeForecastLoader{}, runner, validForecastJob(30), time.Minute, 5*time.Millisecond, nil, nil)

		if queue.failCalls != 1 {
			t.Fatalf("failCalls = %d, want 1", queue.failCalls)
		}
		if !queue.lastFailRetry {
			t.Fatalf("transient renew error must stay retryable=true")
		}
		if queue.lastFailCode == "FORECAST_CANCELED_BY_OPERATOR" {
			t.Fatalf("transient renew error must not record operator cancel")
		}
	})
}

func TestRunSupervisedRecoversPanicAndDrains(t *testing.T) {
	var buf bytes.Buffer
	logger := observability.NewJSONLogger(&buf, slog.LevelInfo)
	metrics := observability.NewRegistry()

	ctx, cancel := context.WithCancel(context.Background())
	var runs atomic.Int32
	var wg sync.WaitGroup

	runSupervised(ctx, &wg, logger, metrics, "forecast.test_worker", 5*time.Millisecond, func() {
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
		t.Fatalf("expected supervised restart after panic, runs=%d", runs.Load())
	}
	cancel()
	waitBackground(&wg, time.Second, logger)

	if !bytes.Contains(buf.Bytes(), []byte("GOROUTINE_PANIC_RECOVERED")) {
		t.Fatalf("expected structured JSON panic recovery log, got: %s", buf.String())
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
			_, _ = w.Write([]byte("done"))
		}),
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer listener.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = srv.Serve(listener)
	}()

	respCh := make(chan error, 1)
	go func() {
		resp, reqErr := http.Get("http://" + listener.Addr().String() + "/slow")
		if reqErr == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		respCh <- reqErr
	}()
	<-started

	shutdownDone, shutdownErr := startServerShutdown(2*time.Second, nil, nil, "forecast.shutdown", srv)
	select {
	case <-shutdownDone:
		t.Fatal("shutdownDone closed while in-flight HTTP request was still running")
	case <-time.After(30 * time.Millisecond):
	}

	close(release)
	<-shutdownDone
	if err := shutdownErr(); err != nil {
		t.Fatalf("shutdown returned error: %v", err)
	}
	if err := <-respCh; err != nil {
		t.Fatalf("in-flight HTTP request failed: %v", err)
	}
	waitBackground(&wg, time.Second, nil)
}
