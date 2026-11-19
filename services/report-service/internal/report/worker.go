package report

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
)

// GenerationJobType is the Scheduler job that takes a Site's monthly report snapshot.
const GenerationJobType = "REPORT_GENERATE"

// A month reads a few thousand hours from ClickHouse; two minutes covers a slow read.
const (
	generationLease   = 2 * time.Minute
	finalizeTimeout   = 5 * time.Second
	defaultJobTimeout = 5 * time.Minute
)

type generationJobQueue interface {
	Claim(ctx context.Context, workerID string, batch int, leaseDuration time.Duration, now time.Time) ([]schedulerjob.Job, error)
	Start(ctx context.Context, job schedulerjob.Job, leaseDuration time.Duration, now time.Time) (bool, error)
	Complete(ctx context.Context, job schedulerjob.Job, output any, now time.Time) error
	Fail(ctx context.Context, job schedulerjob.Job, errorCode string, executionErr error, retryable bool, now time.Time) error
}

type monthlyReportGenerator interface {
	Generate(ctx context.Context, tenantID, siteID, month string, generatedByJob *string) (StoredSnapshot, error)
	MonthBefore(ctx context.Context, tenantID, siteID string, at time.Time) (string, error)
}

// RunScheduledGenerations claims due REPORT_GENERATE jobs and snapshots each Site's month: the
// month the job names, or else the Site-local month before the one it was scheduled in.
func RunScheduledGenerations(ctx context.Context, queue *schedulerjob.Queue, service *Service, workerID string, batch int) error {
	return RunScheduledGenerationsWithLogger(ctx, queue, service, workerID, batch, slog.Default())
}

// RunScheduledGenerationsWithLogger claims due REPORT_GENERATE jobs with structured logging.
func RunScheduledGenerationsWithLogger(ctx context.Context, queue generationJobQueue, service monthlyReportGenerator, workerID string, batch int, logger *slog.Logger) error {
	if ctx.Err() != nil {
		return nil
	}
	jobs, err := queue.Claim(ctx, workerID, batch, generationLease, time.Now().UTC())
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("claim report generations: %w", err)
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return nil
		}
		started, startErr := queue.Start(ctx, job, generationLease, time.Now().UTC())
		if startErr != nil {
			if ctx.Err() == nil && logger != nil {
				logger.Warn("report_generation_start_failed", "error_code", "REPORT_JOB_START_FAILED", "job_id", job.JobID, "error", startErr)
			}
			continue
		}
		if !started {
			continue
		}
		month, monthErr := jobMonth(ctx, service, job)
		if monthErr != nil {
			finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
			retryable := ctx.Err() != nil
			code := "REPORT_GENERATE_INVALID"
			if retryable {
				code = "REPORT_GENERATE_FAILED"
			}
			finish(queue.Fail(finalizeCtx, job, code, monthErr, retryable, time.Now().UTC()), job, logger)
			finalizeCancel()
			continue
		}
		timeout := time.Duration(job.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = defaultJobTimeout
		}
		jobCtx, cancel := context.WithTimeout(ctx, timeout)
		jobID := job.JobID
		stored, generationErr := service.Generate(jobCtx, job.TenantID, job.SiteID, month, &jobID)
		cancel()

		finalizeCtx, finalizeCancel := context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
		switch {
		case generationErr != nil && ctx.Err() != nil:
			finish(queue.Fail(finalizeCtx, job, "REPORT_GENERATE_FAILED", generationErr, true, time.Now().UTC()), job, logger)
		case errors.Is(generationErr, ErrInvalidMonth) || errors.Is(generationErr, ErrMonthNotOver):
			finish(queue.Fail(finalizeCtx, job, "REPORT_GENERATE_INVALID", generationErr, false, time.Now().UTC()), job, logger)
		case generationErr != nil:
			finish(queue.Fail(finalizeCtx, job, "REPORT_GENERATE_FAILED", generationErr, true, time.Now().UTC()), job, logger)
		default:
			finish(queue.Complete(finalizeCtx, job, map[string]any{"month": stored.Month, "version": stored.Version, "snapshotId": stored.ID}, time.Now().UTC()), job, logger)
		}
		finalizeCancel()
	}
	return nil
}

func jobMonth(ctx context.Context, service monthlyReportGenerator, job schedulerjob.Job) (string, error) {
	var payload struct {
		Month string `json:"month"`
	}
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return "", fmt.Errorf("report generation payload: %w", err)
		}
	}
	if payload.Month != "" {
		return payload.Month, nil
	}
	return service.MonthBefore(ctx, job.TenantID, job.SiteID, job.ScheduledFor)
}

func finish(err error, job schedulerjob.Job, logger *slog.Logger) {
	if err != nil && logger != nil {
		logger.Warn("report_generation_finalize_failed", "error_code", "REPORT_JOB_FINALIZE_FAILED", "job_id", job.JobID, "error", err)
	}
}
