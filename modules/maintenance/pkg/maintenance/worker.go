package maintenance

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

type Worker struct {
	Store         *Store
	WorkerID      string
	Batch         int
	LeaseDuration time.Duration
	Logger        *slog.Logger
}

func (w *Worker) Cycle(ctx context.Context, now time.Time) (int, error) {
	jobs, err := w.Store.ClaimJobs(ctx, w.WorkerID, w.Batch, w.LeaseDuration, now)
	if err != nil {
		return 0, err
	}
	for _, job := range jobs {
		w.process(ctx, job)
	}
	return len(jobs), nil
}

func (w *Worker) process(ctx context.Context, job Job) {
	now := time.Now().UTC()
	started, err := w.Store.StartJob(ctx, job, w.LeaseDuration, now)
	if err != nil || !started {
		if err != nil {
			w.Logger.Warn("maintenance_job_start_failed", "job_type", job.Type, "error_code", "MAINTENANCE_JOB_START_FAILED")
		}
		return
	}

	execCtx, cancel := context.WithTimeout(ctx, time.Duration(job.TimeoutSeconds)*time.Second)
	defer cancel()
	type result struct {
		output map[string]any
		err    error
	}
	done := make(chan result, 1)
	go func() {
		output, executeErr := w.Store.ExecuteJob(execCtx, job, time.Now().UTC())
		done <- result{output: output, err: executeErr}
	}()

	renewEvery := w.LeaseDuration / 3
	if renewEvery < time.Second {
		renewEvery = time.Second
	}
	ticker := time.NewTicker(renewEvery)
	defer ticker.Stop()
	for {
		select {
		case outcome := <-done:
			finishedAt := time.Now().UTC()
			if outcome.err == nil {
				if err := w.Store.CompleteJob(context.Background(), job, outcome.output, finishedAt); err != nil {
					w.Logger.Warn("maintenance_job_complete_failed", "job_type", job.Type, "error_code", "MAINTENANCE_JOB_COMPLETE_FAILED")
				}
				return
			}
			var executionErr *ExecutionError
			if errors.As(outcome.err, &executionErr) {
				_ = w.Store.FailJob(context.Background(), job, executionErr.Code, outcome.err, executionErr.Retryable, finishedAt)
			} else {
				_ = w.Store.FailJob(context.Background(), job, "MAINTENANCE_JOB_FAILED", outcome.err, true, finishedAt)
			}
			return
		case <-ticker.C:
			cancelRequested, renewErr := w.Store.RenewJobLease(ctx, job, w.LeaseDuration, time.Now().UTC())
			if renewErr != nil {
				cancel()
				w.Logger.Warn("maintenance_job_lease_lost", "job_type", job.Type, "error_code", "MAINTENANCE_JOB_LEASE_LOST")
				return
			}
			if cancelRequested {
				cancel()
				_ = w.Store.CancelJob(context.Background(), job, time.Now().UTC())
				return
			}
		case <-execCtx.Done():
			if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
				_ = w.Store.FailJob(context.Background(), job, "MAINTENANCE_JOB_TIMEOUT", execCtx.Err(), true, time.Now().UTC())
			}
			return
		}
	}
}
