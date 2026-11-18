package fdd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
)

// An evaluation is one ClickHouse read and one Postgres write; a minute's lease covers it.
const (
	evaluationLease       = time.Minute
	finalizeTimeout       = 5 * time.Second
	defaultJobTimeout     = time.Minute
)

type evaluationJobQueue interface {
	Claim(ctx context.Context, workerID string, batch int, leaseDuration time.Duration, now time.Time) ([]schedulerjob.Job, error)
	Start(ctx context.Context, job schedulerjob.Job, leaseDuration time.Duration, now time.Time) (bool, error)
	Complete(ctx context.Context, job schedulerjob.Job, output any, now time.Time) error
	Fail(ctx context.Context, job schedulerjob.Job, errorCode string, executionErr error, retryable bool, now time.Time) error
}

type lowDeltaTEvaluator interface {
	EvaluateLowDeltaT(ctx context.Context, input EvaluationRequest) (EvaluationResult, error)
}

func (store *PostgresStore) EvaluationQueue() (*schedulerjob.Queue, error) {
	return schedulerjob.NewQueue(store.pool, EvaluationJobType)
}

// RunScheduledEvaluations claims the due FDD_EVALUATION jobs and evaluates each one. A window
// without readings is a settled outcome, not a retry; a failed read or write is retried.
func RunScheduledEvaluations(ctx context.Context, queue *schedulerjob.Queue, service *Service, workerID string, batch int) error {
	return RunScheduledEvaluationsWithLogger(ctx, queue, service, workerID, batch, slog.Default())
}

// RunScheduledEvaluationsWithLogger claims and evaluates due FDD_EVALUATION jobs with structured logging.
func RunScheduledEvaluationsWithLogger(ctx context.Context, queue evaluationJobQueue, service lowDeltaTEvaluator, workerID string, batch int, logger *slog.Logger) error {
	if ctx.Err() != nil {
		return nil
	}
	jobs, err := queue.Claim(ctx, workerID, batch, evaluationLease, time.Now().UTC())
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("claim FDD evaluations: %w", err)
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return nil
		}
		started, startErr := queue.Start(ctx, job, evaluationLease, time.Now().UTC())
		if startErr != nil {
			if ctx.Err() == nil && logger != nil {
				logger.Warn("fdd_evaluation_start_failed", "error_code", "FDD_JOB_START_FAILED", "job_id", job.JobID, "error", startErr)
			}
			continue
		}
		if !started {
			continue
		}
		request, planErr := ScheduledRequest(job)
		if planErr != nil {
			finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
			finish(queue.Fail(finalizeCtx, job, "FDD_EVALUATION_INVALID", planErr, false, time.Now().UTC()), job, logger)
			finalizeCancel()
			continue
		}
		timeout := time.Duration(job.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = defaultJobTimeout
		}
		jobCtx, cancel := context.WithTimeout(ctx, timeout)
		result, evaluationErr := service.EvaluateLowDeltaT(jobCtx, request)
		cancel()

		finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
		switch {
		case evaluationErr != nil && ctx.Err() != nil:
			finish(queue.Fail(finalizeCtx, job, "FDD_EVALUATION_FAILED", evaluationErr, true, time.Now().UTC()), job, logger)
		case errors.Is(evaluationErr, ErrRuleNotFound):
			finish(queue.Fail(finalizeCtx, job, "FDD_RULE_UNAVAILABLE", evaluationErr, false, time.Now().UTC()), job, logger)
		case errors.Is(evaluationErr, ErrNoLoopEvidence):
			finish(queue.Fail(finalizeCtx, job, "FDD_EVIDENCE_UNAVAILABLE", evaluationErr, false, time.Now().UTC()), job, logger)
		case evaluationErr != nil:
			finish(queue.Fail(finalizeCtx, job, "FDD_EVALUATION_FAILED", evaluationErr, true, time.Now().UTC()), job, logger)
		default:
			output := map[string]any{"status": result.Status, "deltaTC": result.DeltaTC, "operatingShare": result.Window.OperatingShare, "lowShare": result.Window.LowShare}
			if result.Window.Suppressed {
				output["suppressed"] = true
				output["suppressionReason"] = result.Window.SuppressionReason
				output["scheduleState"] = result.Window.ScheduleState
				if result.Window.SuppressionDetail != "" {
					output["suppressionDetail"] = result.Window.SuppressionDetail
				}
			}
			if result.Finding != nil {
				output["findingId"] = result.Finding.ID
			}
			finish(queue.Complete(finalizeCtx, job, output, time.Now().UTC()), job, logger)
		}
		finalizeCancel()
	}
	return nil
}

func finish(err error, job schedulerjob.Job, logger *slog.Logger) {
	if err != nil && logger != nil {
		logger.Warn("fdd_evaluation_finalize_failed", "error_code", "FDD_JOB_FINALIZE_FAILED", "job_id", job.JobID, "error", err)
	}
}
