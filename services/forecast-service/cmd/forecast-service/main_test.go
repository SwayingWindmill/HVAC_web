package main

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

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
