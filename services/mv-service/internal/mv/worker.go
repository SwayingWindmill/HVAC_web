package mv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
)

// EvaluationJobType is the Scheduler job that verifies a Site's settled days once a day.
const EvaluationJobType = "MV_EVALUATION"

// A run reads at most a few weeks of days from ClickHouse; five minutes covers a catch-up.
const (
	evaluationLease   = 5 * time.Minute
	finalizeTimeout   = 5 * time.Second
	defaultJobTimeout = 5 * time.Minute
)

type evaluationJobQueue interface {
	Claim(ctx context.Context, workerID string, batch int, leaseDuration time.Duration, now time.Time) ([]schedulerjob.Job, error)
	Start(ctx context.Context, job schedulerjob.Job, leaseDuration time.Duration, now time.Time) (bool, error)
	Complete(ctx context.Context, job schedulerjob.Job, output any, now time.Time) error
	Fail(ctx context.Context, job schedulerjob.Job, errorCode string, executionErr error, retryable bool, now time.Time) error
}

type dueDayVerifier interface {
	VerifyDueDays(ctx context.Context, tenantID, siteID string, asOf time.Time) error
}

// RunScheduledEvaluations claims due MV_EVALUATION jobs and verifies each Site's settled days up
// to the job's scheduled time. A failed read or write is retried; days already verified stay.
func RunScheduledEvaluations(ctx context.Context, queue *schedulerjob.Queue, service *Service, workerID string, batch int) error {
	return RunScheduledEvaluationsWithLogger(ctx, queue, service, workerID, batch, slog.Default())
}

// RunScheduledEvaluationsWithLogger claims due MV_EVALUATION jobs with structured logging.
func RunScheduledEvaluationsWithLogger(ctx context.Context, queue evaluationJobQueue, service dueDayVerifier, workerID string, batch int, logger *slog.Logger) error {
	if ctx.Err() != nil {
		return nil
	}
	jobs, err := queue.Claim(ctx, workerID, batch, evaluationLease, time.Now().UTC())
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("claim M&V evaluations: %w", err)
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return nil
		}
		started, startErr := queue.Start(ctx, job, evaluationLease, time.Now().UTC())
		if startErr != nil {
			if ctx.Err() == nil && logger != nil {
				logger.Warn("mv_evaluation_start_failed", "error_code", "MV_JOB_START_FAILED", "job_id", job.JobID, "error", startErr)
			}
			continue
		}
		if !started {
			continue
		}
		if job.TriggerType != "SCHEDULE" || job.ScheduledFor.IsZero() {
			finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
			finish(queue.Fail(finalizeCtx, job, "MV_EVALUATION_INVALID", errors.New("an M&V evaluation only comes from an MV_EVALUATION schedule"), false, time.Now().UTC()), job, logger)
			finalizeCancel()
			continue
		}
		timeout := time.Duration(job.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = defaultJobTimeout
		}
		jobCtx, cancel := context.WithTimeout(ctx, timeout)
		evaluationErr := service.VerifyDueDays(jobCtx, job.TenantID, job.SiteID, job.ScheduledFor)
		cancel()

		finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
		if evaluationErr != nil {
			finish(queue.Fail(finalizeCtx, job, "MV_EVALUATION_FAILED", evaluationErr, true, time.Now().UTC()), job, logger)
			finalizeCancel()
			continue
		}
		finish(queue.Complete(finalizeCtx, job, map[string]any{"settledBy": job.ScheduledFor.Add(-SettlingDelay).UTC()}, time.Now().UTC()), job, logger)
		finalizeCancel()
	}
	return nil
}

func finish(err error, job schedulerjob.Job, logger *slog.Logger) {
	if err != nil && logger != nil {
		logger.Warn("mv_evaluation_finalize_failed", "error_code", "MV_JOB_FINALIZE_FAILED", "job_id", job.JobID, "error", err)
	}
}
