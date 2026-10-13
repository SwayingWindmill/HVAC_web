package report

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/quanlaihe/hvac-web/libs/schedulerjob"
)

// GenerationJobType is the Scheduler job that takes a Site's monthly report snapshot.
const GenerationJobType = "REPORT_GENERATE"

// A month reads a few thousand hours from ClickHouse; two minutes covers a slow read.
const generationLease = 2 * time.Minute

// RunScheduledGenerations claims due REPORT_GENERATE jobs and snapshots each Site's month: the
// month the job names, or else the Site-local month before the one it was scheduled in.
func RunScheduledGenerations(ctx context.Context, queue *schedulerjob.Queue, service *Service, workerID string, batch int) error {
	jobs, err := queue.Claim(ctx, workerID, batch, generationLease, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("claim report generations: %w", err)
	}
	for _, job := range jobs {
		started, startErr := queue.Start(ctx, job, generationLease, time.Now().UTC())
		if startErr != nil {
			log.Printf("report generation %s start failed: %v", job.JobID, startErr)
			continue
		}
		if !started {
			continue
		}
		month, monthErr := jobMonth(ctx, service, job)
		if monthErr != nil {
			finish(queue.Fail(context.Background(), job, "REPORT_GENERATE_INVALID", monthErr, false, time.Now().UTC()), job)
			continue
		}
		jobCtx, cancel := context.WithTimeout(ctx, time.Duration(job.TimeoutSeconds)*time.Second)
		jobID := job.JobID
		stored, generationErr := service.Generate(jobCtx, job.TenantID, job.SiteID, month, &jobID)
		cancel()
		if generationErr != nil {
			finish(queue.Fail(context.Background(), job, "REPORT_GENERATE_FAILED", generationErr, true, time.Now().UTC()), job)
			continue
		}
		finish(queue.Complete(context.Background(), job, map[string]any{"month": stored.Month, "version": stored.Version, "snapshotId": stored.ID}, time.Now().UTC()), job)
	}
	return nil
}

func jobMonth(ctx context.Context, service *Service, job schedulerjob.Job) (string, error) {
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
	if job.ScheduledFor.IsZero() {
		return "", errors.New("a report generation names a month or comes from a schedule")
	}
	return service.MonthBefore(ctx, job.TenantID, job.SiteID, job.ScheduledFor)
}

func finish(err error, job schedulerjob.Job) {
	if err != nil {
		log.Printf("report generation %s could not be finalized: %v", job.JobID, err)
	}
}
